package fcvm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// JailerVMM is the production VMM. It provisions a jail chroot, launches
// firecracker under the jailer, and drives park/wake via the Firecracker API over
// the jail's unix socket. It is validated on metal (`make test-metal`); the
// orchestration around it (Manager) is proven cross-platform with fakes.
//
// Chroot model (Appendix B): jailer builds the chroot at
// <base>/firecracker/<id>/root and execs firecracker inside it, so every path the
// VM references must live within that root. We hardlink the shared read-only
// kernel/base rootfs in (cheap) and link the per-app layer / snapshot files, then
// reference them by their in-chroot basenames.
type JailerVMM struct {
	chrootBase     string        // /srv/fc/jail
	fcName         string        // chroot dir name jailer derives from the exec-file basename
	readyTimeout   time.Duration // WAKING/cold-boot readiness budget (spec §6)
	destroyWait    time.Duration // cap for DestroyWithExport's wait-for-exit; 0 => 10m
	exportMaxBytes int64         // cap for build-artifact copy-out; 0 => api.MaxExportedLayerBytes
	// storage is the artifact backend where snapshot blobs live per
	// #96 / ADR-025 axis 2. Restore resolves StorageKey → local tmp;
	// Snapshot Streams the produced mem blob back through Storage.Put.
	// Optional — when nil, Restore/Snapshot fall back to the legacy
	// MemPath/VMStatePath branch unchanged.
	storage storage.StorageBackend
	// mountHelperPath is a process-versioned copy of the jail helper on tmpfs.
	// Every VM hardlinks this shared inode into its chroot instead of copying
	// the executable across filesystems on every wake. Older bundles use vmmd.
	mountHelperMu   sync.Mutex
	mountHelperPath string

	mu      sync.Mutex
	proc    map[string]*exec.Cmd // instance -> running jailer process
	clients map[string]*http.Client
	// hcClient (issue #460 / ADR-053, ADR-057 / PR-D) is the
	// per-VMM *http.Client the waitReady HTTP probe reuses across
	// its 200ms-cadence loop. Lazily created by healthcheckClient
	// under mu; nil = first probe. 2s per-probe timeout bounds
	// the probe within the readyTimeout deadline.
	hcClient *http.Client
	recs     map[string]*instanceRecord // instance -> per-process bookkeeping (M6 builder VMs)
	// rings is the per-instance log ring buffer (issue #254 / Move 4).
	// Created in Boot/Restore before startJailer so cmd.Stdout can be wired
	// to the ring's writer; closed in Kill/DestroyWithExport so the byte
	// budget is released when the instance is gone (invariant §6.2-4).
	rings map[string]*logbuf.Ring
	// slowSubscriber (issue #309 / tier-2 DX) is the per-VMM
	// callback installed at every ring that registerRing
	// creates. Nil = no callback wired; registerRing skips the
	// SetSlowSubscriberCallback call entirely. Production
	// wires it from cmd/vmmd/main.go to a closure that calls
	// (*wire.OpsMetrics).IncLogDropped("slow_subscriber") via
	// WithSlowSubscriberCallback. The field is read under
	// v.mu inside registerRing so a swap is safe at runtime.
	slowSubscriber func()
	// evictedLine receives lines that cross the per-instance ring byte
	// budget. Production supplies a bounded async archive sink; the callback
	// must not perform disk or network I/O while the ring mutex is held.
	evictedLine func(instance string, line logbuf.Line)
	// processExitSink receives the fact that a tracked Firecracker
	// process has exited. Manager owns the lifecycle decision; the
	// VMM only reports the reaped child. Kept outside the VMM
	// interface so injected VMMs remain source-compatible.
	processExitSink func(instance string, exitCode int)
	// materialisedTmp tracks tmp files materializeFromStorage created for
	// each instance so Kill/DestroyWithExport can Remove them on teardown.
	// Without this, the tmp files (in /tmp) outlive the chroot and leak
	// across thousands of wakes on a busy box.
	materialisedTmp map[string][]string
	// bindMounts tracks image bind mounts used when a source and the jail
	// chroot are on different filesystems (the production jail is tmpfs).
	// The source mode is restored after the VM exits.
	bindMounts map[string][]ephemeralBind
	// bindSourceModes reference-counts temporary source permission widening
	// when multiple VMs bind the same shared base image concurrently.
	bindSourceModes map[string]bindSourceMode
	// events is the wake-timeline fan-out (issue #517 / PR-C /
	// ADR-064). vmmd is the corroborating-observation source for
	// wake.boot_started (mirror) and the canonical emit site for
	// wake.readiness_200 (the first 2xx probe). nil opts out
	// (pre-PR-C test fixtures).
	events *events.Platform
	// readinessStartedAt is the waitReady loop's start timestamp;
	// captured before the probe loop so the readiness_200 payload
	// can carry the elapsed_ms field. Reset on every waitReady
	// call. lazy — populated by waitReady, not at construction.
	readinessStartedAt time.Time
}

type ephemeralBind struct {
	source     string
	mountpoint string
	mode       os.FileMode
}

type bindSourceMode struct {
	mode os.FileMode
	refs int
}

// restoreTimingBreakdown is the vmmd-side breakdown of one successful
// snapshot restore. The timestamps are converted to integer milliseconds
// only when the wake-timeline event is emitted; keeping the struct in
// durations avoids making the restore path depend on the event wire shape.
type restoreTimingBreakdown struct {
	ChrootMs             int64
	MaterializeMemMs     int64
	MaterializeVMStateMs int64
	ResolveImagesMs      int64
	StageDrivesMs        int64
	StageSnapshotMs      int64
	HelperMs             int64
	StartJailerMs        int64
	BindTunMs            int64
	LoadSnapshotMs       int64
	ResumeHookMs         int64
	WaitReadyMs          int64
	TotalMs              int64
}

// instanceRecord tracks one firecracker child + build-specific options so
// DestroyWithExport can wait for exit, capture the code, and copy artifacts.
// The exited/exitCode fields are written exactly once by the watchdog goroutine
// started in startJailer; reads in DestroyWithExport block until the watchdog
// signals done via the cond.
type instanceRecord struct {
	cmd         *exec.Cmd
	consolePath string        // serial console file used to detect a guest halt
	isBuilder   bool          // builderd owns the expected process exit/export path
	exited      bool          // set by the watchdog when cmd.Wait completes
	exitCode    int           // captured from cmd.Wait's ProcessState.ExitCode()
	done        chan struct{} // closed by the watchdog; readers <-done to wake
}

// ringWriter is a thin io.Writer that forwards every Write call to the
// per-instance logbuf.Ring as a Line tagged with the configured stream
// ("stdout" or "stderr"). The ring buffers partial lines until '\n' and
// assigns the monotonic Seq, so this adapter is allocation-free per Write
// beyond a 1-field closure copy in the io.Writer interface.
type ringWriter struct {
	ring   *logbuf.Ring
	stream string
}

func (w *ringWriter) Write(p []byte) (int, error) {
	return w.ring.Write(w.stream, p)
}

// ringFor returns the per-instance ring registered for instance, or nil
// when the instance never had one (legacy Boot callers, test seams).
// Caller may be holding v.mu; the function takes and releases it.
func (v *JailerVMM) ringFor(instance string) *logbuf.Ring {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.rings[instance]
}

// LogRing returns the per-instance log ring, or nil if instance is not
// alive on this vmmd. Used by vmmdgrpc.Server.Logs to fan frames out to
// subscribers. nil-safe so the gRPC handler can treat "no ring" as a
// NotFound without a separate liveness check.
func (v *JailerVMM) LogRing(instance string) *logbuf.Ring {
	return v.ringFor(instance)
}

// registerRing creates a fresh ring for the instance, replacing any prior
// ring (which is closed and discarded). Called from Boot/Restore before
// startJailer so cmd.Stdout can be wired to the writer.
//
// Returns the ring pointer for callers that want to pre-publish a line
// (e.g. M6 builder VMs writing the build-done marker).
func (v *JailerVMM) registerRing(instance string) *logbuf.Ring {
	v.mu.Lock()
	defer v.mu.Unlock()
	if old, ok := v.rings[instance]; ok {
		_ = old.Close()
	}
	r := logbuf.New(0) // 0 -> logbuf.DefaultMaxBytes (10 MiB)
	// Install the per-VMM slow-subscriber callback (issue
	// #309 / tier-2 DX). The callback fires once per Write
	// that hit at least one full subscriber channel — the
	// counter semantics that map to the
	// apid_logs_dropped_total{reason="slow_subscriber"}
	// increment the dashboard queries. nil-safe: tests that
	// construct a JailerVMM without WithSlowSubscriberCallback
	// see the original behaviour (no callback wired, no
	// metric increment).
	if v.slowSubscriber != nil {
		r.SetSlowSubscriberCallback(v.slowSubscriber)
	}
	if v.evictedLine != nil {
		evictedLine := v.evictedLine
		r.SetEvictCallback(func(line logbuf.Line) {
			evictedLine(instance, line)
		})
	}
	v.rings[instance] = r
	return r
}

// WithSlowSubscriberCallback installs (or replaces, when cb is
// non-nil) the per-ring slow-subscriber callback every future
// registerRing call will wire (issue #309 / tier-2 DX). The
// callback fires once per ring Write whose Publish loop hit a
// full subscriber channel. Returns the receiver so the
// constructor call site reads as a fluent chain, matching the
// WithStorage precedent.
//
// The callback is captured by closure at install time; the
// typical wiring is:
//
//	jailer := fcvm.NewJailerVMM(...).WithStorage(...).
//	    WithSlowSubscriberCallback(func() {
//	        ops.IncLogDropped("slow_subscriber")
//	    })
//
// Passing nil uninstalls (subsequent rings get no callback) —
// useful for tests that swap the metric sink.
func (v *JailerVMM) WithSlowSubscriberCallback(cb func()) *JailerVMM {
	v.mu.Lock()
	v.slowSubscriber = cb
	v.mu.Unlock()
	return v
}

// WithLogEvictionCallback installs the per-line callback every future ring
// invokes when a line leaves the in-memory byte budget. The callback runs
// under the ring mutex, so callers should enqueue to a bounded async sink.
func (v *JailerVMM) WithLogEvictionCallback(cb func(instance string, line logbuf.Line)) *JailerVMM {
	v.mu.Lock()
	v.evictedLine = cb
	v.mu.Unlock()
	return v
}

// WithProcessExitSink installs the callback for an unexpected Firecracker
// process exit. Passing nil disables the callback.
func (v *JailerVMM) WithProcessExitSink(cb func(instance string, exitCode int)) *JailerVMM {
	v.mu.Lock()
	v.processExitSink = cb
	v.mu.Unlock()
	return v
}

// unregisterRing closes the ring and removes it from the map. Idempotent
// — Kill/DestroyWithExport always call this so a park/unpark cycle frees
// the byte budget (invariant §6.2-4: parked app = zero RAM).
func (v *JailerVMM) unregisterRing(instance string) {
	v.mu.Lock()
	r, ok := v.rings[instance]
	if ok {
		delete(v.rings, instance)
	}
	v.mu.Unlock()
	if ok {
		_ = r.Close()
	}
}

// NewJailerVMM constructs a JailerVMM. readyTimeout of 0 defaults to 30s (the
// COLD_BOOTING ceiling, spec §6.1).
func NewJailerVMM(chrootBase string, readyTimeout time.Duration) *JailerVMM {
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}
	return &JailerVMM{
		chrootBase:      chrootBase,
		fcName:          resolveFCChrootName(),
		readyTimeout:    readyTimeout,
		destroyWait:     10 * time.Minute, // builder timeout (spec §1 BuildTimeoutSeconds) + headroom
		exportMaxBytes:  0,                // resolved to api.MaxExportedLayerBytes at first export
		proc:            make(map[string]*exec.Cmd),
		clients:         make(map[string]*http.Client),
		recs:            make(map[string]*instanceRecord),
		rings:           make(map[string]*logbuf.Ring),
		materialisedTmp: make(map[string][]string),
		bindMounts:      make(map[string][]ephemeralBind),
		bindSourceModes: make(map[string]bindSourceMode),
	}
}

// WithStorage wires the artifact backend the VMM uses for snapshot blob
// (de)serialization. Issue #96 / ADR-025 axis 2 — when Restore carries a
// StorageKey, the VMM streams the bytes through Storage.Get into a tmp
// file and uses the absolute path for the chroot staging step. On
// Snapshot, the produced mem blob is Put under the configured StorageKey
// after the move-out. Calling WithStorage(nil) clears the override and
// restores the legacy MemPath-only contract (one-release deprecation
// window).
func (v *JailerVMM) WithStorage(s storage.StorageBackend) *JailerVMM {
	v.storage = s
	return v
}

// WithEvents stamps the wake-timeline fan-out (issue #517 / PR-C /
// ADR-064) on the VMM. vmmd is the corroborating-observation source
// for wake.boot_started (mirror at the gRPC server) and the
// canonical emit site for wake.readiness_200 (the first 2xx probe).
// Sibling of WithStorage — nil opts out (pre-PR-C fixtures).
func (v *JailerVMM) WithEvents(p *events.Platform) VMM {
	v.events = p
	return v
}

// resolveFCChrootName returns the directory name jailer will use for the chroot:
// jailer resolves the --exec-file symlink and uses the REAL binary's basename, so
// a `firecracker -> firecracker-v1.7.0` symlink (both the ansible role and the
// Lima loop ship one) makes jailer build .../firecracker-v1.7.0/<id>/root. The
// Manager must place the config/drives in that same dir, so it tracks the same
// resolved basename here. Falls back to the plain name off the metal path.
func resolveFCChrootName() string {
	p, err := exec.LookPath(FirecrackerBin)
	if err != nil {
		return FirecrackerBin
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Base(real)
	}
	return filepath.Base(p)
}

// DetectFirecrackerVersion runs `firecracker --version` and returns the version
// string (e.g. "1.7.0"). Snapshots are pinned to this value (ADR-005); on a
// change every snapshot goes stale and apps re-snapshot via cold boot.
func DetectFirecrackerVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, FirecrackerBin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("vmm: firecracker --version: %w", err)
	}
	// First line looks like "Firecracker v1.7.0".
	line := out
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		line = out[:i]
	}
	fields := bytes.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("vmm: unexpected version output %q", out)
	}
	return string(bytes.TrimPrefix(fields[len(fields)-1], []byte("v"))), nil
}

func (v *JailerVMM) chrootRoot(instance string) string {
	return filepath.Join(v.chrootBase, v.fcName, instance, "root")
}

// JailRoot is the version-scoped directory whose immediate children are
// the per-instance chroots — the parent of every chrootRoot. Exported
// for the startup orphan sweep (ReapOrphanedJails), which has to
// enumerate instances that this process never created and therefore
// cannot ask the live map about.
//
// The fcName segment is the jailer's own naming (it derives the
// directory from the exec-file basename), so the path is only correct
// for the Firecracker binary this VMM was resolved against — which is
// exactly the set of chroots a restarted vmmd can still act on.
func (v *JailerVMM) JailRoot() string {
	return filepath.Join(v.chrootBase, v.fcName)
}

// resolveDriveImage finds the writable drive inside Jailer’s chroot. The
// canonical layer name is used when present; builderd and older callers may
// preserve the source basename, so accept a single ext4 fallback as well.
func (v *JailerVMM) resolveDriveImage(instance string) (string, error) {
	root := v.chrootRoot(instance)
	canonical := filepath.Join(root, layerImageName)
	if _, err := os.Stat(canonical); err == nil {
		return canonical, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("resolve drive1: read chroot root: %w", err)
	}
	var found string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ext4") {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("resolve drive1: multiple ext4 images in %s", root)
		}
		found = entry.Name()
	}
	if found == "" {
		return "", fmt.Errorf("resolve drive1: no ext4 image in %s", root)
	}
	return filepath.Join(root, found), nil
}

func (v *JailerVMM) socketPath(instance string) string {
	return filepath.Join(v.chrootRoot(instance), APISockName)
}

// BootColdBoot is the cold-boot leg of Wake (spec §4.4 / cold boot must
// always work, ADR-005). It materializes the kernel/base/layer StorageBackend
// keys through the configured StorageBackend, builds the VMConfig from
// the resolved tmp paths, then delegates to Boot for chroot staging +
// jailer start + readiness wait.
//
// #96 / ADR-025 axis 2 (PR #116): BootColdBoot is the seam where keys
// become host paths the FC daemon can read. Same pattern as the
// mem-blob materialize in Restore (line ~209 above): keys go in,
// tmp paths come out, trackMaterialised handles cleanup so the
// deferred Kill sweeps the tmp files alongside the chroot (which is
// already on tmpfs, per spec §11).
func (v *JailerVMM) BootColdBoot(ctx context.Context, l Lease, spec ColdBootSpec) (err error) {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("vmm: cold boot: %w", err)
	}
	kernelSrc, err := v.restoreSourceFromStorage(ctx, l.Instance, spec.KernelKey)
	if err != nil {
		return fmt.Errorf("vmm: stage kernel: %w", err)
	}
	baseSrc, err := v.restoreSourceFromStorage(ctx, l.Instance, spec.BaseKey)
	if err != nil {
		return fmt.Errorf("vmm: stage base: %w", err)
	}
	// Issue #463 / ADR-069 / PR-B: when Workloads is empty, the
	// legacy single-workload path resolves spec.LayerKey once.
	// When Workloads is non-empty, we resolve each workload's
	// StorageBackend key in turn and overwrite the StorageKey
	// field with the staged tmp path so BuildColdBootConfig's
	// PathOnHost reads point at the chroot-basename tmp files.
	if len(spec.Workloads) == 0 {
		layerSrc, mErr := v.restoreSourceFromStorage(ctx, l.Instance, spec.LayerKey)
		if mErr != nil {
			return fmt.Errorf("vmm: stage layer: %w", mErr)
		}
		spec.LayerKey = layerSrc
	} else {
		for i := range spec.Workloads {
			resolved, mErr := v.restoreSourceFromStorage(ctx, l.Instance, spec.Workloads[i].StorageKey)
			if mErr != nil {
				return fmt.Errorf("vmm: stage workload %d (%s): %w", i, spec.Workloads[i].Name, mErr)
			}
			spec.Workloads[i].StorageKey = resolved
		}
	}
	// Build VMConfig from the resolved paths. Drive paths become the
	// tmp paths so provision (line ~941) stages them as basenames.
	// spec.Tap isn't used in the config — the Veth is plumbed by the
	// caller (Manager) before Boot runs.
	spec.KernelKey = kernelSrc
	spec.BaseKey = baseSrc
	// Issue #460 / ADR-053, ADR-057 / PR-D: per-deployment override
	// readiness probe path. Empty keeps the legacy TCP-accept on :8080
	// (pre-PR-D default). Non-empty → waitReady does HTTP GET
	// <HealthcheckPath> against <HostIP>:8080 and accepts 2xx as ready.
	if spec.SkipReady {
		return v.bootNoWait(ctx, l, BuildColdBootConfig(spec, l.Slot), spec.Workloads, spec.SecretsEnvJSON, spec.APIEnvJSON)
	}
	return v.boot(ctx, l, BuildColdBootConfig(spec, l.Slot), false, spec.HealthcheckPath, spec.StartupDeadlineS, spec.Workloads, spec.SecretsEnvJSON, spec.APIEnvJSON)
}

func (v *JailerVMM) bootNoWait(ctx context.Context, l Lease, cfg VMConfig, workloads []WorkloadSpec, secretsEnvJSON, apiEnvJSON []byte) error {
	return v.boot(ctx, l, cfg, true, "", 0, workloads, secretsEnvJSON, apiEnvJSON)
}

// Boot provisions the chroot, starts the jailed firecracker with a full config,
// and blocks until the guest is ready. On error it kills whatever it started.
//
// Prefer BootColdBoot for cold boots — it materializes the StorageBackend
// keys in ColdBootSpec into host paths first, then delegates here. Boot
// is kept for tests that already have resolved paths in hand.
//
// healthcheckPath (issue #460 / ADR-053, ADR-057 / PR-D) is the
// per-deployment override readiness probe path. Empty keeps the legacy
// TCP-accept on :8080 (pre-PR-D default). Non-empty → waitReady does
// HTTP GET <healthcheckPath> against <HostIP>:8080 and accepts 2xx as
// ready. startupDeadlineS is the plan-resolved per-app readiness budget;
// zero preserves the vmmd default for legacy callers. The BootColdBoot
// wrapper threads both fields from ColdBootSpec; tests that already have
// resolved paths in hand can pass "" to keep the legacy probe.
//
// Move 4 (issue #254): registerRing is called BEFORE startJailer so
// cmd.Stdout can be wired to the ring's writer in startJailer.
func (v *JailerVMM) Boot(ctx context.Context, l Lease, cfg VMConfig, healthcheckPath string) (err error) {
	return v.boot(ctx, l, cfg, false, healthcheckPath, 0, nil, nil, nil)
}

func (v *JailerVMM) boot(ctx context.Context, l Lease, cfg VMConfig, skipReady bool, healthcheckPath string, startupDeadlineS int, workloads []WorkloadSpec, secretsEnvJSON, apiEnvJSON []byte) (err error) {
	root, err := v.mkChroot(l.Instance)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = v.Kill(context.WithoutCancel(ctx), l)
		}
	}()
	_ = v.registerRing(l.Instance)

	jailed, err := v.provision(root, cfg, l.UID, l.GID, l.Instance)
	if err != nil {
		return fmt.Errorf("vmm: provision chroot: %w", err)
	}
	if err := v.stagePreBootFiles(l.Instance, workloads, secretsEnvJSON, apiEnvJSON); err != nil {
		return fmt.Errorf("vmm: stage pre-boot workload state: %w", err)
	}
	cfgBytes, err := json.Marshal(jailed)
	if err != nil {
		return fmt.Errorf("vmm: marshal config: %w", err)
	}
	cfgPath := filepath.Join(root, VMConfigName)
	if err = prepareConfigFIFO(cfgPath, l.UID, l.GID); err != nil {
		return fmt.Errorf("vmm: prepare config: %w", err)
	}
	if err = v.ownChrootRoot(root, l); err != nil {
		return err
	}
	if err = v.stageMountHelper(root); err != nil {
		return err
	}
	if err = v.bindTunSource(root, l.Instance); err != nil {
		return err
	}
	if err = v.startJailer(ctx, l, "--config-file", VMConfigName); err != nil {
		return err
	}
	if err = v.bindTunDeviceInJailer(root, l.Instance, l.UID, l.GID); err != nil {
		return err
	}
	if l.IsBuilder || l.Plan.Valid() {
		if err = v.applyPreBootCgroupFence(l, workloads); err != nil {
			return fmt.Errorf("vmm: apply pre-boot cgroup fence: %w", err)
		}
	}
	if err = writeConfigFIFO(ctx, cfgPath, cfgBytes); err != nil {
		return fmt.Errorf("vmm: write config: %w", err)
	}
	// Vsock is configured via the config-file (top-level `vsock:` field,
	// see VMConfig). Firecracker attaches it pre-start; the UDS at
	// vsockUDSSock is created by the time startJailer returns. No
	// post-start PUT needed.
	if !skipReady {
		if err = v.waitReady(ctx, l, healthcheckPath, startupDeadlineS); err != nil {
			return fmt.Errorf("vmm: readiness: %w", err)
		}
	}
	return nil
}

// preparesWakeStateBeforeBoot is an internal marker used by Manager.Wake.
// JailerVMM stages all runtime files while the Firecracker config FIFO is
// still closed, so Manager must not repeat the writes after readiness. Test
// VMMs do not implement this marker and retain the older observable staging
// hooks.
func (v *JailerVMM) preparesWakeStateBeforeBoot() bool { return true }

// stagePreBootFiles writes every file guest-init needs before the VM can run.
// The main drive is available after provision; the Firecracker process has not
// received its config yet, so this is the last safe point for secrets, API env,
// per-sidecar env overrides, and the sidecar roster.
func (v *JailerVMM) stagePreBootFiles(instance string, workloads []WorkloadSpec, secretsEnvJSON, apiEnvJSON []byte) error {
	if len(secretsEnvJSON) > 0 {
		if err := v.StageSecretsEnv(instance, secretsEnvJSON); err != nil {
			return fmt.Errorf("stage secrets.env: %w", err)
		}
	}
	if len(apiEnvJSON) > 0 {
		if err := v.StageAPIEnv(instance, apiEnvJSON); err != nil {
			return fmt.Errorf("stage env.json: %w", err)
		}
	}
	if len(workloads) > 1 {
		// Sidecar drives are deliberately read-only. Image defaults and the
		// effective command are baked into their immutable manifests, while
		// deployment-specific env overrides are written to the instance's
		// writable main upper before guest-init can execute them.
		for _, workload := range workloads[1:] {
			if len(workload.preparedEnvJSON) == 0 {
				continue
			}
			if err := v.StageWorkloadEnv(instance, workload.Name, workload.preparedEnvJSON); err != nil {
				return fmt.Errorf("stage workload %s env: %w", workload.Name, err)
			}
		}
		if err := v.StageWorkloadManifest(instance, -1, workloads[0]); err != nil {
			return fmt.Errorf("stage main workload manifest: %w", err)
		}
		if err := v.StageWorkloadRoster(instance, workloads[0], workloads[1:]); err != nil {
			return fmt.Errorf("stage workload roster: %w", err)
		}
	}
	return nil
}

// applyPreBootCgroupFence installs the host-side memory/CPU fence after
// jailer has created the instance scope but before the config FIFO releases
// Firecracker. A missing or unwritable fence is fatal: allowing an uncapped
// VM to continue would turn a provisioning error into a resource-isolation
// bypass.
func (v *JailerVMM) applyPreBootCgroupFence(l Lease, workloads []WorkloadSpec) error {
	if l.IsBuilder {
		if err := writeBuildCgroup(l.Instance, l.MemoryMaxMiB); err != nil {
			return err
		}
		return nil
	}
	if err := writeAppCgroup(l.Instance, l.Plan, l.MemoryMaxMiB, l.CPUMillicores); err != nil {
		return err
	}
	if len(workloads) <= 1 {
		return nil
	}
	parentScope := filepath.Join(cgroupRoot, ParentCgroupFor(l.Plan), PerInstanceScope(l.Instance))
	if err := writeWorkloadCgroup(parentScope, WorkloadNameMain, l.MemoryMaxMiB, l.CPUMillicores); err != nil {
		return err
	}
	for _, workload := range workloads[1:] {
		// Zero values inherit the parent limits; no child leaf is
		// needed when neither resource has an override.
		if workload.RamMB == 0 && workload.CPUMillicores == 0 {
			continue
		}
		if err := writeWorkloadCgroup(parentScope, workload.Name, workload.RamMB, workload.CPUMillicores); err != nil {
			return err
		}
	}
	return nil
}

// Restore starts a bare jailed firecracker and loads a snapshot into it, resuming
// the guest (spec §4.4, mem_backend File). The netns/tap already exist (the
// Manager set them up); the restored net device references tap0 by name.
//
// spec.HealthcheckPath (issue #460 / ADR-053, ADR-057 / PR-D) is the
// per-deployment override readiness probe path. Empty keeps the legacy
// TCP-accept on :8080 (pre-PR-D default). Non-empty → waitReady does
// HTTP GET <path> against <HostIP>:8080 and accepts 2xx as ready. The
// Manager threads WakeRequest.HealthcheckPath into this field at bringUp.
func (v *JailerVMM) Restore(ctx context.Context, l Lease, spec RestoreSpec) (err error) {
	// Start the breakdown before any chroot or storage work. The manager's
	// RestoreMs already covers this full method; keeping the detailed log on
	// the same boundary makes its total_ms comparable to that field and keeps
	// remote memory/vmstate materialisation visible in the breakdown.
	t0 := time.Now()
	root, err := v.mkChroot(l.Instance)
	if err != nil {
		return err
	}
	chrootReady := time.Now()
	defer func() {
		if err != nil {
			_ = v.Kill(context.WithoutCancel(ctx), l)
		}
	}()

	tMemStateResolveStart := time.Now()
	// #96 / ADR-025 axis 2 — materialise the mem blob from the configured
	// StorageBackend into a vmmd-allocated tmp file. After slice 3 the
	// staging tmp path is purely internal: no caller-supplied MemPath is
	// honoured. The local driver on the production box maps "snap/" to
	// /srv/fc/snap and the resolution is essentially a stat; the OCI
	// driver streams the bytes over HTTP. Tmp cleanup happens via the
	// deferred Kill (chroot lives on tmpfs and disappears with it).
	memSrc, err := v.restoreMemSource(ctx, l.Instance, spec)
	if err != nil {
		return err
	}
	if memSrc == "" {
		return fmt.Errorf("vmm: restore spec missing mem source (storage_key=%q)", spec.StorageKey)
	}
	memReady := time.Now()

	// #121 / ADR-025 axis 2 slice 4 — materialise the vmstate blob
	// independently of mem. The branch selector is the new VMStateStorageKey
	// field's emptiness, NOT v.storage != nil: production default-local
	// wires a non-nil LocalStorageBackend (cmd/vmmd/main.go) so a
	// storage-nil gate would route vmstate through the local backend
	// even on default-local and break the host-path branch the engine
	// relies on for single-box wakes (the documented "every wake pays
	// the dial cost" model only ships vmstate via storage on remote
	// nodes). When the key is non-empty the configured backend is the
	// authoritative carrier and spec.VMStatePath is logged-only metadata.
	// When the key is empty we fall back to spec.VMStatePath byte-for-bit
	// (the existing single-box behaviour).
	vmstateStart := time.Now()
	stateSrc := spec.VMStatePath
	if spec.VMStateStorageKey != "" && v.storage != nil {
		stateTmp, gerr := v.restoreSourceFromStorage(ctx, l.Instance, spec.VMStateStorageKey)
		if gerr != nil {
			return gerr
		}
		if stateTmp != "" {
			stateSrc = stateTmp
		}
		// Defensive: a nil-error, empty-result from materializeFromStorage
		// means the backend didn't surface a file for this key (e.g. the
		// materialise helper's "no entry" return code). Falling through to
		// spec.VMStatePath below keeps Restore advancing when a legacy
		// host-path file is still around; the next branch's empty-stateSrc
		// check is the hard error when neither locator has bytes.
	}
	if stateSrc == "" {
		return fmt.Errorf("vmm: restore spec missing vmstate source (vmstate_storage_key=%q vmstate_path=%q)",
			spec.VMStateStorageKey, spec.VMStatePath)
	}
	vmstateReady := time.Now()
	tMemStateResolve := time.Now()

	// Re-stage everything the snapshot's recorded VM state still references.
	// Park→Kill (vmm.Kill) wiped the prior chroot, so the chroot-relative
	// basenames in the snapshot (kernel + drive backings) must be restored
	// before /snapshot/load, otherwise Firecracker 400s when it tries to
	// open the backing file. Drive 0 (base) is shared RO — hardlink; drive 1
	// (per-app layer, RW overlay upper) is per-instance — copy + chown.
	//
	// #96 / ADR-025 axis 2 (PR #116): kernel/base/layer keys are
	// canonical StorageBackend keys; vmmd resolves them through the
	// configured backend into vmmd-allocated tmp paths via
	// materializeFromStorage (the same seam that handles the mem blob
	// above). For the local backend the tmp path is the same file the
	// legacy host-path helper returned (wasted I/O but functionally
	// identical); for the OCI backend it's the streamed bytes. Tmp
	// cleanup reuses trackMaterialised — already wired for the mem
	// blob — so the Kill deferred above sweeps all three.
	if spec.KernelKey == "" || spec.BaseKey == "" {
		return fmt.Errorf("vmm: restore spec missing kernel/base: %+v", spec)
	}
	if len(spec.Workloads) == 0 && spec.LayerKey == "" {
		return fmt.Errorf("vmm: restore spec missing layer: %+v", spec)
	}
	kernelSrc, err := v.restoreSourceFromStorage(ctx, l.Instance, spec.KernelKey)
	if err != nil {
		return fmt.Errorf("vmm: stage kernel: %w", err)
	}
	baseSrc, err := v.restoreSourceFromStorage(ctx, l.Instance, spec.BaseKey)
	if err != nil {
		return fmt.Errorf("vmm: stage base: %w", err)
	}
	// Issue #463 / ADR-069 / PR-B: when Workloads is empty, the
	// legacy single-workload path resolves spec.LayerKey once and
	// stages it as the rw drive1. When Workloads is non-empty, we
	// resolve each workload's StorageBackend key in turn and stage
	// the resolved path as either rw (main, idx==0) or ro (sidecars).
	var layerSrc string
	resolvedWorkloads := make([]string, 0, len(spec.Workloads))
	if len(spec.Workloads) == 0 {
		layerSrc, err = v.restoreSourceFromStorage(ctx, l.Instance, spec.LayerKey)
		if err != nil {
			return fmt.Errorf("vmm: stage layer: %w", err)
		}
	} else {
		for i, w := range spec.Workloads {
			resolved, mErr := v.restoreSourceFromStorage(ctx, l.Instance, w.StorageKey)
			if mErr != nil {
				return fmt.Errorf("vmm: stage workload %d (%s): %w", i, w.Name, mErr)
			}
			resolvedWorkloads = append(resolvedWorkloads, resolved)
			if i == 0 {
				// Main workload — must be rw so the customer's
				// container can write to /tmp etc. Sidecars go
				// ro below.
				layerSrc = resolved
			}
		}
	}
	tResolve := time.Now()
	if _, err := v.stageReadOnlyAs(root, kernelSrc, stableReadOnlyName(kernelSrc, kernelImageName), l.Instance); err != nil {
		return fmt.Errorf("vmm: stage kernel: %w", err)
	}
	if _, err := v.stageReadOnlyAs(root, baseSrc, stableReadOnlyName(baseSrc, baseImageName), l.Instance); err != nil {
		return fmt.Errorf("vmm: stage base: %w", err)
	}
	// layerSrc is empty when cold-booting without a layer (cold path, no snapshot yet).
	// stageWritable with an empty src would fail; skip it — Boot handles a missing drive1.
	tStageWritableStart := time.Now()
	if layerSrc != "" {
		if _, err := v.stageWritable(root, layerSrc, l.UID, l.GID, l.Instance); err != nil {
			return fmt.Errorf("vmm: stage layer: %w", err)
		}
	}
	tStageWritable := time.Now()
	// PR-B: stage each sidecar drive as read-only (the upper/writable
	// overlay is shared with main; sidecars never get their own rw
	// path, which is the load-bearing invariant — a runaway sidecar
	// cannot escape quota accounting by writing past its read-only
	// boundary).
	for i := 1; i < len(resolvedWorkloads); i++ {
		if _, err := v.stageReadOnlyFor(root, resolvedWorkloads[i], l.Instance); err != nil {
			return fmt.Errorf("vmm: stage sidecar %d: %w", i-1, err)
		}
	}
	tStageDrives := time.Now()
	if err := v.stagePreBootFiles(l.Instance, spec.Workloads, spec.SecretsEnvJSON, spec.APIEnvJSON); err != nil {
		return fmt.Errorf("vmm: stage pre-boot workload state: %w", err)
	}

	// Snapshot files are read-only inputs shared across the N instances a single
	// snapshot may restore (invariant §6.2-5): hardlink them in and widen for read
	// rather than chown, which would rewrite the shared inode owner.
	memName, err := v.stageReadOnlyAs(root, memSrc, memSnapshotName, l.Instance)
	if err != nil {
		return fmt.Errorf("vmm: stage mem file: %w", err)
	}
	stateName, err := v.stageReadOnlyAs(root, stateSrc, vmstateSnapshotName, l.Instance)
	if err != nil {
		return fmt.Errorf("vmm: stage vmstate: %w", err)
	}
	tMemState := time.Now()
	// firecracker (as the jailer uid) writes the API socket and, later, snapshot
	// output into the chroot root — it must own that directory.
	if err = v.ownChrootRoot(root, l); err != nil {
		return err
	}
	if err = v.stageMountHelper(root); err != nil {
		return err
	}
	if err = v.bindTunSource(root, l.Instance); err != nil {
		return err
	}
	tHelper := time.Now()

	// Start firecracker with only the API socket, then load + resume.
	// Move 4 (issue #254): register the per-instance ring BEFORE
	// startJailer so cmd.Stdout captures every byte the resumed FC
	// writes, including the boot echo and the resume hook's ack.
	_ = v.registerRing(l.Instance)
	if err = v.startJailer(ctx, l); err != nil {
		return err
	}
	tStartJailer := time.Now()
	if err = v.bindTunDeviceInJailer(root, l.Instance, l.UID, l.GID); err != nil {
		return err
	}
	if l.IsBuilder || l.Plan.Valid() {
		if err = v.applyPreBootCgroupFence(l, spec.Workloads); err != nil {
			return fmt.Errorf("vmm: apply pre-boot cgroup fence: %w", err)
		}
	}
	tBindTun := time.Now()
	body := map[string]any{
		"snapshot_path": stateName,
		"mem_backend":   map[string]any{"backend_type": "File", "backend_path": memName},
		"resume_vm":     true,
	}
	if err = v.apiPut(ctx, l.Instance, "/snapshot/load", body); err != nil {
		return fmt.Errorf("vmm: load snapshot: %w", err)
	}
	tLoad := time.Now()
	// Vsock is in the config-file (set at config-write time before
	// startJailer), so the UDS is live by the time /snapshot/load
	// completes. Trigger the resume hook now to re-seed entropy and step
	// the clock before the app can bind :8080 (spec §11 V6).
	if err = v.TriggerResumeHook(ctx, l, time.Now().UnixNano()); err != nil {
		return fmt.Errorf("vmm: resume hook: %w", err)
	}
	tResume := time.Now()
	if err = v.waitReady(ctx, l, spec.HealthcheckPath, spec.StartupDeadlineS); err != nil {
		return fmt.Errorf("vmm: readiness after restore: %w", err)
	}
	tReady := time.Now()
	breakdown := restoreTimingBreakdown{
		ChrootMs:             chrootReady.Sub(t0).Milliseconds(),
		MaterializeMemMs:     memReady.Sub(chrootReady).Milliseconds(),
		MaterializeVMStateMs: vmstateReady.Sub(vmstateStart).Milliseconds(),
		ResolveImagesMs:      tResolve.Sub(vmstateReady).Milliseconds(),
		StageDrivesMs:        tStageDrives.Sub(tResolve).Milliseconds(),
		StageSnapshotMs:      tMemState.Sub(tStageDrives).Milliseconds(),
		HelperMs:             tHelper.Sub(tMemState).Milliseconds(),
		StartJailerMs:        tStartJailer.Sub(tHelper).Milliseconds(),
		BindTunMs:            tBindTun.Sub(tStartJailer).Milliseconds(),
		LoadSnapshotMs:       tLoad.Sub(tBindTun).Milliseconds(),
		ResumeHookMs:         tResume.Sub(tLoad).Milliseconds(),
		WaitReadyMs:          tReady.Sub(tResume).Milliseconds(),
		TotalMs:              tReady.Sub(t0).Milliseconds(),
	}
	v.emitRestoreBreakdown(ctx, l, tReady, breakdown)
	slog.Default().Info("restore timing breakdown",
		"instance", l.Instance,
		"chroot_ms", breakdown.ChrootMs,
		"materialize_mem_ms", breakdown.MaterializeMemMs,
		"materialize_vmstate_ms", breakdown.MaterializeVMStateMs,
		"resolve_images_ms", breakdown.ResolveImagesMs,
		"mem_state_resolve_ms", tMemStateResolve.Sub(tMemStateResolveStart).Milliseconds(),
		"stage_drives_ms", breakdown.StageDrivesMs,
		"stage_writable_ms", tStageWritable.Sub(tStageWritableStart).Milliseconds(),
		"stage_snapshot_ms", breakdown.StageSnapshotMs,
		"helper_ms", breakdown.HelperMs,
		"start_jailer_ms", breakdown.StartJailerMs,
		"bind_tun_ms", breakdown.BindTunMs,
		"load_snapshot_ms", breakdown.LoadSnapshotMs,
		"resume_hook_ms", breakdown.ResumeHookMs,
		"wait_ready_ms", breakdown.WaitReadyMs,
		"total_ms", breakdown.TotalMs,
	)
	return nil
}

// restoreMemSource resolves the memory blob from the canonical storage key
// whenever one is present. The legacy VMStatePath is only a fallback for
// callers that predate StorageKey. This must not branch on
// FAAS_STORAGE_BACKEND: OCI mode still needs the memory blob materialized
// from StorageBackend before Firecracker can load the snapshot.
func (v *JailerVMM) restoreMemSource(ctx context.Context, instanceID string, spec RestoreSpec) (string, error) {
	memSrc := spec.VMStatePath
	if spec.StorageKey == "" || v.storage == nil {
		return memSrc, nil
	}
	memTmp, err := v.restoreSourceFromStorage(ctx, instanceID, spec.StorageKey)
	if err != nil {
		return "", err
	}
	if memTmp != "" {
		memSrc = memTmp
	}
	return memSrc, nil
}

// vsockUDSSock is the host-side path the TriggerResumeHook dialer reaches.
// It's the chroot-local UDS the jailer creates; vmmd dials it from the
// chroot root because the firecracker process is unprivileged and only its
// jailer uid can read the socket file.
func (v *JailerVMM) vsockUDSSock(instance string) string {
	return filepath.Join(v.chrootRoot(instance), VsockUDSSocketName)
}

// VsockUDSSocketPath returns the host-side Firecracker vsock proxy path for
// an instance. Firecracker exposes guest AF_VSOCK listeners through this Unix
// socket; callers must use the CONNECT <guest-port> handshake rather than
// dialing the guest CID directly. The method is intentionally read-only and
// exposes no mutable VMM state so vmmd's liveness monitor can share the same
// path convention as the resume and characterization protocols.
func (v *JailerVMM) VsockUDSSocketPath(instance string) string {
	if v == nil {
		return ""
	}
	return v.vsockUDSSock(instance)
}

// resumeHookDialDeadline bounds the TriggerResumeHook wait. The jailer
// creates the vsock UDS a few ms after firecracker accepts the /vsock PUT; on
// a slow nested-KVM guest this can take ~50 ms. Five seconds is well above
// the realistic ceiling and well below the spec §6.1 readyTimeout (30 s).
const resumeHookDialDeadline = 5 * time.Second

// resumeHookDialStep is the per-attempt backoff between dial retries.
const resumeHookDialStep = 20 * time.Millisecond

// resumeHookMsgResume is the wire-format discriminator for a resume request.
// Wire: 4-byte BE msg type + 4-byte BE body length + JSON body (ADR-022).
// The length prefix lets the guest read exactly N bytes instead of waiting
// for EOF — some AF_VSOCK proxies don't propagate CloseWrite promptly, so
// depending on it produced EOF-mid-ack in the V6 metal test.
const resumeHookMsgResume uint32 = 1

// resumeHookGuestPort is the AF_VSOCK port the guest-init resume
// listener binds. Must match guest/init/listen_resume_linux.go's
// VsockResumePort.
const resumeHookGuestPort = 1024

// resumeHookEntropyBytes is the count of host-supplied CSPRNG bytes sent
// in every resume payload. The guest injects them into /dev/urandom via
// ioctl(RNDADDENTROPY) BEFORE reading /proc/sys/kernel/random/uuid, so
// each restore's draw is unique even when virtio-rng state is identical
// (it is snapshotted, so /dev/hwrng returns the same bytes per restore).
// 256 bits of entropy is enough to fully re-key the pool for UUID
// generation. ADR-022 §"Why the host ships entropy".
const resumeHookEntropyBytes = 256

// resumeHookMaxBodyBytes is the upper bound on the JSON-marshaled body of
// the resume-hook payload. The body is constructed from exactly
// resumeHookEntropyBytes of CSPRNG output (base64 → 4/3 expansion) plus
// the JSON envelope; 8 KiB is comfortably above the current ~400 B
// observed size and well under int32/2, so a future bump to
// resumeHookEntropyBytes can never push 8+len(body) into overflow
// territory. The guest's VsockResumeMaxEntropyBytes is the matching cap
// on the receiving side. CodeQL go/allocation-size-overflow guards.
const resumeHookMaxBodyBytes = 8 * 1024

// readConnectAck consumes the "OK <hostside_port>\n" reply from
// Firecracker. Returns the first whitespace-delimited token. Reads
// until newline so the byte count doesn't matter (FC's host-assigned
// port is a 32-bit integer — variable digit count).
func readConnectAck(conn net.Conn) (string, error) {
	const max = 64
	buf := make([]byte, 0, max)
	one := make([]byte, 1)
	for len(buf) < max {
		if _, err := conn.Read(one); err != nil {
			return "", fmt.Errorf("read CONNECT reply: %w", err)
		}
		if one[0] == '\n' || one[0] == '\r' {
			break
		}
		buf = append(buf, one[0])
	}
	if len(buf) == 0 {
		return "", fmt.Errorf("empty CONNECT reply")
	}
	// Return the first whitespace-delimited token.
	for i := 0; i < len(buf); i++ {
		if buf[i] == ' ' {
			return string(buf[:i]), nil
		}
	}
	return string(buf), nil
}

// TriggerResumeHook dials the guest's vsock UDS and asks it to run its
// post-restore side effects (re-seed entropy + step clock). Must be called
// from Restore after /snapshot/load and before waitReady. Spec §11 V6 is the
// acceptance gate: two instances from one snapshot must produce distinct
// /proc/sys/kernel/random/uuid immediately post-resume.
//
// Wire format (Firecracker vsock host-initiated, FC docs/vsock.md):
//
//  1. Host connects to <chroot>/vsock.sock.
//  2. Host writes ASCII "CONNECT <port>\n" (e.g. "CONNECT 1024\n").
//  3. Firecracker replies with "OK <assigned_hostside_port>\n".
//  4. Bidirectional byte stream — host writes the resume-hook payload,
//     guest writes back a 1-byte ack.
//
// Payload format (ADR-022): 4-byte big-endian msg type (= 1 =
// MSG_RESUME) + JSON body {"hostTimeUnixNano": N, "entropy": "<base64>"}
// where entropy is 256 bytes of fresh CSPRNG output from the host.
// The guest's listenResumeHook (guest/init/listen_resume_linux.go) reads
// the same shape and writes back ack=0 (ok) or ack=1 (nack).
//
// We fail closed: any error (dial timeout, CONNECT failure, payload
// write failure, nack) returns wrapped. A restored VM with snapshot-
// shared entropy is exactly the failure mode V6 rejects, so we refuse
// to declare it ready.
func (v *JailerVMM) TriggerResumeHook(ctx context.Context, l Lease, hostTimeUnixNano int64) error {
	// Defense-in-depth: refuse to dial with a half-built VMM or empty instance.
	// Without this guard, a refactor that passes an uninitialised JailerVMM
	// (test seam, future caller) would dial a malformed UDS path and return a
	// cryptic ENOENT — fails closed, but the operator gets no clue. With the
	// guard, the failure mode is a clear "this VM was never set up right".
	if v == nil {
		return fmt.Errorf("vmm: TriggerResumeHook: nil receiver")
	}
	if l.Instance == "" {
		return fmt.Errorf("vmm: TriggerResumeHook: empty instance")
	}
	if v.chrootBase == "" {
		return fmt.Errorf("vmm: TriggerResumeHook: chrootBase not configured")
	}
	// Issue #555 PR-4: extract W3C traceparent from the boot context so
	// the guest's resume hook can stamp TRACEPARENT onto the runner env.
	// When the boot context has no span (e.g. legacy single-box without
	// OTel config), the empty string is shipped and the guest no-ops.
	traceparent := traceparentFromContext(ctx)
	started := time.Now()
	attempts := 0
	sock := v.vsockUDSSock(l.Instance)
	deadline := time.Now().Add(resumeHookDialDeadline)
	var conn net.Conn
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var c net.Conn
		var err error
		attempts++
		c, err = net.DialTimeout("unix", sock, 20*time.Millisecond)
		if err == nil {
			_ = c.SetDeadline(time.Now().Add(500 * time.Millisecond))
			// Step 1: FC CONNECT-port handshake. "CONNECT <port>\n" — ASCII,
			// newline-terminated. Guest listens on port VsockResumePort (1024).
			connectCmd := fmt.Sprintf("CONNECT %d\n", resumeHookGuestPort)
			if _, err = c.Write([]byte(connectCmd)); err == nil {
				// Step 2: read "OK <hostside_port>\n". FC prefixes the host-assigned
				// ephemeral port with "OK ". We don't care about the value (it's
				// for connection-multiplexing bookkeeping on the FC side), only
				// that the response starts with "OK ".
				var connectAck string
				connectAck, err = readConnectAck(c)
				if err == nil && connectAck == "OK" {
					conn = c
					break
				}
			}
			_ = c.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if conn == nil {
		return fmt.Errorf("vmm: dial vsock uds %s: %w", sock, lastErr)
	}
	connected := time.Now()
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(resumeHookDialDeadline))

	// Step 3: write the resume-hook payload. 4-byte BE msg type + 4-byte BE
	// body length + JSON body. The length prefix lets the guest read exactly
	// N bytes; we deliberately do NOT close our write half — the connection
	// stays open for the ack roundtrip (some AF_VSOCK proxies don't propagate
	// CloseWrite promptly, so depending on it produced EOF-mid-ack in the
	// V6 metal test).
	//
	// The body carries fresh entropy bytes from the host's CSPRNG. The guest
	// injects them into /dev/urandom via ioctl(RNDADDENTROPY) BEFORE reading
	// /proc/sys/kernel/random/uuid. Without this, both restores from one
	// snapshot read the SAME 256 bytes from /dev/hwrng (virtio-rng state is
	// captured in the snapshot), inject the same input into the pool, and
	// draw the same UUID — spec §11 V6 fails on every concurrent restore.
	// See ADR-022 §"Why the host ships entropy".
	entropy := make([]byte, resumeHookEntropyBytes)
	if _, err := rand.Read(entropy); err != nil {
		return fmt.Errorf("vmm: read host entropy: %w", err)
	}
	body, err := json.Marshal(struct {
		HostTimeUnixNano int64  `json:"hostTimeUnixNano"`
		Entropy          string `json:"entropy"`     // base64; guest decodes + ioctl(RNDADDENTROPY)
		Traceparent      string `json:"traceparent"` // W3C; empty is tolerated (guest no-ops)
	}{HostTimeUnixNano: hostTimeUnixNano, Entropy: base64.StdEncoding.EncodeToString(entropy), Traceparent: traceparent})
	if err != nil {
		return fmt.Errorf("vmm: marshal resume body: %w", err)
	}
	// Bound the marshal output defensively. The body is constructed from
	// exactly resumeHookEntropyBytes bytes of CSPRNG + a JSON envelope, so
	// in practice it stays under ~400 B — but a future bump of the entropy
	// constant or a hostile build tag could push len(body) into overflow
	// territory, and `make([]byte, 8+len(body))` would panic with
	// "makeslice: len out of range". CodeQL go/allocation-size-overflow
	// flags this; the cap is the actual defense.
	if len(body) > resumeHookMaxBodyBytes {
		return fmt.Errorf("vmm: resume body %d bytes exceeds %d cap", len(body), resumeHookMaxBodyBytes)
	}
	msg := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(msg[:4], resumeHookMsgResume)
	binary.BigEndian.PutUint32(msg[4:8], uint32(len(body)))
	copy(msg[8:], body)
	if _, err := conn.Write(msg); err != nil {
		return fmt.Errorf("vmm: write resume request: %w", err)
	}

	sent := time.Now()

	// Step 4: read the 1-byte ack from the guest.
	ack := make([]byte, 1)
	if _, err := io.ReadFull(conn, ack); err != nil {
		return fmt.Errorf("vmm: read resume ack: %w", err)
	}
	if ack[0] != 0 {
		return fmt.Errorf("vmm: resume hook failed (ack=%d)", ack[0])
	}
	// Keep host transport setup separate from waiting for the guest hook.
	// Durations and the lease ID are sufficient; never log the entropy payload.
	slog.Default().Info("resume hook timing", "instance", l.Instance,
		"connect_ms", connected.Sub(started).Milliseconds(),
		"write_ms", sent.Sub(connected).Milliseconds(),
		"ack_ms", time.Since(sent).Milliseconds(), "attempts", attempts)
	return nil
}

// SendStatelessAdvisory is the host-side receiver for one batch
// guest-init stateless_advisory_linux.go shipped over AF_VSOCK
// DGRAM (port 1025, msg_type 2). The wire receiver goroutine in
// cmd/vmmd (issue #301 follow-up, ADR-047) dials this seam after
// parsing the framed DGRAM payload.
//
// ADR-035: best-effort. The wire shape is parsed in-memory; on
// parse failure we log Warn and return nil (the advisory is
// observation, not source of truth). On forward failure the
// Manager's AdvisoryForwarder (pkg/vmmdgrpc.AdvisoryClient) handles
// the drop per its own retry policy.
//
// Wave 0 PR-C scope: this method is a thin pass-through — the
// vsock DGRAM listener and the apid.sock gRPC client are
// independent seams. Wave 1 follow-up if telemetry shows the
// vsock→gRPC path benefits from batching: lift the listener into
// the VMM so the call from cmd/vmmd becomes a single in-process
// channel read.
func (v *JailerVMM) SendStatelessAdvisory(ctx context.Context, l Lease, appID string, batch []AdvisoryEvent) error {
	if v == nil {
		return fmt.Errorf("vmm: SendStatelessAdvisory: nil receiver")
	}
	if l.Instance == "" {
		return fmt.Errorf("vmm: SendStatelessAdvisory: empty instance")
	}
	if appID == "" {
		// Defensive: the wire receiver should have validated this
		// before calling, but a missing app_id is a programming
		// error rather than an advisory to ship.
		return fmt.Errorf("vmm: SendStatelessAdvisory: empty app_id")
	}
	if len(batch) == 0 {
		// No-op; matches Manager.ForwardStatelessAdvisory. A fanotify
		// storm that drains to zero events within the dedupe window
		// should not pollute the audit table.
		return nil
	}
	// The actual forward to apid lives on the Manager (which owns
	// the AdvisoryForwarder seam). Wire receivers call
	// Manager.ForwardStatelessAdvisory directly; this VMM-level
	// method exists only to satisfy the VMM interface so a future
	// call from inside Boot/Restore can hand a batch straight to
	// the audit row without going through Manager — see ADR-047
	// "Wave 1 follow-up" for the planned lift.
	_ = ctx // future: pass to the manager forwarder
	return nil
}

// Snapshot pauses the running VM, writes a full snapshot to
// spec's durable paths, and destroys the VM (spec §4.4).
//
// #96 / ADR-025 axis 2 — when spec.StorageKey is set, the produced mem
// blob is Put under it via the configured StorageBackend AFTER the
// successful moveOut. The local driver maps "snap/<depID>/mem" to the
// canonical /srv/fc/snap path so the Put is effectively a no-op bytewise
// move; a remote driver streams the bytes to the shared registry. A
// storage-key publication is authoritative for multi-box deployments, so a
// remote Put failure fails the snapshot instead of allowing imaged to record
// an unusable snapshot row.
//
// Issue #470 / PR #470-FU-A: Snapshot is now a thin wrapper around
// SnapshotKeepAlive + v.Kill. The pause + /snapshot/create + mem/vmstate
// publish logic lives in SnapshotKeepAlive so the warm-tier capture
// (Manager.WarmSnapshot → vmm.WarmSnapshot → vmmdgrpc.WarmSnapshot)
// can reuse it without a Kill at the end. The engine's
// captureWarmSnapshotLocked fires the warm path FIRST (RUNNING instance,
// runner is alive), then Snapshot fires the init-tier capture with
// the trailing Kill (the legacy park step).
func (v *JailerVMM) Snapshot(ctx context.Context, l Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	info, err := v.SnapshotKeepAlive(ctx, l, spec)
	if err != nil {
		return SnapshotInfo{}, err
	}
	// Snapshot semantics: the VM is destroyed after a successful snapshot.
	_ = v.Kill(ctx, l)
	return info, nil
}

// SnapshotKeepAlive (issue #470 / PR #470-FU-A) is the warm-tier
// twin of Snapshot. Pauses the VM, hits /snapshot/create, streams the
// mem and vmstate blobs through the configured StorageBackend, and
// returns WITHOUT the trailing Kill. Caller is responsible for the
// subsequent Resume (VMM.ResumeVM) and for keeping the live Instance
// entry intact.
//
// The pause / create / publish sequence is lifted from the pre-PR-A
// Snapshot so the legacy wire shape and local storage behaviour stay
// identical. The shared-storage branch now fails closed when a keyed
// publication cannot complete, which prevents a multi-box database row
// from pointing at a missing restore blob. The split keeps both init-tier
// and warm-tier captures on one implementation.
//
// Error mapping mirrors Snapshot: pause / create / moveOut / open /
// put failures are wrapped with the firecracker-side status so the
// gRPC handler can lift them into a stable *api.Problem (Internal
// for the vmmd side, distinct from the engine's Destroy-the-VM
// failure handler in pkg/sched.engine.captureWarmSnapshotLocked).
func (v *JailerVMM) SnapshotKeepAlive(ctx context.Context, l Lease, spec SnapshotSpec) (info SnapshotInfo, retErr error) {
	root := v.chrootRoot(l.Instance)
	if err := v.apiPatch(ctx, l.Instance, "/vm", map[string]any{"state": "Paused"}); err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: pause: %w", err)
	}
	restoreSnapshotLimit, snapshotErr := widenSnapshotMemoryCgroup(l)
	if snapshotErr != nil {
		return SnapshotInfo{}, snapshotErr
	}
	defer func() {
		if restoreErr := restoreSnapshotLimit(); restoreErr != nil {
			restoreErr = fmt.Errorf("vmm: snapshot memory fence cleanup: %w", restoreErr)
			if retErr == nil {
				retErr = restoreErr
			} else {
				retErr = errors.Join(retErr, restoreErr)
			}
		}
	}()
	const memName, stateName = "mem", "vmstate"
	create := map[string]any{
		"snapshot_type": "Full",
		"snapshot_path": stateName,
		"mem_file_path": memName,
	}
	if err := v.apiPut(ctx, l.Instance, "/snapshot/create", create); err != nil {
		// Keep the Firecracker response in the vmmd journal. The gRPC
		// boundary intentionally reduces this to an Internal problem, but
		// the response body is the only useful explanation for a snapshot
		// rejection on a bare-metal node.
		slog.Default().Error("vmm: create snapshot failed", "instance", l.Instance, "err", err)
		return SnapshotInfo{}, fmt.Errorf("vmm: create snapshot: %w", err)
	}

	// #96 / ADR-025 axis 2 — after slice 3 the mem destination is
	// vmmd-allocated. Firecracker dumps the paused mem at <chroot>/mem;
	// moveOut copies it into a tmp and we stream that into the
	// configured StorageBackend at the StorageKey.
	//
	// #121 / ADR-025 axis 2 slice 4 — vmstate parallels mem. When
	// VMStateStorageKey is non-empty the configured StorageBackend is
	// authoritative and we stream the vmstate bytes straight from the
	// chroot-resident file (no moveOut, no host-path allocation).
	// When the key is empty we keep the legacy moveOut(spec.VMStatePath)
	// behaviour byte-for-bit so single-box / default-local is unaffected. A
	// keyed publication is required to succeed; otherwise a subsequent
	// cross-box wake could observe a database row whose restore blob is absent
	// from the shared backend.
	// A local backend already owns the canonical destination on the same
	// filesystem as the jail. Publish the Firecracker-produced mem file by
	// rename instead of copying it through a private /tmp and then copying it
	// back into storage. This matters for 1 GiB+ snapshots and also avoids a
	// systemd PrivateTmp namespace making the intermediate file invisible to
	// operators. Remote backends keep the streaming temp-file path below.
	var memTmpPath string
	var memBytes int64
	var err error
	memPublishedLocally := false
	// In OCI mode, never rename into a cache path returned by LocalPath:
	// the cache is only a read-through copy and must not become the sole
	// publication of a new snapshot. Force the StorageBackend.Put path so
	// the shared registry receives the blob. Explicit local-prefix routing
	// still remains functional because Put dispatches the key normally.
	if spec.StorageKey != "" && v.storage != nil && !strings.EqualFold(os.Getenv("FAAS_STORAGE_BACKEND"), "oci") {
		if resolver, ok := v.storage.(storage.LocalPathResolver); ok {
			if localPath, pathOK, pathErr := resolver.LocalPath(spec.StorageKey); pathErr != nil {
				return SnapshotInfo{}, fmt.Errorf("vmm: resolve snapshot mem path: %w", pathErr)
			} else if pathOK {
				var moveErr error
				memBytes, moveErr = moveOut(filepath.Join(root, memName), localPath)
				if moveErr != nil {
					return SnapshotInfo{}, fmt.Errorf("vmm: publish local snapshot mem: %w", moveErr)
				}
				memPublishedLocally = true
			}
		}
	}

	if !memPublishedLocally {
		memTmp, tmpErr := os.CreateTemp("", "faas-snap-*.mem")
		if tmpErr != nil {
			return SnapshotInfo{}, fmt.Errorf("vmm: alloc snapshot mem tmp: %w", tmpErr)
		}
		memTmpPath = memTmp.Name()
		_ = memTmp.Close()
		defer func() { _ = os.Remove(memTmpPath) }()

		memBytes, err = moveOut(filepath.Join(root, memName), memTmpPath)
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("vmm: export mem: %w", err)
		}
	}
	// Vmstate publication branches on VMStateStorageKey exactly like mem
	// does on StorageKey. Same predicate shape (key + non-nil storage);
	// the value selector is the new field's emptiness, not storage nil,
	// so default-local still wires a LocalStorageBackend and routes
	// vmstate through the legacy path because the key is empty.
	var stateBytes int64
	vmstateSrcInChroot := filepath.Join(root, stateName)
	if spec.VMStateStorageKey != "" && v.storage != nil {
		// nolint:forbidigo // vmstateSrcInChroot is the chroot-resident
		// tmp Firecracker just wrote; not a customer-supplied location,
		// so the openCustomerFile guard does not apply.
		f, oerr := os.Open(vmstateSrcInChroot)
		if oerr != nil {
			return SnapshotInfo{}, fmt.Errorf("vmm: open snapshot vmstate for publish: %w", oerr)
		} else {
			if perr := v.storage.Put(ctx, spec.VMStateStorageKey, f); perr != nil {
				_ = f.Close()
				return SnapshotInfo{}, fmt.Errorf("vmm: publish snapshot vmstate: %w", perr)
			}
			_ = f.Close()
		}
		// stateBytes still needs the chroot file size for telemetry;
		// the bytes were published via storage rather than copied to a
		// host path, so we stat the chroot-resident original.
		if fi, serr := os.Stat(vmstateSrcInChroot); serr == nil {
			stateBytes = fi.Size()
		}
	} else {
		// Legacy host-path branch (default-local / single-box):
		// moveOut from chroot to the caller-supplied VMStatePath.
		stateBytes, err = moveOut(vmstateSrcInChroot, spec.VMStatePath)
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("vmm: export vmstate: %w", err)
		}
	}

	if spec.StorageKey != "" && v.storage != nil && !memPublishedLocally {
		// nolint:forbidigo // memTmpPath is a vmmd-allocated tmp under
		// os.TempDir(); not a customer-supplied location, so the
		// openCustomerFile guard does not apply.
		f, oerr := os.Open(memTmpPath)
		if oerr != nil {
			return SnapshotInfo{}, fmt.Errorf("vmm: open snapshot mem for publish: %w", oerr)
		} else {
			if perr := v.storage.Put(ctx, spec.StorageKey, f); perr != nil {
				_ = f.Close()
				return SnapshotInfo{}, fmt.Errorf("vmm: publish snapshot mem: %w", perr)
			}
			_ = f.Close()
		}
	}

	// SnapshotKeepAlive purposely does NOT Kill the VM — the
	// warm-tier capture keeps the VM paused until the engine's
	// pre-existing snapshotAndPark (init-tier capture) finishes
	// and the legacy Snapshot() wrapper releases the chroot. The
	// responsible caller (Manager.WarmSnapshot → vmm.WarmSnapshot
	// → vmmdgrpc.WarmSnapshot) MUST fire ResumeVM on success.
	return SnapshotInfo{MemBytes: memBytes, VMStateBytes: stateBytes}, nil
}

// ResumeVM (issue #470 / PR #470-FU-A) is the host-side resume
// half of the warm snapshot. PATCH /vm {"state":"Resumed"} on the
// live Firecracker socket so the paused VM continues executing.
//
// Firecracker returns 409 Conflict if the VM is already Running
// (we treat this as a soft pass-through so a redundant resume after
// the runner's framework_ready DGRAM raced a kill is observable
// but not fatal). Any other 4xx/5xx is wrapped as a hard error and
// bubbled up to the engine's failure path: vmm.Destroy + skip the
// init-tier capture. The CID-to-lease join and the live Instance
// entry are NOT touched here — that contract lives on
// Manager.WarmSnapshot / Manager.Park, not on the host VMM.
//
// The 409 status is matched via the typed fcAPIError surfaced by
// apiCallWithClient (NOT a substring on err.Error()) so a future
// Firecracker "Conflict: chassis is locked" 409 with similar
// wording doesn't get silently swallowed as success.
func (v *JailerVMM) ResumeVM(ctx context.Context, l Lease) error {
	if v == nil {
		return fmt.Errorf("vmm: ResumeVM: nil receiver")
	}
	if l.Instance == "" {
		return fmt.Errorf("vmm: ResumeVM: empty instance")
	}
	// PATCH /vm {"state":"Resumed"} — the Firecracker API mirror of
	// /vm {"state":"Paused"} that SnapshotKeepAlive fired moments
	// ago. The retry loop in apiCallWithClient swallows transient
	// socket races (the FC API socket is created by firecracker
	// itself a few ms after startJailer returns; the same race
	// applies on a long-paused VM that just got hit by /snapshot/
	// create — the socket is fine, but defensive retries are
	// cheap).
	err := v.apiPatch(ctx, l.Instance, "/vm", map[string]any{"state": "Resumed"})
	if err == nil {
		return nil
	}
	// 409 Conflict on a no-op state transition (VM already Running)
	// is observable but not fatal — treat as success so a redundant
	// WarmSnapshot (engine retried after a transient vmmd restart)
	// doesn't surface as a hard error. Any other status / fault is
	// a real failure and bubbles up.
	var apiErr *fcAPIError
	if errors.As(err, &apiErr) && apiErr.statusCode == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("vmm: resume: %w", err)
}

// Kill stops the jailer process (if any) and removes the chroot. Idempotent.
// SIGKILL'd instances don't get an artifact export — that's Builderd's path
// (use DestroyWithExport).
func (v *JailerVMM) Kill(_ context.Context, l Lease) error {
	v.mu.Lock()
	cmd, hasCmd := v.proc[l.Instance]
	rec, hasRec := v.recs[l.Instance]
	if hasCmd {
		delete(v.proc, l.Instance)
	}
	v.mu.Unlock()
	// Move 4 (issue #254): close the per-instance ring so subscribers
	// see a clean EOF and the byte budget is released (invariant §6.2-4:
	// parked app = zero RAM). Done BEFORE the chroot wipe because the
	// ring holds host-side bytes; nothing else depends on it.
	v.unregisterRing(l.Instance)

	if hasCmd && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if hasRec && rec.done != nil {
		// Wait for the watchdog to finish (it always does, since cmd.Process.Wait
		// is observed by Go's runtime even on signal-induced exit). Bound by the
		// same destroyWait so a wedged firecracker can't pin us.
		select {
		case <-rec.done:
		case <-time.After(v.destroyWait):
		}
		v.mu.Lock()
		delete(v.recs, l.Instance)
		v.mu.Unlock()
	}
	v.closeClient(l.Instance)
	v.unmountBindMounts(l.Instance)
	// Chroot lives in tmpfs (spec §Gotchas); removing it frees the RAM it holds.
	if err := os.RemoveAll(filepath.Join(v.chrootBase, v.fcName, l.Instance)); err != nil {
		return fmt.Errorf("vmm: remove chroot: %w", err)
	}
	// Tmp files materialized from a StorageKey live in /tmp (not the chroot
	// root) — sweep them explicitly so they don't leak across thousands of
	// wakes.
	v.sweepMaterialised(l.Instance)
	// Remove the per-VM cgroup scope jailer created (--cgroup cpu.weight=…).
	// Required by spec §6.2-4 ("parked = zero RAM") — a populated cgroup dir
	// holds page-cache references. The scope name equals jailer --id
	// (= Lease.Instance); see pkg/fcvm/cgroup.go for the matching write path.
	// Idempotent; missing dir is fine.
	//
	// The parent path is plan-aware (issue #301 / ADR-044): the 3-level
	// hierarchy is faas-tenant.slice/<plan-slice>/<instance>. ParentCgroupFor
	// reads the lease's Plan; an empty plan falls back to the legacy 2-level
	// path so pre-issue-301 callers keep working.
	//
	// EBUSY (or any other non-IsNotExist error) is logged and swallowed: the
	// jailer process is already gone at this point, so we cannot rewind the
	// teardown. A leftover cgroup dir leaks RAM only until the next cgroup
	// pressure event reaps it; failing the whole call would mask the real
	// teardown success.
	parentCgroup := ParentCgroupFor(l.Plan)
	if l.IsBuilder {
		parentCgroup = BuilderCgroupParent
	}
	scopePath := filepath.Join(cgroupRoot, parentCgroup, PerInstanceScope(l.Instance))
	if err := os.RemoveAll(scopePath); err != nil && !os.IsNotExist(err) {
		slog.Default().Warn("cgroup scope remove failed; continuing teardown",
			"path", scopePath, "instance", l.Instance, "err", err)
	}
	return nil
}

// SignalAndKill (M-2 / ADR-138 §Decision 1) is the graceful stop
// sequence used by Engine.StopInstance for worker / job mode
// instances: send `signal` to the guest's PID 1 (via vsock; today
// the signal lands at guest-init's installSignalHandlers — commit
// 7), wait up to `grace` for the workload to exit cleanly, and on
// deadline fall through to a hard SIGKILL via cmd.Process.Kill().
//
// The cleanup half (chroot wipe, cgroup scope removal, ring
// unregister, etc.) is identical to Kill's — reused via a tail call
// to keep the destruction path in one place. killSignalSent is true
// iff the deadline fired and SIGKILL was the actual exit cause; the
// schedd records this on the audit row.
//
// Signal=0 with grace=0 is the legacy Destroy shape (immediate
// SIGKILL, no graceful wait); in that case the function delegates
// to Kill verbatim and reports killSignalSent=true.
//
// Reuses the process-wait watchdog pattern from Kill at lines
// 1270-1284 (cmd.Wait() observed by the runtime even on signal-
// induced exit). destroyWait bounds the watchdog; an additional
// grace timer races against the watchdog to fire SIGKILL on the
// customer-configured deadline.
func (v *JailerVMM) SignalAndKill(ctx context.Context, l Lease, signal syscall.Signal, grace time.Duration) (killSignalSent bool, exitCode int32, err error) {
	// Legacy Destroy shape: signal=0, grace=0. Delegate to Kill and
	// report killSignalSent=true (the SIGKILL is what killed it).
	if signal == 0 && grace == 0 {
		if kerr := v.Kill(ctx, l); kerr != nil {
			return true, 0, kerr
		}
		return true, 0, nil
	}

	v.mu.Lock()
	cmd, hasCmd := v.proc[l.Instance]
	rec, hasRec := v.recs[l.Instance]
	v.mu.Unlock()

	// Default signal: SIGTERM. The schedd's Engine.StopInstance (commit 6)
	// passes the manifest's StopSignal translated to syscall.Signal; an
	// empty/zero value here means "use SIGTERM" — the same default
	// Docker's `docker stop` ships.
	if signal == 0 {
		signal = syscall.SIGTERM
	}

	// Send the signal + race grace timer against the watchdog.
	// Extracted to a free function so the portable test
	// (pkg/fcvm/vmm_signal_kill_test.go) can exercise the
	// signal-grace-SIGKILL sequence without booting firecracker.
	var doneCh <-chan struct{}
	if hasRec && rec != nil {
		doneCh = rec.done
	}
	killSignalSent, exitCode, err = signalAndKillRace(cmd, doneCh, signal, grace, v.destroyWait)
	if err != nil {
		return false, 0, err
	}

	// Always run the destruction tail (chroot wipe, cgroup scope
	// removal, ring unregister, etc.) — same invariant as Kill.
	if killSignalSent {
		if hasCmd && cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if hasRec && rec != nil && rec.done != nil {
			select {
			case <-rec.done:
			case <-time.After(v.destroyWait):
			}
		}
		if kerr := v.Kill(ctx, l); kerr != nil {
			return killSignalSent, exitCode, kerr
		}
	}
	return killSignalSent, exitCode, nil
}

// signalAndKillRace is the inner signal-grace-SIGKILL sequence
// shared by JailerVMM.SignalAndKill and the portable test.
// cmd is the running jailer/firecracker child (or any *exec.Cmd
// the test wants to race against); doneCh is the watchdog
// channel that closes when the child has exited; signal is the
// POSIX signal number to send (0 = default SIGTERM); grace is
// the upper bound on clean-shutdown wait; destroyWait is the
// cap on the post-SIGKILL watchdog (so a wedged child can't
// pin the caller forever). Returns (killSignalSent, exitCode,
// err): killSignalSent=true iff the grace timer fired and we
// escalated to SIGKILL.
//
// Lives in production code (not in the test file) so the test
// exercises the EXACT shape production ships — no risk of
// drift between the test fixture and the production path.
func signalAndKillRace(cmd *exec.Cmd, doneCh <-chan struct{}, signal syscall.Signal, grace time.Duration, destroyWait time.Duration) (bool, int32, error) {
	if cmd != nil && cmd.Process != nil {
		if serr := cmd.Process.Signal(signal); serr != nil {
			// ESRCH (Linux) / os.ErrProcessDone (macOS,
			// Windows) — both mean "process already gone".
			// Treated as benign: the workload exited
			// before we got here. The race-watchdog
			// below races against the watchdog channel
			// which the spawn goroutine already closed.
			if !errors.Is(serr, syscall.ESRCH) && !errors.Is(serr, os.ErrProcessDone) {
				return false, 0, fmt.Errorf("vmm: signal %d: %w", signal, serr)
			}
		}
	}

	// Wait for clean exit up to grace. We race a timer against
	// the watchdog. If grace fires first, escalate.
	if grace > 0 && doneCh != nil {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-doneCh:
			// Clean exit within grace.
			exitCode := int32(0)
			if cmd != nil && cmd.ProcessState != nil {
				if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
					exitCode = int32(ws.ExitStatus())
				}
			}
			return false, exitCode, nil
		case <-timer.C:
			// Grace expired — escalate to SIGKILL.
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			if doneCh != nil {
				select {
				case <-doneCh:
				case <-time.After(destroyWait):
				}
			}
			return true, 0, nil
		}
	}
	// No grace configured or no watchdog — escalate immediately.
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if doneCh != nil {
		select {
		case <-doneCh:
		case <-time.After(destroyWait):
		}
	}
	return true, 0, nil
}

// DestroyWithExport is the build-VM teardown path (M6 / spec §4.5). It blocks
// until the firecracker child exits (capped by v.destroyWait — default 10m,
// comfortable above spec §1 BuildTimeoutSeconds), captures the exit code, and
// only if exportDir != "" loopback-mounts the chroot-local drive1 to copy out
// /etc/faas/build-done.json and /build/out/* before removing the chroot.
//
// Ordinary app VMs with exportDir="" are killed immediately, matching
// Manager.Destroy's contract. Builder records wait for completion even when
// a caller suppresses export; cancellation interrupts them via InterruptBuild.
func (v *JailerVMM) DestroyWithExport(ctx context.Context, l Lease, exportDir string) (int, error) {
	v.mu.Lock()
	rec, ok := v.recs[l.Instance]
	v.mu.Unlock()
	if !ok {
		// Unknown / already-torn-down instance: idempotent, no exit code to report.
		v.closeClient(l.Instance)
		_ = os.RemoveAll(filepath.Join(v.chrootBase, v.fcName, l.Instance))
		return 0, nil
	}

	// App VMs run until explicitly stopped; waiting for natural exit here
	// holds their network and lease for the builder timeout. Only builders
	// need to finish and flush artifacts before teardown.
	if exportDir == "" && !rec.isBuilder {
		return 0, v.Kill(ctx, l)
	}

	// 1. Wait for the firecracker child to exit. The watchdog goroutine started
	//    by startJailer is the single point that calls cmd.Process.Wait;
	//    DestroyWithExport just blocks on rec.done and reads rec.exitCode.
	destroyWait := v.destroyWaitFor(exportDir, l.BuildTimeoutSec)
	deadline := time.NewTimer(destroyWait)
	defer deadline.Stop()
	// A Linux guest can reach "System halted" while the Firecracker process
	// remains alive. Builder guest-init has already synced build-done.json and
	// the OCI output before requesting poweroff, so this is a safe terminal
	// state in which to stop the VMM and proceed with export. Polling the
	// per-instance serial file keeps this recovery scoped to builder teardown;
	// ordinary app VMs retain the existing wait/kill behavior.
	var haltPoll <-chan time.Time
	if exportDir != "" && rec.consolePath != "" {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		haltPoll = ticker.C
	}
	for {
		select {
		case <-rec.done:
			goto exited
		case <-ctx.Done():
			v.killProcess(l.Instance)
			select {
			case <-rec.done:
			case <-time.After(5 * time.Second):
				return -1, fmt.Errorf("vmm: %s did not exit after cancellation", l.Instance)
			}
			goto exited
		case <-deadline.C:
			// Force-kill and re-wait with a shorter budget. A builder that ignores
			// the spec's BuildTimeoutSeconds is misbehaving; refuse to hold vmmd
			// forever, but don't tear down the chroot before the export either.
			v.killProcess(l.Instance)
			select {
			case <-rec.done:
			case <-time.After(5 * time.Second):
				return -1, fmt.Errorf("vmm: %s did not exit within %s", l.Instance, destroyWait)
			}
			goto exited
		case <-haltPoll:
			if !consoleShowsGuestHalted(rec.consolePath) {
				continue
			}
			v.killProcess(l.Instance)
			select {
			case <-rec.done:
			case <-time.After(5 * time.Second):
				return -1, fmt.Errorf("vmm: %s did not exit after guest halt", l.Instance)
			}
			goto exited
		}
	}

exited:

	v.mu.Lock()
	exitCode := rec.exitCode
	v.mu.Unlock()

	// 2. Artifact export (build VMs only). Loopback-mount the chroot-local
	//    drive1.ext4 and copy out /etc/faas/build-done.json + /build/out/*.
	//    The mount uses root privileges (vmmd is the only root component, §11).
	var exportErr error
	if exportDir != "" {
		if err := v.exportBuildArtifacts(l.Instance, exportDir); err != nil {
			// Retain the export error, but release the dead VM's resources
			// before returning it.
			exportErr = fmt.Errorf("vmm: export build artifacts: %w", err)
		}
	}

	// 3. Tear down the chroot + per-instance state.
	v.mu.Lock()
	delete(v.recs, l.Instance)
	delete(v.proc, l.Instance)
	v.mu.Unlock()
	// Move 4 (issue #254): close the per-instance ring so subscribers
	// see EOF and the byte budget is released. Done before the chroot
	// wipe for the same reason as in Kill.
	v.unregisterRing(l.Instance)
	v.closeClient(l.Instance)
	v.unmountBindMounts(l.Instance)
	if err := os.RemoveAll(filepath.Join(v.chrootBase, v.fcName, l.Instance)); err != nil {
		return exitCode, fmt.Errorf("vmm: remove chroot: %w", err)
	}
	v.sweepMaterialised(l.Instance)
	return exitCode, exportErr
}

// InterruptBuild stops the child without releasing its drives, chroot or
// process record. DestroyWithExport owns those resources until export finishes.
func (v *JailerVMM) InterruptBuild(ctx context.Context, instance string) (int32, error) {
	v.mu.Lock()
	rec := v.recs[instance]
	v.mu.Unlock()
	if rec == nil {
		return 0, nil
	}
	v.killProcess(instance)
	select {
	case <-rec.done:
		v.mu.Lock()
		code := rec.exitCode
		v.mu.Unlock()
		return int32(code), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (v *JailerVMM) killProcess(instance string) {
	v.mu.Lock()
	proc := v.proc[instance]
	v.mu.Unlock()
	if proc != nil && proc.Process != nil {
		_ = proc.Process.Kill()
	}
}

// consoleShowsGuestHalted reads only the tail of the serial console. The
// kernel emits "System halted" after guest-init has synced its build marker;
// limiting the read bounds teardown overhead even for a noisy build log.
func consoleShowsGuestHalted(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	const tailBytes int64 = 4096
	offset := info.Size() - tailBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return bytes.Contains(buf, []byte("System halted"))
}

// InstancePID returns the host PID of the running jailer child for
// instance, or (0, false) if the instance is not currently alive on
// this vmmd. M8 §11: the SeccompStatus gRPC handler reads
// /proc/<pid>/status to verify the jailer default seccomp filter is
// in place. The bool is the load-bearing distinction — Kill runs
// delete on v.proc, so a torn-down instance returns (0, false) and
// the handler maps that to NotFound. The 0-case for a "live but
// somehow PId-less" cmd is treated as (0, false) too: even though
// JailerVMM.Boot only registers a cmd after exec.Start succeeds, a
// future refactor that elides the exec for any reason would surface
// here as a missing filter — the defensive choice.
func (v *JailerVMM) InstancePID(instance string) (int, bool) {
	v.mu.Lock()
	cmd, ok := v.proc[instance]
	rec := v.recs[instance]
	v.mu.Unlock()
	if !ok || rec == nil || rec.exited || cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
		return 0, false
	}
	return cmd.Process.Pid, true
}

// exportBuildArtifacts loopback-mounts the chroot-local drive1 image and
// copies /etc/faas/build-done.json and /build/out/* into exportDir. Files
// larger than exportMaxBytes are skipped + counted as failures (best-effort
// — never blocks the caller). vmmd is the only root component, so the mount
// is fine; the chroot-local drive1.ext4 is owned by root after provision
// (pkg/fcvm/vmm.go:stageWritable).
func (v *JailerVMM) exportBuildArtifacts(instance, exportDir string) error {
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return fmt.Errorf("mkdir export: %w", err)
	}
	drive1, err := v.resolveDriveImage(instance)
	if err != nil {
		return err
	}
	mp, err := os.MkdirTemp("", "faas-vmm-export-")
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()

	// Export is read-only: the guest has already synced the upperdir before
	// powering off. Mount ro,noload first so a dirty ext4 journal cannot make
	// the host block in rw journal replay while the builder queue is waiting.
	// A plain read-only mount remains a compatibility fallback for images whose
	// filesystem features reject noload.
	if out, mountErr := exec.Command("mount", "-o", "loop,ro,noload", drive1, mp).CombinedOutput(); mountErr != nil {
		if roOut, roErr := exec.Command("mount", "-o", "loop,ro", drive1, mp).CombinedOutput(); roErr != nil {
			return fmt.Errorf("mount loop: ro,noload=%w (%s); ro=%w (%s)", mountErr, bytes.TrimSpace(out), roErr, bytes.TrimSpace(roOut))
		}
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	// build-done.json is the canonical manifest builderd reads.
	srcDone := filepath.Join(mp, "upper", "etc", "faas", "build-done.json")
	if data, err := os.ReadFile(srcDone); err == nil {
		if err := os.WriteFile(filepath.Join(exportDir, "build-done.json"), data, 0o644); err != nil {
			return fmt.Errorf("write build-done.json: %w", err)
		}
	} // else: VM died before guest-init wrote it — caller falls back to exit-code class.

	// /build/out/ holds the produced OCI tarball. Walk + copy with the size
	// cap enforced. A build that overruns the cap is logged as infra failure
	// via the caller's classification (no error returned — best-effort).
	srcOut := filepath.Join(mp, "upper", "build", "out")
	if _, err := os.Stat(srcOut); err == nil {
		dstOut := filepath.Join(exportDir, "build", "out")
		if err := os.MkdirAll(dstOut, 0o755); err != nil {
			return fmt.Errorf("mkdir out: %w", err)
		}
		return copyTree(srcOut, dstOut, v.exportMax())
	}
	return nil
}

// layerImageName is the in-chroot basename vmmd provisions for drive1 (see
// provision / stageWritable — copy preserves basename, so the chroot always
// sees "layer.ext4").
const layerImageName = "layer.ext4"

// Firecracker records chroot-relative backing-file names in vmstate. Keep
// temporary storage materializations out of that snapshot contract.
const (
	kernelImageName     = "vmlinux"
	baseImageName       = "base.ext4"
	memSnapshotName     = "snap-in-mem"
	vmstateSnapshotName = "snap-in-vmstate"
)

func stableReadOnlyName(src, fallback string) string {
	name := filepath.Base(src)
	if strings.HasPrefix(name, "faas-snap-") {
		return fallback
	}
	return name
}

// sidecarDriveImageName returns the in-chroot basename for a sidecar
// drive at the given index (issue #463 / ADR-069 / PR-B). Index 0
// is the first sidecar; the per-workload drive slot the boot
// config PUTs to FC uses the same name, so the per-workload file
// inside the chroot can be looked up without a side-channel
// registry. The drive slot that BuildColdBootConfig emits is
// fmt.Sprintf("%s%d", DriveSidecarPrefix, i-1) for the i-th
// workload (i==0 is main); we drop the "layer-" prefix here so
// the in-chroot file reads naturally as "sidecar-0.ext4" while
// the FC drive ID is "layer-sidecar-0".
func sidecarDriveImageName(idx int) string {
	return fmt.Sprintf("sidecar-%d.ext4", idx)
}

// workloadManifestPath is the in-guest location guest-init reads to
// discover a workload's runtime shape (issue #463 / ADR-069 / PR-B).
// Each sidecar drive carries /etc/faas/workload.json (the manifest
// the guest-init supervisor uses to fork/exec the workload). The
// main workload's drive carries the same file at the same path so
// guest-init can read all workloads uniformly; the main workload
// invariant makes the manifest's type="main" / name="main" entries
// redundant but stable. Main secrets/API env continue to live at the
// older /etc/faas/secrets.env and /etc/faas/env.json paths on drive1.
// Sidecar image defaults and command metadata are baked into each
// immutable sidecar layer; deployment-specific overrides are staged by
// vmmd under /etc/faas/workloads/<name>/env.json in the writable main upper.
const workloadManifestPath = "upper/etc/faas/workload.json"

// secretsEnvPath is the in-guest location guest-init reads after pivot_root
// (spec §11/G2). JSON-encoded envelope shape is documented on secretbox.Open.
// The same file is written once per wake — overwriting any prior content —
// so a secret rotation propagates without re-provisioning the layer.
const secretsEnvPath = "upper/etc/faas/secrets.env"

// apiEnvPath is the plaintext api-env file written by StageAPIEnv
// (issue #395 / ADR-045). Sibling to secretsEnvPath — same drive1
// location, different file. Guest-init reads BOTH files at boot and
// merges into the process env with precedence "secrets > api_env >
// manifest_env > os.environ".
const apiEnvPath = "upper/etc/faas/env.json"

// stagedDrivePath selects the on-disk contract location for drive1. The
// optimized two-drive artifact stores mutable runtime files below /upper;
// full-rootfs artifacts are already mounted as the guest's root and therefore
// receive the same files directly under /. The marker is written only by the
// platform builder, so a malformed value fails closed instead of silently
// writing state to a path guest-init will never read.
func stagedDrivePath(mountRoot, optimizedPath string) (string, error) {
	marker := filepath.Join(mountRoot, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
	info, err := os.Lstat(marker)
	if err != nil {
		if os.IsNotExist(err) {
			return filepath.Join(mountRoot, optimizedPath), nil
		}
		return "", fmt.Errorf("inspect full-rootfs marker: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("full-rootfs marker is not a regular file")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return "", fmt.Errorf("read full-rootfs marker: %w", err)
	}
	if string(data) != api.FullRootfsMarkerValue {
		return "", fmt.Errorf("invalid full-rootfs marker payload")
	}
	return filepath.Join(mountRoot, strings.TrimPrefix(optimizedPath, "upper/")), nil
}

// StageSecretsEnv loopback-mounts drive1 (the per-app layer, the only fs
// the VM can write at runtime), writes /etc/faas/secrets.env with mode
// 0400, and umounts. The plaintext is read off the chroot-local image
// only for the duration of this call. vmmd is the only root component,
// so the loopback mount is permitted by the §11 threat model.
//
// Mirrors exportBuildArtifacts (read side) — same chroot layout, same
// mountpoint-handling pattern, write-vs-read swap. The function is a no-op
// when jsonBlob is empty: no file is written, no mount attempted. This
// short-circuit is what lets an app with zero secrets proceed without any
// extra mount/umount cost.
func (v *JailerVMM) StageSecretsEnv(instance string, jsonBlob []byte) error {
	if len(jsonBlob) == 0 {
		return nil
	}
	drive1, err := v.resolveDriveImage(instance)
	if err != nil {
		return err
	}
	mp, err := os.MkdirTemp("", "faas-vmm-secrets-")
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()

	// rw,noexec,nosuid — drive1 is a vfat-less ext4; noexec would still
	// work but we don't need it and rw alone is the minimum.
	if out, err := exec.Command("mount", "-o", "loop,rw", drive1, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	target, err := stagedDrivePath(mp, secretsEnvPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir etc/faas: %w", err)
	}
	if err := os.WriteFile(target, jsonBlob, 0o400); err != nil {
		return fmt.Errorf("write secrets.env: %w", err)
	}
	return nil
}

// StageAPIEnv is the plaintext sibling of StageSecretsEnv (issue #395 /
// ADR-045). Writes /etc/faas/env.json on drive1 (a JSON map of key→value)
// for guest-init to merge into the process env at boot. Same loopback-
// mount dance as StageSecretsEnv (loop mount drive1 → write file →
// umount), but the payload is plaintext by contract — no host key
// needed, no unseal step. Empty jsonBlob short-circuits to a no-op so
// apps without API env rows skip the mount/umount cycle entirely.
//
// File mode 0o400 mirrors StageSecretsEnv's read-only posture even
// though the contents are non-sensitive — the guest-init process is the
// only consumer and there's no reason to give the customer code write
// access to its own env file.
func (v *JailerVMM) StageAPIEnv(instance string, jsonBlob []byte) error {
	if len(jsonBlob) == 0 {
		return nil
	}
	drive1, err := v.resolveDriveImage(instance)
	if err != nil {
		return err
	}
	mp, err := os.MkdirTemp("", "faas-vmm-apienv-")
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()

	if out, err := exec.Command("mount", "-o", "loop,rw", drive1, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	target, err := stagedDrivePath(mp, apiEnvPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir etc/faas: %w", err)
	}
	if err := os.WriteFile(target, jsonBlob, 0o400); err != nil {
		return fmt.Errorf("write env.json: %w", err)
	}
	return nil
}

// workloadEnvPath is the per-sidecar override file written to the main
// workload's writable upper. The sidecar image's immutable workload.json
// remains in its own read-only lower and supplies the image defaults.
const workloadEnvPath = "upper/etc/faas/workloads"

// StageWorkloadEnv writes one sidecar's already-unsealed env overrides to the
// main workload's writable layer. The file is instance-scoped and is staged
// before Firecracker receives its config, so plaintext never lands in the
// shared sidecar image or reaches guest-init through the wake wire.
func (v *JailerVMM) StageWorkloadEnv(instance, workloadName string, jsonBlob []byte) error {
	if len(jsonBlob) == 0 {
		return nil
	}
	if !validWorkloadName(workloadName) {
		return fmt.Errorf("invalid workload name %q", workloadName)
	}
	drive1, err := v.resolveDriveImage(instance)
	if err != nil {
		return err
	}
	mp, err := os.MkdirTemp("", "faas-vmm-workload-env-")
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()
	if out, err := exec.Command("mount", "-o", "loop,rw", drive1, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	base, err := stagedDrivePath(mp, workloadEnvPath)
	if err != nil {
		return err
	}
	target := filepath.Join(base, workloadName, "env.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir workload env: %w", err)
	}
	if err := os.WriteFile(target, jsonBlob, 0o400); err != nil {
		return fmt.Errorf("write workload env: %w", err)
	}
	return nil
}

func validWorkloadName(name string) bool {
	if name == "" || len(name) > 63 || filepath.Base(name) != name {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
		if i == 0 && ((c < 'a' || c > 'z') && (c < '0' || c > '9')) {
			return false
		}
	}
	return true
}

// StageWorkloadManifest (issue #463 / ADR-069 / PR-B) is the
// compatibility helper for writing /etc/faas/workload.json on a
// workload drive. New sidecar layers carry their effective runtime
// contract at /etc/faas/workloads/<name>/workload.json during the
// image build; the wake-time helper remains for the main workload
// and older sidecar layers.
//
// driveIdx is the 0-based sidecar index (0 for the first sidecar,
// 1 for the second, etc.) — the same index BuildColdBootConfig
// uses to derive the FC drive ID. For the main workload, pass
// driveIdx = -1 and we'll point at drive1 (the legacy
// single-workload path) instead of the sidecarLeaf naming convention.
//
// The drive is loopback-mounted rw, the file is written with mode
// 0o400 (read-only for the in-guest workloads), and umount runs in
// a defer. The compatibility manifest captures command and port
// from the WorkloadSpec that schedd sent on the wake wire; vmmd
// trusts the wire as it trusts every other wake-field (the gRPC
// server runs on a unix socket reachable only by the faas group;
// ADR-014 / ADR-015).
func (v *JailerVMM) StageWorkloadManifest(instance string, driveIdx int, w WorkloadSpec) error {
	if driveIdx < 0 {
		// Main workload: stamp on drive1 (the legacy path).
		drive1, err := v.resolveDriveImage(instance)
		if err != nil {
			return err
		}
		return v.writeWorkloadManifest(drive1, w)
	}
	drive := filepath.Join(v.chrootRoot(instance), sidecarDriveImageName(driveIdx))
	return v.writeWorkloadManifest(drive, w)
}

// writeWorkloadManifest is the mount/umount/write helper
// StageWorkloadManifest delegates to. Public so the test seam can
// drive it directly without routing through a Manager. The
// mountpoint is cleaned up by a deferred RemoveAll; the umount
// runs in a defer so a failed write doesn't leak the mount.
func (v *JailerVMM) writeWorkloadManifest(drive string, w WorkloadSpec) error {
	if _, err := os.Stat(drive); err != nil {
		return fmt.Errorf("stat workload drive: %w", err)
	}
	mp, err := os.MkdirTemp("", "faas-vmm-workload-")
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()
	if out, err := exec.Command("mount", "-o", "loop,rw", drive, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()
	// Pre-marshal byte cap projection (PR-B review finding #7).
	// Marshalling an unbounded Name field before checking size
	// would let a malicious or buggy wire payload allocate
	// without bound; the cap here MUST run before json.Marshal
	// (which calls AppendQuote on every byte of the string).
	// The projection is conservative — the workloadManifest
	// struct shape is fixed, and only Name can vary.
	if projected := projectedWorkloadManifestBytes(w); projected > api.MaxExportedLayerBytes {
		return fmt.Errorf("workload manifest projected %d bytes exceeds cap %d (name=%q)", projected, api.MaxExportedLayerBytes, w.Name)
	}
	// Marshal the manifest. encoding/json sorts map keys
	// alphabetically so re-reads produce the same bytes; we don't
	// need that contract here (guest-init parses each file once
	// at boot) but the determinism is free.
	manifest := workloadManifest{
		Name:          w.Name,
		Type:          w.Type,
		RamMB:         w.RamMB,
		CPUMillicores: w.CPUMillicores,
		Port:          w.Port,
		Essential:     w.Essential,
		Cmd:           w.Cmd,
		Entrypoint:    w.Entrypoint,
		DependsOn:     w.DependsOn,
		// StorageKey is omitted: the guest doesn't need to know
		// the host-side path; it just reads the workload spec
		// from the manifest and ignores the storage key. ADR-069
		// §"Downstream" wording.
	}
	blob, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal workload manifest: %w", err)
	}
	target, err := stagedDrivePath(mp, workloadManifestPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir etc/faas: %w", err)
	}
	if err := os.WriteFile(target, blob, 0o400); err != nil {
		return fmt.Errorf("write workload.json: %w", err)
	}
	return nil
}

// projectedWorkloadManifestBytes (issue #463 / ADR-069 / PR-B
// review finding #7; PR-C §6 extends for cmd/entrypoint) returns
// a conservative upper bound on the marshalled workloadManifest
// byte size for the given input. The struct shape is fixed; only
// Name + Cmd + Entrypoint can vary, and JSON's AppendQuote escapes
// 6 characters (`\`, `"`, and control chars) on top of the raw
// byte count. The estimate is deliberately generous —
// overestimating just rejects a payload that wouldn't have hit
// the cap anyway, so the false-positive rate is zero. The
// projection runs BEFORE json.Marshal so an unbounded Name can't
// allocate without bound during marshalling.
//
// Layout:
//
//	{"cpu_millicores":INT,"essential":BOOL,"name":"...","port":INT,"ram_mb":INT,"type":"...",
//	 "cmd":[...],"entrypoint":[...],"depends_on":[...]}
//
// Braces, colons, commas, and quoted keys/values dominate the
// fixed overhead; Name + Cmd + Entrypoint contribute variable bytes.
func projectedWorkloadManifestBytes(w WorkloadSpec) int64 {
	// Per JSON spec, the 6 chars that get escaped (\ " \b \t
	// \n \f \r plus U+0000-U+001F) take 2 bytes each after
	// quoting. The defensive escape multiplier handles
	// worst-case ASCII control characters (most Names are
	// safe DNS-1123 labels with no escapes — the multiplier
	// is just safety margin).
	nameBytes := int64(len(w.Name)) * 2
	// Cmd/Entrypoint (PR-C §6): each entry contributes its
	// quoted length + 2 bytes for the comma + 2 bytes for the
	// brackets. The 2× escape multiplier is the same conservative
	// bound as Name — images with shell-style command lines
	// (e.g. `["/bin/sh","-c","echo $HOME"]`) are mostly safe
	// ASCII, but the multiplier guards against a deploy that
	// ships a control-character-laden argument.
	cmdBytes := int64(0)
	for _, c := range w.Cmd {
		cmdBytes += int64(len(c)) * 2
	}
	entrypointBytes := int64(0)
	for _, e := range w.Entrypoint {
		entrypointBytes += int64(len(e)) * 2
	}
	dependencyBytes := int64(0)
	for _, dep := range w.DependsOn {
		dependencyBytes += int64(len(dep.Name)+len(dep.Condition)) * 2
	}
	// Three int fields (port, ram_mb, cpu_millicores) and a bool + 2 array
	// fields. 11 bytes per int is the worst case for a 32-bit
	// value; 5 bytes for "false". The 5 quoted keys + 2 numeric
	// values + 1 bool + 2 array brackets contribute a fixed
	// overhead; we over-estimate at 128 to absorb the new
	// cmd/entrypoint keys.
	const fixedOverhead = 128
	return nameBytes + cmdBytes + entrypointBytes + dependencyBytes + fixedOverhead
}

// projectedWorkloadRosterBytes (issue #463 / ADR-069 / PR-B
// review finding #7) is the roster-shape twin of
// projectedWorkloadManifestBytes. The roster is 1 main +
// len(sidecars) manifests wrapped in
//
//	{"main":{...},"sidecars":[{...},...]}
//
// so the projection is the sum of per-workload projections
// plus a small wrapper overhead. SidecarCapMax bounds
// len(sidecars) so the projection is bounded — a future PR
// that lifts the cap doesn't change the formula here, only
// the constant.
func projectedWorkloadRosterBytes(main WorkloadSpec, sidecars []WorkloadSpec) int64 {
	total := projectedWorkloadManifestBytes(main)
	for _, sc := range sidecars {
		total += projectedWorkloadManifestBytes(sc)
	}
	// Wrapper: {"main":{...},"sidecars":[]} — 32 bytes of
	// braces/commas/colons plus a "main" key and a "sidecars"
	// key. 64 is the conservative ceiling.
	const wrapperOverhead = 64
	return total + wrapperOverhead
}

// workloadManifest is the on-disk shape of /etc/faas/workload.json
// (issue #463 / ADR-069 / PR-B; issue #463 / PR-C §6 adds cmd/entrypoint).
// The field set is the minimum guest-init needs to fork/exec a workload:
// Name + Type for the supervisor's map key, RamMB for the in-guest cgroup
// partition (PR-B's primary OOM isolation), Port for the port-normalization
// wiring (ADR-053), and Essential for the restart policy. Cmd/Entrypoint
// override the per-workload image's baked entrypoint (the sidecar
// image's /usr/local/bin/start.sh, the main workload's app.json), and
// DependsOn for dependency-aware startup.
//
// Both fields are omitempty so the legacy PR-B path (no customer
// override) writes the same byte shape as before — dashboards
// and pre-§6 guest-init binaries that don't recognize cmd still
// parse the manifest byte-for-byte.
//
// JSON field-order pinning: the field declarations are kept in
// alphabetical order so the marshalled byte shape is stable and
// the projected-byte budget (projectedWorkloadManifestBytes) is
// predictable. The companion guest/init/workloadSpec struct in
// guest/init/workload_linux.go mirrors the same order — the
// two structs are a wire pair, and a reorder on one side MUST
// land on the other in the same commit. The round-trip test
// (TestWorkloadManifest_RoundTripsCmdEntry) does NOT pin
// the byte order, only the parsed-equivalence; if a future
// refactor needs the manifest emitted in a different order it
// must be a single PR that updates both sides + the projection
// helper.
type workloadManifest struct {
	Cmd           []string                 `json:"cmd,omitempty"`
	CPUMillicores int                      `json:"cpu_millicores,omitempty"`
	DependsOn     []api.WorkloadDependency `json:"depends_on,omitempty"`
	Entrypoint    []string                 `json:"entrypoint,omitempty"`
	Essential     bool                     `json:"essential"`
	Name          string                   `json:"name"`
	Port          int                      `json:"port"`
	RamMB         int                      `json:"ram_mb"`
	Type          string                   `json:"type"`
}

// workloadRosterPath is the in-guest location guest-init reads
// to discover the deployment-level roster (issue #463 / ADR-069 /
// PR-B). vmmd writes this file once on drive1 at wake time;
// guest-init reads it after assembleOverlay + pivot_root. Lives at
// a sibling path of workloadManifestPath (which is per-drive).
// The orchestrator (guest/init/workload_linux.go) reads the roster,
// not the per-drive manifest; the per-drive stamp remains as a
// reverse-compat / operator-visibility affordance.
const workloadRosterPath = "upper/etc/faas/workloads.json"

// workloadRoster is the on-disk shape of /etc/faas/workloads.json
// (issue #463 / ADR-069 / PR-B). The Main field carries the main
// workload's spec; Sidecars carries the per-sidecar array. Mirrors
// guest/init/workload_linux.go::workloadRoster exactly (a rename
// here requires a parallel rename in the guest-init shape).
type workloadRoster struct {
	Main     workloadManifest   `json:"main"`
	Sidecars []workloadManifest `json:"sidecars"`
}

// StageWorkloadRoster (issue #463 / ADR-069 / PR-B) writes the
// deployment-level roster at /etc/faas/workloads.json on drive1
// (the main workload's drive). The orchestrator reads this file
// after pivot_root to discover the main workload's spec + the
// per-sidecar array, so the file MUST land on drive1 before
// guest-init can route through runWorkloads. The legacy
// single-workload path (no roster) is a guest-init fallback — boot
// routes to runAppWithEnv unchanged.
//
// We mount drive1 once, write both the roster file and verify the
// per-drive main manifest is in place (StageWorkloadManifest
// stamps drive1 too with the main spec; that's the operator-visibility
// affordance for debugging tools, the orchestrator ignores it).
//
// sidecars may be nil/empty — boot runs the legacy path. Caller
// filters out the main workload before passing.
func (v *JailerVMM) StageWorkloadRoster(instance string, main WorkloadSpec, sidecars []WorkloadSpec) error {
	drive1, err := v.resolveDriveImage(instance)
	if err != nil {
		return err
	}
	mp, err := os.MkdirTemp("", "faas-vmm-roster-")
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()
	if out, err := exec.Command("mount", "-o", "loop,rw", drive1, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	// Pre-marshal byte cap projection (PR-B review finding #7).
	// Cap runs BEFORE json.Marshal — matches the posture
	// writeWorkloadManifest adopts. The roster is at most 1
	// main + SidecarCapMax (2) sidecars, so the projection
	// multiplies per-workload projections by len(sidecars)+1.
	if projected := projectedWorkloadRosterBytes(main, sidecars); projected > api.MaxExportedLayerBytes {
		return fmt.Errorf("workload roster projected %d bytes exceeds cap %d (sidecars=%d)", projected, api.MaxExportedLayerBytes, len(sidecars))
	}

	roster := workloadRoster{
		Main: workloadManifest{
			Name:          main.Name,
			Type:          main.Type,
			RamMB:         main.RamMB,
			CPUMillicores: main.CPUMillicores,
			Port:          main.Port,
			Essential:     main.Essential,
			DependsOn:     main.DependsOn,
		},
	}
	for _, sc := range sidecars {
		roster.Sidecars = append(roster.Sidecars, workloadManifest{
			Name:          sc.Name,
			Type:          sc.Type,
			RamMB:         sc.RamMB,
			CPUMillicores: sc.CPUMillicores,
			Port:          sc.Port,
			Essential:     sc.Essential,
			Cmd:           sc.Cmd,
			Entrypoint:    sc.Entrypoint,
			DependsOn:     sc.DependsOn,
		})
	}
	blob, err := json.Marshal(roster)
	if err != nil {
		return fmt.Errorf("marshal workload roster: %w", err)
	}
	target, err := stagedDrivePath(mp, workloadRosterPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir etc/faas: %w", err)
	}
	if err := os.WriteFile(target, blob, 0o400); err != nil {
		return fmt.Errorf("write workloads.json: %w", err)
	}
	return nil
}

// exportMax resolves the per-export byte cap. Zero means "unset" — fall back
// to api.MaxExportedLayerBytes. We read via a tiny helper so tests can
// inject a tighter cap.
func (v *JailerVMM) exportMax() int64 {
	if v.exportMaxBytes > 0 {
		return v.exportMaxBytes
	}
	return api.MaxExportedLayerBytes
}

func (v *JailerVMM) destroyWaitFor(exportDir string, buildTimeoutSec int) time.Duration {
	wait := v.destroyWait
	if exportDir != "" {
		// Builder guest-init starts its own configured build timeout clock after
		// boot, source extraction, entropy seeding, and BuildKit readiness.
		// Keep enough host-side headroom for that clock plus artifact export.
		// The guest build timeout is intentionally distinct from this host
		// budget. Export mounts the large scratch ext4 read-only after the
		// guest powers off, and loopback setup can spend minutes flushing
		// dirty backing-file pages before it can read build-done.json.
		if buildTimeoutSec <= 0 {
			buildTimeoutSec = api.BuildTimeoutSeconds
		}
		builderMinimum := time.Duration(buildTimeoutSec+600) * time.Second
		if wait < builderMinimum {
			wait = builderMinimum
		}
	}
	return wait
}

// copyTree copies a directory tree from src to dst, skipping any single file
// whose size exceeds maxBytes. Best-effort by design — partial copies are OK
// for a build that overshot the cap.
func copyTree(src, dst string, maxBytes int64) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		}
		if d.Type()&os.ModeSymlink != 0 {
			linkName, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkName, target)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if maxBytes > 0 && info.Size() > maxBytes {
			return nil // skip the oversize file
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		return os.Chmod(target, info.Mode().Perm())
	})
}

// --- helpers ---------------------------------------------------------------

func (v *JailerVMM) mkChroot(instance string) (string, error) {
	root := v.chrootRoot(instance)
	// Wipe any leftover state from a prior failed Boot/Restore — jailer's
	// chroot-creation step (mknod /dev/net/tun, mkdir -p /dev/net, etc.)
	// is NOT idempotent and panics with EEXIST on a half-built chroot.
	// RemoveAll on a non-existent path is a no-op, so this is safe for
	// the common case too.
	//
	// Concurrency contract: caller must hold the per-instance Lease (and
	// therefore the unique-while-live invariant on `instance`) for the
	// duration of Boot/Restore. The only race surface is a retry-after-
	// failure that fires before the prior call's defer-cleanup ran; in
	// that window the second RemoveAll nukes the first's freshly-built
	// chroot mid-boot. Boot/Restore's deferred Kill on failure makes
	// this self-correcting on the next retry. If we ever call Boot/
	// Restore from a path that does NOT go through Lease uniqueness,
	// gate this with v.mu (held for the whole Boot/Restore).
	if err := os.RemoveAll(root); err != nil {
		return "", fmt.Errorf("vmm: wipe stale chroot: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("vmm: mkdir chroot: %w", err)
	}
	for dir := root; dir != v.chrootBase && dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		_ = os.Chmod(dir, 0o755)
	}
	return root, nil
}

// startJailer launches jailer→firecracker for the instance with any extra
// firecracker args appended, and records the process.
//
// Issue #254 / Move 4: the previously-discarded cmd.Stdout (which carries
// firecracker's own stdout, including kernel printk and any userland
// process writing to /dev/console) is now wired into a per-instance
// logbuf.Ring. The ring is registered in v.rings by Boot/Restore BEFORE
// this call so the lookup here is always non-nil. Firecracker writes its
// own stderr only on configuration errors; that stream remains discarded
// to avoid mixing error noise into the customer's log tail.
func (v *JailerVMM) startJailer(_ context.Context, l Lease, extraFCArgs ...string) error {
	execFile, err := exec.LookPath(FirecrackerBin)
	if err != nil {
		return fmt.Errorf("vmm: locate firecracker binary: %w", err)
	}
	// Pass the resolved real binary so jailer's chroot basename matches v.fcName
	// (jailer follows the symlink); see resolveFCChrootName.
	if real, rErr := filepath.EvalSymlinks(execFile); rErr == nil {
		execFile = real
	}
	argv := append(JailerCommand(JailerSpec{
		Instance: l.Instance, UID: l.UID, GID: l.GID, Netns: l.Netns, ExecFile: execFile,
		Plan:      l.Plan,
		IsBuilder: l.IsBuilder,
		MemoryMaxBytes: func() int64 {
			if l.MemoryMaxMiB < 1 {
				return 0
			}
			memoryMB := api.BillableRAMMB(l.MemoryMaxMiB)
			if l.IsBuilder {
				memoryMB = api.BuilderMemoryMaxMB(l.MemoryMaxMiB)
			}
			return int64(memoryMB) << 20
		}(),
	}), extraFCArgs...)
	// The caller's gRPC context often ends as soon as the boot RPC returns.
	// Jailer/firecracker must remain alive until the explicit Destroy/Kill path
	// tears it down, otherwise a successful builder boot is killed immediately.
	cmd := exec.Command(argv[0], argv[1:]...)
	ring := v.ringFor(l.Instance)
	consolePath := filepath.Join("/var/log/faas", "vm-"+l.Instance+".console")
	var consoleFile *os.File
	if f, openErr := os.OpenFile(consolePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640); openErr == nil {
		consoleFile = f
	}
	stdout := io.Discard
	if ring != nil {
		// Stream FC's stdout (kernel printk + guest /dev/console writers)
		// into the per-instance ring. stderr stays discarded — FC only
		// writes there on configuration errors and operators inspect those
		// via systemctl logs, not the per-app tail.
		stdout = &ringWriter{ring: ring, stream: "stdout"}
	}
	if consoleFile != nil {
		stdout = io.MultiWriter(stdout, consoleFile)
	}
	cmd.Stdout = stdout
	if consoleFile != nil {
		cmd.Stderr = consoleFile
	} else {
		cmd.Stderr = io.Discard
	}
	if err := cmd.Start(); err != nil {
		if consoleFile != nil {
			_ = consoleFile.Close()
		}
		return fmt.Errorf("vmm: start jailer: %w", err)
	}
	v.mu.Lock()
	v.proc[l.Instance] = cmd
	rec := &instanceRecord{cmd: cmd, consolePath: consolePath, isBuilder: l.IsBuilder, done: make(chan struct{})}
	v.recs[l.Instance] = rec
	v.mu.Unlock()
	// Watchdog: cmd.Wait must be called exactly once per process (stdlib
	// contract). Run it here so DestroyWithExport can later read the captured
	// exit code without racing the actual process termination.
	go func() {
		state, _ := cmd.Process.Wait()
		if consoleFile != nil {
			_ = consoleFile.Close()
		}
		exitCode := 0
		if state != nil {
			exitCode = state.ExitCode()
		}
		var sink func(string, int)
		v.mu.Lock()
		rec.exitCode = exitCode
		// Remove the process from the liveness source of truth as
		// soon as Wait completes. Destroy/Kill remain responsible
		// for record/chroot cleanup.
		if current, ok := v.proc[l.Instance]; ok && current == cmd {
			delete(v.proc, l.Instance)
			if !rec.isBuilder {
				sink = v.processExitSink
			}
		}
		rec.exited = true
		close(rec.done)
		v.mu.Unlock()
		if sink != nil {
			sink(l.Instance, exitCode)
		}
	}()
	return nil
}

// provision stages the kernel and rootfs images into the chroot for the jailer
// uid and returns a copy of cfg with paths rewritten to the chroot-relative
// basenames. Read-only images (kernel, drive0 base) are hard-linked in and widened
// for read; the writable drive (drive1, the overlay upper) is copied to a private
// per-instance file owned by the uid — see stageReadOnly / stageWritable.
func (v *JailerVMM) provision(root string, cfg VMConfig, uid, gid int, instance ...string) (VMConfig, error) {
	out := cfg
	instanceID := ""
	if len(instance) > 0 {
		instanceID = instance[0]
	}
	kname, err := v.stageReadOnlyAs(root, cfg.BootSource.KernelImagePath,
		stableReadOnlyName(cfg.BootSource.KernelImagePath, kernelImageName), instanceID)
	if err != nil {
		return out, err
	}
	out.BootSource.KernelImagePath = kname
	out.Drives = make([]Drive, len(cfg.Drives))
	for i, d := range cfg.Drives {
		var name string
		var err error
		if d.IsReadOnly {
			name = filepath.Base(d.PathOnHost)
			if i == 0 {
				name = stableReadOnlyName(d.PathOnHost, baseImageName)
			}
			if i > 0 && strings.HasPrefix(d.DriveID, DriveSidecarPrefix) {
				name = sidecarDriveImageName(i - 1)
			}
			name, err = v.stageReadOnlyAs(root, d.PathOnHost, name, instanceID)
		} else if cfg.EphemeralWritable {
			name, err = v.stageEphemeralWritableAs(root, d.PathOnHost, layerImageName, uid, gid, instanceID)
		} else {
			name, err = v.stageWritableAs(root, d.PathOnHost, layerImageName, uid, gid, instanceID)
		}
		if err != nil {
			return out, err
		}
		d.PathOnHost = name
		out.Drives[i] = d
	}
	return out, nil
}

// stageEphemeralWritableAs prefers a hardlink for a builder scratch image.
// Production keeps the jail chroot on tmpfs and the builder drive on the
// builder filesystem, so EXDEV is expected there. Copying a 28 GiB sparse
// image into tmpfs would charge the bytes to vmmd's supervisor cgroup and
// either exhaust RAM or hit MemoryMax; bind-mounting preserves the disk-backed
// sparse image and the per-build isolation contract.
func (v *JailerVMM) stageEphemeralWritableAs(root, src, name string, uid, gid int, instance string) (string, error) {
	dst := filepath.Join(root, name)
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stage ephemeral writable %s: %w", src, err)
	}
	if err := os.Link(src, dst); err == nil {
		if err := os.Chmod(dst, 0o600); err != nil {
			_ = os.Remove(dst)
			return "", fmt.Errorf("chmod ephemeral writable %s: %w", src, err)
		}
		if err := chownJail(dst, uid, gid); err != nil {
			_ = os.Remove(dst)
			return "", err
		}
		return name, nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return "", fmt.Errorf("link ephemeral writable %s: %w", src, err)
	}
	return v.bindImage(root, src, name, instance, 0o006, false)
}

// stageReadOnlyFor hardlinks a shared read-only image when possible and
// bind-mounts it when the source lives on a different filesystem. The latter
// is the normal OCI/cache path on compute nodes: the jail is tmpfs, while
// runner/base/kernel images live on disk. Copying those images into tmpfs
// would consume vmmd's small supervisor cgroup.
func (v *JailerVMM) stageReadOnlyFor(root, src, instance string) (string, error) {
	return v.stageReadOnlyAs(root, src, filepath.Base(src), instance)
}

func (v *JailerVMM) stageReadOnlyAs(root, src, name, instance string) (string, error) {
	dst := filepath.Join(root, name)
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stage read-only %s: %w", src, err)
	}
	if err := os.Link(src, dst); err == nil {
		if err := ensureOtherReadable(dst); err != nil {
			return "", err
		}
		return name, nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return "", fmt.Errorf("link read-only %s: %w", src, err)
	}
	return v.bindImage(root, src, name, instance, 0o044, true)
}

// bindImage exposes a source image inside the jail without copying it into
// the tmpfs chroot. addPerms is temporarily applied to the source so the
// jailer uid can open it; the original mode is restored once all VMs using
// that source have been torn down.
func (v *JailerVMM) bindImage(root, src, name, instance string, addPerms os.FileMode, readOnly bool) (string, error) {
	if instance == "" {
		return "", fmt.Errorf("bind image %s: empty instance", src)
	}
	fi, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("stat bind image %s: %w", src, err)
	}
	mode := fi.Mode().Perm()
	v.mu.Lock()
	if state, ok := v.bindSourceModes[src]; ok {
		state.refs++
		v.bindSourceModes[src] = state
	} else {
		if err := os.Chmod(src, mode|addPerms); err != nil {
			v.mu.Unlock()
			return "", fmt.Errorf("chmod bind image %s: %w", src, err)
		}
		v.bindSourceModes[src] = bindSourceMode{mode: mode, refs: 1}
	}
	v.mu.Unlock()

	dst := filepath.Join(root, name)
	f, createErr := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o666)
	if createErr != nil {
		v.releaseBindSource(src)
		return "", fmt.Errorf("create bind target %s: %w", dst, createErr)
	}
	_ = f.Close()
	if output, mountErr := bindFileMount(src, dst); mountErr != nil {
		v.releaseBindSource(src)
		_ = os.Remove(dst)
		return "", fmt.Errorf("bind image %s: %w (%s)", src, mountErr, strings.TrimSpace(string(output)))
	}
	if readOnly {
		if output, remountErr := makeFileMountReadOnly(dst); remountErr != nil {
			_ = exec.Command("umount", dst).Run()
			v.releaseBindSource(src)
			_ = os.Remove(dst)
			return "", fmt.Errorf("remount read-only image %s: %w (%s)", src, remountErr, strings.TrimSpace(string(output)))
		}
	}
	v.mu.Lock()
	v.bindMounts[instance] = append(v.bindMounts[instance], ephemeralBind{source: src, mountpoint: dst, mode: mode})
	v.mu.Unlock()
	return name, nil
}

func (v *JailerVMM) releaseBindSource(src string) {
	v.mu.Lock()
	state, ok := v.bindSourceModes[src]
	if !ok {
		v.mu.Unlock()
		return
	}
	state.refs--
	if state.refs > 0 {
		v.bindSourceModes[src] = state
		v.mu.Unlock()
		return
	}
	delete(v.bindSourceModes, src)
	v.mu.Unlock()
	_ = os.Chmod(src, state.mode)
}

// prepareConfigFIFO creates the one-shot config handoff used during a cold
// boot. Firecracker blocks opening the FIFO until vmmd has entered jailer's
// private mount namespace and repaired /dev/net/tun.
func (v *JailerVMM) stageMountHelper(root string) error {
	shared, err := v.ensureMountHelper()
	if err != nil {
		return err
	}
	dst := filepath.Join(root, "faas-mount-helper")
	_ = os.Remove(dst)
	if err := os.Link(shared, dst); err == nil {
		return nil
	}
	// Defensive fallback for non-production tests/configurations where the
	// chroot base and instance root do not share a filesystem.
	if err := copyFile(shared, dst); err != nil {
		return fmt.Errorf("vmm: stage mount helper: %w", err)
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		return fmt.Errorf("vmm: chmod mount helper: %w", err)
	}
	return nil
}

func (v *JailerVMM) ensureMountHelper() (string, error) {
	v.mountHelperMu.Lock()
	defer v.mountHelperMu.Unlock()
	if v.mountHelperPath != "" {
		if info, err := os.Stat(v.mountHelperPath); err == nil && info.Mode().IsRegular() {
			return v.mountHelperPath, nil
		}
		v.mountHelperPath = ""
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("vmm: locate mount helper: %w", err)
	}
	exe, err = resolveMountHelper(exe)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(v.chrootBase, 0o700); err != nil {
		return "", fmt.Errorf("vmm: create mount helper root: %w", err)
	}
	shared := filepath.Join(v.chrootBase, ".faas-mount-helper")
	tmp, err := os.CreateTemp(v.chrootBase, ".faas-mount-helper-*")
	if err != nil {
		return "", fmt.Errorf("vmm: create shared mount helper: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("vmm: close shared mount helper: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }()
	if err := copyFile(exe, tmpPath); err != nil {
		return "", fmt.Errorf("vmm: copy shared mount helper: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return "", fmt.Errorf("vmm: chmod shared mount helper: %w", err)
	}
	if err := os.Rename(tmpPath, shared); err != nil {
		return "", fmt.Errorf("vmm: publish shared mount helper: %w", err)
	}
	v.mountHelperPath = shared
	return shared, nil
}

// bindTunSource carries the real host TUN device into the jail before
// jailer pivots its root. The non-special path avoids jailer's unconditional
// mknod(/dev/net/tun), while the later helper can bind this source over that
// synthetic node from inside the private mount namespace.
func (v *JailerVMM) bindTunSource(root, instance string) error {
	const source = "/dev/net/tun"
	target := filepath.Join(root, "faas-host-tun")
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("vmm: create TUN source target: %w", err)
	}
	_ = f.Close()
	if output, err := bindFileMount(source, target); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("vmm: bind TUN source: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	v.mu.Lock()
	v.bindMounts[instance] = append(v.bindMounts[instance], ephemeralBind{source: source, mountpoint: target})
	v.mu.Unlock()
	return nil
}

func prepareConfigFIFO(path string, uid, gid int) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale config: %w", err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return fmt.Errorf("mkfifo: %w", err)
	}
	if err := chownJail(path, uid, gid); err != nil {
		return fmt.Errorf("chown config FIFO: %w", err)
	}
	return nil
}

// writeConfigFIFO releases a Firecracker cold boot after the mount namespace
// has been repaired. O_NONBLOCK avoids wedging vmmd if jailer exits early.
func writeConfigFIFO(ctx context.Context, path string, body []byte) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_, writeErr := f.Write(body)
			_ = f.Close()
			return writeErr
		}
		if !errors.Is(err, syscall.ENXIO) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("config reader did not open FIFO")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// bindTunDeviceInJailer repairs jailer's private /dev tree and makes the host
// TUN device visible inside it. The chroot lives on a nodev filesystem, so
// Jailer-created device nodes look correct in stat(2) but return EACCES from
// open(2). A private device-capable tmpfs fixes KVM; the real host TUN remains
// a bind mount because this kernel rejects a synthetic TUN node. The config
// FIFO keeps Firecracker paused until both repairs are complete.
func (v *JailerVMM) bindTunDeviceInJailer(root, instance string, uid, gid int) error {
	if instance == "" {
		return fmt.Errorf("vmm: bind TUN device: empty instance")
	}
	const source = "/dev/net/tun"
	fi, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("vmm: stat TUN device: %w", err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("vmm: TUN path %s is not a character device", source)
	}
	if fi.Mode().Perm()&0o006 != 0o006 {
		return fmt.Errorf("vmm: TUN device %s must be accessible to jailer users (mode %04o)", source, fi.Mode().Perm())
	}
	pid, ok := v.InstancePID(instance)
	if !ok {
		return fmt.Errorf("vmm: bind TUN device: jailer process is not alive")
	}
	selfNS, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return fmt.Errorf("vmm: read vmmd mount namespace: %w", err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		childNS, readErr := os.Readlink(fmt.Sprintf("/proc/%d/ns/mnt", pid))
		if readErr == nil && childNS != selfNS {
			break
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("vmm: jailer did not create a private mount namespace")
		case <-time.After(1 * time.Millisecond):
		}
	}
	// Single-pass setup: prepare /dev tmpfs, bind TUN, and mknod KVM in one nsenter invocation.
	if _, err := exec.Command("nsenter", "-t", strconv.Itoa(pid), "-m", "-r", "--", "/faas-mount-helper", "--setup-jail", "/dev", "/faas-host-tun", "/dev/net/tun", "/dev/kvm", strconv.Itoa(uid), strconv.Itoa(gid)).CombinedOutput(); err != nil {
		// Fallback to legacy 3-step sequence if the mounted helper doesn't support --setup-jail yet
		if outDev, errDev := exec.Command("nsenter", "-t", strconv.Itoa(pid), "-m", "-r", "--", "/faas-mount-helper", "--mount-dev", "/dev").CombinedOutput(); errDev != nil {
			return fmt.Errorf("vmm: prepare jail device tree: %w (%s)", errDev, strings.TrimSpace(string(outDev)))
		}
		if outTun, errTun := exec.Command("nsenter", "-t", strconv.Itoa(pid), "-m", "-r", "--", "/faas-mount-helper", "--mount-bind", "/faas-host-tun", source).CombinedOutput(); errTun != nil {
			return fmt.Errorf("vmm: bind TUN device: %w (%s)", errTun, strings.TrimSpace(string(outTun)))
		}
		if outKvm, errKvm := exec.Command("nsenter", "-t", strconv.Itoa(pid), "-m", "-r", "--", "/faas-mount-helper", "--mknod-kvm", "/dev/kvm", strconv.Itoa(uid), strconv.Itoa(gid)).CombinedOutput(); errKvm != nil {
			return fmt.Errorf("vmm: provision KVM device: %w (%s)", errKvm, strings.TrimSpace(string(outKvm)))
		}
	}
	_ = os.Remove(filepath.Join(root, "faas-mount-helper"))
	v.mu.Lock()
	v.bindMounts[instance] = append(v.bindMounts[instance], ephemeralBind{source: source, mountpoint: filepath.Join(root, "dev", "net", "tun")})
	v.mu.Unlock()
	return nil
}

// unmountBindMounts releases image bind mounts before the jail chroot is
// removed and restores source modes for the owning storage/build daemon.
func (v *JailerVMM) unmountBindMounts(instance string) {
	v.mu.Lock()
	binds := v.bindMounts[instance]
	delete(v.bindMounts, instance)
	v.mu.Unlock()
	for i := len(binds) - 1; i >= 0; i-- {
		b := binds[i]
		_ = exec.Command("umount", b.mountpoint).Run()
		v.releaseBindSource(b.source)
		_ = os.Remove(b.mountpoint)
	}
}

// ownChrootRoot hands the chroot root directory to the jailer uid so the jailed
// firecracker — which chroots into it and then runs unprivileged — can create the
// API socket there and, on Snapshot, write the mem/vmstate files it later exports.
func (v *JailerVMM) ownChrootRoot(root string, l Lease) error {
	parent := filepath.Dir(root)
	_ = os.Chmod(parent, 0o755)
	if err := chownJail(root, l.UID, l.GID); err != nil {
		return fmt.Errorf("vmm: chown chroot root: %w", err)
	}
	return nil
}

// waitReady probes the guest's routable identity for readiness.
//
// Legacy path (healthcheckPath == ""): bare TCP accept on :8080 — the
// pre-PR-D contract. The accept proves the customer app bound *something*
// on the inner port the portnorm ladder re-exposes on :8080 (ADR-009).
//
// PR-D path (healthcheckPath != "", issue #460 / ADR-053 / ADR-057):
// HTTP GET <healthcheckPath> against <HostIP>:8080; 2xx = ready, non-2xx
// or err = retry until readyTimeout (default 30s). The host probe target
// is always :8080 — ADR-009 + portnorm re-expose the customer bind on
// :8080 inside the guest, so the path is the customer's choice and the
// port is the host's choice. Wake must always work (ADR-005): a
// transient customer-app 500 must not wedge a wake, so we retry instead
// of fast-failing. 200ms backoff matches the legacy TCP cadence.
//
// The HTTP client is a per-VMM cached instance with a 2s per-probe
// timeout (bounded by the readyTimeout deadline). On a successful 2xx
// the body is discarded immediately — the probe is "alive enough to
// answer", not "shape-conformant".
//
// Error-explanations cluster (spec §6.4 amendment 1): when the
// deadline expires, the returned error is a typed *api.Problem
// carrying CodeAppNotListening (or CodeAppStartupTimeout for the
// HTTP-probe case) so schedd's engine can pass it through to
// gatewayd-internal unchanged and the CLI renderer pulls hint/why/
// fix from pkg/whycopy without re-classifying. The TCP probe that
// consistently hits ECONNREFUSED (the kernel-side "no listener")
// is the canonical app_not_listening detection; the HTTP probe that
// never returns 2xx is app_startup_timeout (the app may be up but
// the healthcheck is wrong — distinct failure).
func readyTimeoutFor(defaultTimeout time.Duration, startupDeadlineS ...int) time.Duration {
	if len(startupDeadlineS) > 0 && startupDeadlineS[0] > 0 {
		return time.Duration(startupDeadlineS[0]) * time.Second
	}
	return defaultTimeout
}

func (v *JailerVMM) waitReady(ctx context.Context, l Lease, healthcheckPath string, startupDeadlineS ...int) error {
	readyTimeout := readyTimeoutFor(v.readyTimeout, startupDeadlineS...)
	deadline := time.Now().Add(readyTimeout)
	addr := net.JoinHostPort(l.HostIP.String(), "8080")
	// issue #517 / PR-C / ADR-064 — stamp the readiness probe start
	// so the wake.readiness_200 emit can carry the elapsed_ms
	// field. Per-VMM, not per-call (the deadline is the same); the
	// loop resets to the deadline at the top of every iteration.
	v.readinessStartedAt = time.Now()

	// Legacy TCP-accept — pre-PR-D contract. Byte-identical to the
	// pre-PR-D loop.
	if healthcheckPath == "" {
		// Track ECONNREFUSED specifically across the loop — a
		// sustained ECONNREFUSED is the kernel's "no listener"
		// shibboleth for app_not_listening. Other transient
		// errors (timeout, EHOSTUNREACH) collapse to the generic
		// "not ready" message so we don't misclassify a slow
		// boot as a misconfigured listener.
		connRefusedCount := 0
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				v.emitReadiness200(ctx, l, healthcheckPath, 1)
				return nil
			}
			// ECONNREFUSED on TCP dial = nothing is listening on
			// the port. Counts as the canonical
			// app_not_listening signal. Any other error (timeout,
			// host unreachable) means the network stack itself is
			// the problem — not the absence of a listener.
			if isConnRefusedErr(err) {
				connRefusedCount++
			}
			if time.Now().After(deadline) {
				return v.notReadyProblem(l, healthcheckPath, connRefusedCount, readyTimeout)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// PR-D HTTP GET probe. Reuse the cached client across probes —
	// the 200ms cadence would otherwise allocate a Transport on
	// every iteration. The host loop is bounded by ctx.Done() and
	// the deadline.
	client := v.healthcheckClient()
	probeCount := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return api.NewProblem(422, api.CodeAppStartupTimeout,
				"app did not become ready in time",
				fmt.Sprintf("guest %s not ready (healthcheck %s) after %s", l.Instance, healthcheckPath, readyTimeout))
		}
		probeCount++
		if ok, err := healthcheckProbe(ctx, client, addr, healthcheckPath); err == nil && ok {
			v.emitReadiness200(ctx, l, healthcheckPath, probeCount)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// notReadyProblem shapes the deadline-expired error from the TCP
// probe path. When every probe hit ECONNREFUSED, the kernel is
// telling us nothing is listening — the canonical
// app_not_listening. When a mix of errors occurred (or no probes
// landed), we fall through to app_startup_timeout (the app may be
// booting, but we waited the full window).
//
// The port literal "8080" mirrors waitReady's hard-coded addr —
// ADR-009 fixes the readiness probe on 8080 regardless of the
// deployment's per-app override (the override only affects the
// HTTP GET path on healthcheckPath != ""). The whycopy catalog's
// Observed renderer templates the port into the Why field so the
// customer sees the literal ":8080" in the failure response.
func (v *JailerVMM) notReadyProblem(l Lease, healthcheckPath string, connRefusedCount int, timeout ...time.Duration) *api.Problem {
	readyTimeout := v.readyTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		readyTimeout = timeout[0]
	}
	if connRefusedCount > 0 {
		return api.NewProblem(422, api.CodeAppNotListening,
			"no process listening on $PORT",
			fmt.Sprintf("readiness probe dialed :8080 and got ECONNREFUSED on every attempt (refused_count=%d, deadline=%s, instance=%s)",
				connRefusedCount, readyTimeout, l.Instance))
	}
	return api.NewProblem(422, api.CodeAppStartupTimeout,
		"app did not become ready in time",
		fmt.Sprintf("guest %s not ready after %s (no probes connected)", l.Instance, readyTimeout))
}

// isConnRefusedErr returns true when the dial error is the kernel's
// ECONNREFUSED. The stdlib wraps the underlying syscall errno so we
// match on the literal string — portable across Linux/macOS, no
// platform-specific syscall import needed. Other errors (timeout,
// host unreachable, network unreachable) are deliberately excluded:
// they don't mean "no listener", they mean "the network path is
// broken", and classifying them as app_not_listening would mislead
// the customer into debugging their bind address when the real
// problem is a broken netns.
func isConnRefusedErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "connection refused")
}

// emitReadiness200 (issue #517 / PR-C / ADR-064) is the canonical
// wake.readiness_200 emit. Called once per waitReady on the first
// 2xx probe (or the first successful TCP-accept on the legacy
// path). The wake_id is recovered from the wire envelope on the
// supplied ctx (the schedd engine stamped it on bootCtx before
// calling vmmd, per issue #517 PR-A). The vmmd emit is the
// canonical source of truth for the readiness moment — the
// timeline endpoint joins this row to wake.boot_started (schedd)
// and wake.boot_completed (schedd) under the same wake_id.
func (v *JailerVMM) emitReadiness200(ctx context.Context, l Lease, healthcheckPath string, probeCount int) {
	if v.events == nil {
		return
	}
	var wakeID, appID string
	if fields, ok := wire.FromContext(ctx); ok {
		wakeID = fields.WakeID
		appID = fields.AppID
	}
	now := time.Now()
	elapsed := now.Sub(v.readinessStartedAt)
	v.events.Emit(ctx, events.Readiness200{
		EmitAt:          now.UTC(),
		WakeID:          wakeID,
		AppID:           appID,
		InstanceID:      l.Instance,
		HealthcheckPath: healthcheckPath,
		ProbeCount:      probeCount,
		ElapsedMs:       elapsed.Milliseconds(),
	})
}

// emitRestoreBreakdown writes the vmmd-side restore phases to the same
// wake-timeline stream as readiness_200. Restore is also used by focused
// tests and a few operator paths without a wake envelope, so those calls
// deliberately remain log-only rather than creating an unjoinable event.
func (v *JailerVMM) emitRestoreBreakdown(ctx context.Context, l Lease, at time.Time, b restoreTimingBreakdown) {
	if v.events == nil {
		return
	}
	fields, ok := wire.FromContext(ctx)
	if !ok || fields.WakeID == "" {
		return
	}
	v.events.Emit(ctx, events.RestoreBreakdown{
		EmitAt:               at.UTC(),
		WakeID:               fields.WakeID,
		AppID:                fields.AppID,
		InstanceID:           l.Instance,
		ChrootMs:             b.ChrootMs,
		MaterializeMemMs:     b.MaterializeMemMs,
		MaterializeVMStateMs: b.MaterializeVMStateMs,
		ResolveImagesMs:      b.ResolveImagesMs,
		StageDrivesMs:        b.StageDrivesMs,
		StageSnapshotMs:      b.StageSnapshotMs,
		HelperMs:             b.HelperMs,
		StartJailerMs:        b.StartJailerMs,
		BindTunMs:            b.BindTunMs,
		LoadSnapshotMs:       b.LoadSnapshotMs,
		ResumeHookMs:         b.ResumeHookMs,
		WaitReadyMs:          b.WaitReadyMs,
		TotalMs:              b.TotalMs,
	})
}

// healthcheckProbe issues a single GET against
// `http://<addr><healthcheckPath>` via client. Returns (true, nil)
// on a 2xx response, (false, nil) on any non-2xx, transport error,
// or context cancel — the caller decides what to do with a
// "not-yet-2xx" answer (the waitReady loop treats it as a retry
// trigger). Extracted so unit tests can drive the probe against a
// httptest.Server on an ephemeral port instead of contending for
// the literal `:8080` the production loop pins.
//
// addr must be the output of net.JoinHostPort (host:port, no
// scheme); healthcheckPath must start with `/` (DTO validator
// guarantees this in production).
func healthcheckProbe(ctx context.Context, client *http.Client, addr, healthcheckPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+healthcheckPath, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		// Transport error (connection refused, dial timeout,
		// EOF) — the waitReady loop treats (false, nil) as
		// "retry until deadline". Surfacing err here would
		// abort the loop on the first transient transport
		// hiccup, contradicting ADR-005's "wake must always
		// work" stance (a guest that hasn't bound its port
		// yet looks identical to a guest whose netns blew
		// away mid-probe — both must be retried).
		return false, nil //nolint:nilerr
	}
	// Drain the body (capped) before close so the cached
	// transport's keep-alive can reuse the connection. Without
	// this, every probe opens a fresh TCP socket and TIME_WAIT
	// accumulates under sustained probing. The 64 KiB cap is a
	// safety net — production customers' healthcheck responses
	// are status-only — but bounded drain is required so a
	// misbehaving runner that streams a 1 GB body can't OOM
	// the host.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	return resp.StatusCode/100 == 2, nil
}

// healthcheckClient returns the per-VMM *http.Client used by the
// PR-D waitReady HTTP probe. A single client with a 2s per-probe
// timeout is reused across probes (the 200ms loop cadence would
// otherwise allocate a Transport on every iteration). Nil-safe:
// returns a fresh client when the receiver or the cache is nil.
func (v *JailerVMM) healthcheckClient() *http.Client {
	if v == nil {
		return &http.Client{Timeout: 2 * time.Second}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.hcClient == nil {
		v.hcClient = &http.Client{Timeout: 2 * time.Second}
	}
	return v.hcClient
}

// VsockCharacterizationHostPort is the AF_VSOCK port the host's
// characterization listener accepts on. Must match
// guest/init/characterize_linux.go::VsockCharacterizationPort (1026).
// Distinct from VsockResumePort (1024) and VsockStatelessAdvisoryPort
// (1025) so a host-side prefix collision is impossible.
const VsockCharacterizationHostPort = 1026

// VsockCharacterizationMsgType is the wire-format discriminator for
// a guest→host characterization report. Matches
// guest/init/characterize_linux.go::VsockCharacterizationMsgType (=3).
const VsockCharacterizationMsgType uint32 = 3

// VsockCharacterizationMaxBody caps the JSON body at 128 KiB.
// The typical report is <2 KiB; 128 KiB accommodates a long log_tail
// plus listening_addrs for a polyglot app AND an OpenAPI doc capture
// (ADR-122 §D2). Real-world OpenAPI docs typically run 8-30 KiB;
// complex apps (Stripe-scale: ~700 operations, deep
// components.schemas) exceed the previous 32 KiB cap. 128 KiB leaves
// 4× headroom for path-count inflation. The guest hard-truncates
// BEFORE json.Marshal so the receiver never sees a malformed body,
// and the OpenAPIDocTruncated field on the report surfaces the
// truncation to the customer. Matches the guest's
// VsockCharacterizationMaxBody.
const VsockCharacterizationMaxBody = 128 * 1024

// WaitCharacterizationReport accepts the FIRST guest-initiated
// AF_VSOCK STREAM connection on VsockCharacterizationHostPort and
// reads [4B BE msg_type][4B BE body_len][N B JSON], writes a 1-byte
// ack (0=ok), and returns the parsed report.
//
// Wire direction: GUEST INITIATES — guest dials host CID 2 at
// port 1026 (mirror of VsockResumePort which is host-accept). The
// guest retries with backoff (100/250/500 ms, 3 retries); the host
// must accept any of them. We open the listener ONCE, accept ONE
// connection, and let the deadline elapse if no guest arrives.
//
// Caller (pkg/sched/engine.go in PR-D) gates the RUNNING transition
// on the report arriving. On timeout, the engine falls back to the
// scan-hint class (never fails the deploy — that would regress vs
// today's opaque `:8080` accept failure).
//
// Defense-in-depth mirrors TriggerResumeHook: nil receiver, empty
// instance, unconfigured chroot root are all explicit errors. A
// refactor that passes an uninitialised VMM would otherwise dial a
// malformed UDS path and return a cryptic ENOENT.
func (v *JailerVMM) WaitCharacterizationReport(ctx context.Context, l Lease, deadline time.Duration) (api.CharacterizationReport, error) {
	var zero api.CharacterizationReport
	if v == nil {
		return zero, fmt.Errorf("vmm: WaitCharacterizationReport: nil receiver")
	}
	if l.Instance == "" {
		return zero, fmt.Errorf("vmm: WaitCharacterizationReport: empty instance")
	}
	if v.chrootBase == "" {
		return zero, fmt.Errorf("vmm: WaitCharacterizationReport: chrootBase not configured")
	}
	sock := v.vsockUDSSock(l.Instance)

	dialDeadline := time.Now().Add(deadline)
	var conn net.Conn
	var lastErr error
	for time.Now().Before(dialDeadline) {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		var err error
		conn, err = net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err == nil {
			break
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(resumeHookDialStep):
		}
	}
	if conn == nil {
		return zero, fmt.Errorf("vmm: dial vsock uds %s: %w", sock, lastErr)
	}
	defer func() { _ = conn.Close() }()

	// Step 1: FC CONNECT-port handshake. Same shape as
	// TriggerResumeHook — the guest's listen_resume_linux.go's UDS
	// and the characterization UDS are the same socket; the port
	// arg tells FC which guest-side listener to deliver to.
	connectCmd := fmt.Sprintf("CONNECT %d\n", VsockCharacterizationHostPort)
	if _, err := conn.Write([]byte(connectCmd)); err != nil {
		return zero, fmt.Errorf("vmm: write CONNECT %d: %w", VsockCharacterizationHostPort, err)
	}
	connectAck, err := readConnectAck(conn)
	if err != nil {
		return zero, fmt.Errorf("vmm: read CONNECT ack: %w", err)
	}
	if connectAck != "OK" {
		return zero, fmt.Errorf("vmm: CONNECT rejected: %q", connectAck)
	}

	_ = conn.SetDeadline(time.Now().Add(deadline))

	// Step 2: read the framed JSON. 4-byte BE msg type discriminator +
	// 4-byte BE body length + N bytes JSON. We validate msg_type =
	// VsockCharacterizationMsgType; a wrong type means a misrouted
	// frame (the guest dialed the right port but the wrong listener,
	// or the host reused the UDS for something else).
	var hdr [8]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return zero, fmt.Errorf("vmm: read characterization frame header: %w", err)
	}
	msgType := binary.BigEndian.Uint32(hdr[0:4])
	bodyLen := binary.BigEndian.Uint32(hdr[4:8])
	if msgType != VsockCharacterizationMsgType {
		return zero, fmt.Errorf("vmm: wrong msg_type %d (want %d)", msgType, VsockCharacterizationMsgType)
	}
	if bodyLen == 0 || int(bodyLen) > VsockCharacterizationMaxBody {
		return zero, fmt.Errorf("vmm: characterization body length %d out of range (max %d)", bodyLen, VsockCharacterizationMaxBody)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return zero, fmt.Errorf("vmm: read characterization body: %w", err)
	}

	// Step 3: parse the report. The guest's emit-side encodes via
	// pkg/api.CharacterizationReport (json tags are wire-stable).
	var report api.CharacterizationReport
	if err := json.Unmarshal(body, &report); err != nil {
		return zero, fmt.Errorf("vmm: unmarshal characterization report: %w", err)
	}

	// Step 4: 1-byte ack. 0 = ok. The guest retries on !=0 or
	// short read; we send 0 unconditionally (any failure here would
	// have already returned).
	if _, err := conn.Write([]byte{0}); err != nil {
		// Ack write failure doesn't undo the parsed report; the
		// guest will retry but we already have what we need.
		// Surface as a soft warning via the return tuple: caller
		// (engine.go) decides if it matters.
		return report, fmt.Errorf("vmm: write ack: %w", err)
	}
	return report, nil
}

// fcClient returns an HTTP client bound to the instance's Firecracker API socket.
// Clients are cached per instance because http.Transport's connection pool is
// the expensive part; rebuilding per request would re-resolve the socket every
// time.
func (v *JailerVMM) fcClient(instance string) *http.Client {
	v.mu.Lock()
	defer v.mu.Unlock()
	if c, ok := v.clients[instance]; ok {
		return c
	}
	sock := v.socketPath(instance)
	c := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
	v.clients[instance] = c
	return c
}

// closeClient drops any cached http.Client for instance. Called by Kill so a
// subsequent Boot of the same instance name gets a fresh client (and thus a
// fresh transport pool pointed at the new socket).
func (v *JailerVMM) closeClient(instance string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.clients, instance)
}

func (v *JailerVMM) apiPut(ctx context.Context, instance, path string, body any) error {
	return v.apiCall(ctx, http.MethodPut, instance, path, body)
}

func (v *JailerVMM) apiPatch(ctx context.Context, instance, path string, body any) error {
	return v.apiCall(ctx, http.MethodPatch, instance, path, body)
}

// fcAPIError (issue #470 / PR #470-FU-A) is the typed error
// apiCallWithClient surfaces on a 3xx/4xx/5xx Firecracker API
// response. Carries the HTTP status code (not the body) so callers
// like ResumeVM can branch on status without string-matching
// "409"/"Conflict" substrings that could catch unrelated faults.
// Falls back to apiCall's old formatting when status is 0 (a
// transport-level error before a response was received).
type fcAPIError struct {
	method     string
	path       string
	statusCode int
	statusText string
	body       string
}

func (e *fcAPIError) Error() string {
	if e.statusCode == 0 {
		return fmt.Sprintf("firecracker %s %s: transport error: %s", e.method, e.path, e.body)
	}
	return fmt.Sprintf("firecracker %s %s: %s: %s", e.method, e.path, e.statusText, e.body)
}

func (v *JailerVMM) apiCall(ctx context.Context, method, instance, path string, body any) error {
	return v.apiCallWithClient(ctx, v.fcClient(instance), method, path, body)
}

// apiCallWithClient is the seam that drives a single Firecracker API request.
// Split out from apiCall so tests can inject a client backed by an httptest
// server without needing the unix-socket machinery.
func (v *JailerVMM) apiCallWithClient(ctx context.Context, client *http.Client, method, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// startJailer returns as soon as the jailer process is forked — the
	// Firecracker API socket is created by firecracker itself a few ms
	// later. On a slow nested-KVM guest (Lima arm64) the first POST
	// races the socket creation; retry briefly before giving up so the
	// snapshot-restore path isn't held hostage to the boot timing.
	const maxAttempts = 100
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Each attempt needs a fresh body reader because http.Client.Do
		// consumes the body on send.
		req.Body = io.NopCloser(bytes.NewReader(buf))
		req.ContentLength = int64(len(buf))
		resp, err := client.Do(req)
		if err == nil {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode >= 300 {
				msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				return &fcAPIError{
					method:     method,
					path:       path,
					statusCode: resp.StatusCode,
					statusText: resp.Status,
					body:       string(bytes.TrimSpace(msg)),
				}
			}
			return nil
		}
		lastErr = err
		// Short backoff: 5ms × 20 = 100ms total. The socket appears in
		// single-digit ms on bare metal; nested KVM needs ~50ms.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	return lastErr
}

// stageReadOnly hardlinks a shared read-only source (kernel, drive0 base, or a
// snapshot file) into the chroot and widens its mode so the unprivileged jailer
// uid can read it. These files are shared across instances via hardlink (cheap —
// Appendix B), so we must NOT chown them: that would rewrite the shared inode's
// owner and break every other instance holding the same link. They are non-secret,
// read-only, and visible only inside this instance's chroot, so o+r is safe.
func stageReadOnly(root, src string) (string, error) {
	return stageReadOnlyAs(root, src, filepath.Base(src))
}

func stageReadOnlyAs(root, src, name string) (string, error) {
	name, err := linkIntoAs(root, src, name)
	if err != nil {
		return "", err
	}
	if err := ensureOtherReadable(filepath.Join(root, name)); err != nil {
		return "", err
	}
	return name, nil
}

// stageWritable copies a source image into the chroot as a private, per-instance
// file owned by the jailer uid. drive1 (the overlay upper — guest/init) is opened
// read-write by firecracker, and two instances must never share it (invariant
// §6.2-5), so it is copied — never hard-linked — and chowned to the uid. A hardlink
// would alias the shared source inode and corrupt it under concurrent writers.
func stageWritable(root, src string, uid, gid int) (string, error) {
	return stageWritableAs(root, src, layerImageName, uid, gid)
}

func stageWritableAs(root, src, name string, uid, gid int) (string, error) {
	dst := filepath.Join(root, name)
	// A read-only sibling drive may already have hard-linked this basename in (the
	// M0 fixture points drive0 and drive1 at the same image); drop that link first
	// so the copy below can't truncate the shared source through it.
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stage writable %s: %w", src, err)
	}
	if err := copyFile(src, dst); err != nil {
		return "", fmt.Errorf("copy writable %s: %w", src, err)
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		return "", fmt.Errorf("chmod writable %s: %w", dst, err)
	}
	if err := chownJail(dst, uid, gid); err != nil {
		return "", err
	}
	return name, nil
}

func (v *JailerVMM) stageWritable(root, src string, uid, gid int, instance string) (string, error) {
	return v.stageWritableAs(root, src, layerImageName, uid, gid, instance)
}

// stageWritableAs takes a private CoW clone beside a disk-backed source and
// bind-mounts that clone into the tmpfs jail. On XFS reflink=1 this avoids
// copying the complete application layer into RAM on every restore while
// preserving the non-aliasing invariant for writable drives. Unsupported
// filesystems retain the established copy path.
func (v *JailerVMM) stageWritableAs(root, src, name string, uid, gid int, instance string) (string, error) {
	if instance == "" {
		return stageWritableAs(root, src, name, uid, gid)
	}
	clone, cloned, err := reflinkCloneTemp(src)
	if err != nil {
		return "", fmt.Errorf("reflink writable %s: %w", src, err)
	}
	if !cloned {
		return stageWritableAs(root, src, name, uid, gid)
	}
	if err := os.Chmod(clone, 0o600); err != nil {
		_ = os.Remove(clone)
		return "", fmt.Errorf("chmod writable clone %s: %w", clone, err)
	}
	if err := chownJail(clone, uid, gid); err != nil {
		_ = os.Remove(clone)
		return "", err
	}
	staged, err := v.bindImage(root, clone, name, instance, 0, false)
	if err != nil {
		_ = os.Remove(clone)
		return "", err
	}
	v.trackMaterialised(instance, clone)
	return staged, nil
}

// stageEphemeralWritableAs links a builder scratch image into the jail. The
// source is unique to one build and is removed by builderd after
// DestroyWithExport, so aliasing it is safe and avoids copying the full 24 GiB
// scratch file. This helper is intentionally separate from stageWritableAs:
// app VM layers are persistent/shared storage and must never alias their
// writable source inode.
func stageEphemeralWritableAs(root, src, name string, uid, gid int) (string, error) {
	dst := filepath.Join(root, name)
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stage ephemeral writable %s: %w", src, err)
	}
	if err := os.Link(src, dst); err != nil {
		// Remote storage or a different filesystem cannot be hard-linked;
		// preserve functionality with the normal isolated copy path.
		return stageWritableAs(root, src, name, uid, gid)
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("chmod ephemeral writable %s: %w", src, err)
	}
	if err := chownJail(dst, uid, gid); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return name, nil
}

// ensureOtherReadable widens path's mode to add group+other read if it isn't there
// already. Used for shared read-only chroot files that the unprivileged jailer uid
// (never the owner, never in a matching group) must be able to open.
func ensureOtherReadable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&0o004 == 0 {
		if err := os.Chmod(path, perm|0o044); err != nil {
			return fmt.Errorf("widen readable %s: %w", path, err)
		}
	}
	return nil
}

// chownJail gives path to the jailer uid/gid. Chowning to an arbitrary uid needs
// CAP_CHOWN, i.e. root; vmmd is the only root component (spec §11) and owns all
// jail staging. Off the box the unit suite runs unprivileged, where chowning to a
// 20000+ uid would EPERM, so we skip when not root: those tests never launch a
// real jailed firecracker, and the metal suite runs as root (test-metal /
// metal-lima are sudo).
func chownJail(path string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown jail %s -> %d:%d: %w", path, uid, gid, err)
	}
	return nil
}

// linkInto hardlinks src into dir (falling back to copy on cross-device) and
// returns its basename for chroot-relative reference.
func linkInto(dir, src string) (string, error) {
	name := filepath.Base(src)
	return linkIntoAs(dir, src, name)
}

func linkIntoAs(dir, src, name string) (string, error) {
	dst := filepath.Join(dir, name)
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err != nil {
		if cErr := copyFile(src, dst); cErr != nil {
			return "", fmt.Errorf("link/copy %s: %w", src, cErr)
		}
	}
	return name, nil
}

// moveOut moves src to dst (across filesystems if needed) and returns the size.
func moveOut(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return 0, err
	}
	if err := os.Rename(src, dst); err != nil {
		if cErr := copyFile(src, dst); cErr != nil {
			return 0, cErr
		}
		_ = os.Remove(src)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// materializeFromStorage pulls the bytes for key via the configured
// StorageBackend and writes them into a fresh tmp file. Returns the
// absolute path the caller should substitute into MemPath. The tmp
// path is registered against instanceID so Kill / DestroyWithExport
// Remove it during teardown; without the registration the file
// would outlive the chroot (which lives on tmpfs) and leak across
// thousands of wakes on a busy box. When storage is nil or key is
// empty, the helper is a no-op and reports the keys were unused —
// Restore proceeds on MemPath unchanged.
//
// #96 / ADR-025 axis 2 — single seam that lets the local driver satisfy
// the call from /srv/fc/snap and a future remote driver from a registry.
// The chroot staging layer never has to learn about storage.
func (v *JailerVMM) materializeFromStorage(ctx context.Context, instanceID, key string) (string, error) {
	if key == "" {
		return "", nil // nil layer key: cold boot without layer, skip staging
	}
	// Absolute paths (builderd's layer at /var/lib/faas/build-drive/…,
	// or any direct host path) bypass storage. validateKey rejects
	// keys starting with '/', so we detect them here and return as-is.
	// The caller treats the returned path as a host file to stage.
	if filepath.IsAbs(key) {
		return key, nil
	}
	if v.storage == nil {
		return key, nil // nil storage: key IS the direct host path (builderd path-through mode)
	}
	rc, err := v.storage.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("vmm: storage get %q: %w", key, err)
	}
	defer func() { _ = rc.Close() }()
	tmp, err := os.CreateTemp("", "faas-snap-*.bin")
	if err != nil {
		return "", fmt.Errorf("vmm: create tmp for %q: %w", key, err)
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("vmm: copy %q: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("vmm: close tmp for %q: %w", key, err)
	}
	v.trackMaterialised(instanceID, tmpPath)
	return tmpPath, nil
}

// restoreSourceFromStorage resolves a restore input without copying when the
// configured backend already has a local path. Snapshot restore is latency
// sensitive and local snapshots can be gigabytes; routing them through Get
// would turn a hardlink into a full disk-to-disk copy. Remote/OCI backends do
// not implement LocalPathResolver and retain the streaming materialization
// path.
func (v *JailerVMM) restoreSourceFromStorage(ctx context.Context, instanceID, key string) (string, error) {
	if key == "" || filepath.IsAbs(key) || v.storage == nil {
		return v.materializeFromStorage(ctx, instanceID, key)
	}
	if resolver, ok := v.storage.(storage.LocalPathResolver); ok {
		if path, local, err := resolver.LocalPath(key); err != nil {
			return "", err
		} else if local {
			// Keep the cold-boot path observable while storage backends are
			// being migrated between local and OCI routing. The key is
			// canonical and the resolved path contains no customer payload.
			slog.Default().Info("vmm: resolved local storage path", "key", key, "path", path)
			return path, nil
		}
	}
	return v.materializeFromStorage(ctx, instanceID, key)
}

// trackMaterialised records tmpPath against instanceID so Kill /
// DestroyWithExport can Remove it during teardown. Per-instance
// lifecycle: registered on materialize, cleared on the next Kill.
func (v *JailerVMM) trackMaterialised(instanceID, tmpPath string) {
	if instanceID == "" || tmpPath == "" {
		return
	}
	v.mu.Lock()
	v.materialisedTmp[instanceID] = append(v.materialisedTmp[instanceID], tmpPath)
	v.mu.Unlock()
}

// sweepMaterialised Removes every tmp path tracked against instanceID
// and clears the slot. Best-effort: a missing tmp file is not an error;
// anything else is logged so a leak is observable but never blocks the
// chroot teardown.
func (v *JailerVMM) sweepMaterialised(instanceID string) {
	v.mu.Lock()
	paths := v.materialisedTmp[instanceID]
	delete(v.materialisedTmp, instanceID)
	v.mu.Unlock()
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Default().Warn("vmm: remove materialised tmp",
				"path", p, "instance", instanceID, "err", err)
		}
	}
}

const ficloneIoctl = 0x40049409

// reflinkCloneTemp creates a private CoW clone in src's directory. The bool is
// false when the filesystem does not support FICLONE; callers then use their
// portable copy path. Keeping the clone beside src is what guarantees both
// files are on the same reflink-capable filesystem.
func reflinkCloneTemp(src string) (path string, cloned bool, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", false, err
	}
	defer func() {
		if closeErr := in.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	out, err := os.CreateTemp(filepath.Dir(src), ".faas-layer-*")
	if err != nil {
		return "", false, err
	}
	tmpPath := out.Name()
	path = tmpPath
	defer func() {
		if closeErr := out.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if !cloned || err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, out.Fd(), ficloneIoctl, in.Fd()); errno != 0 {
		return "", false, nil
	}
	return path, true, nil
}

//nolint:forbidigo // src/dst are vetted slot/instance-id paths under /srv/fc — vmmd is the sole writer of this directory; the tmpfs jail root means symlink-attack would require root (which vmmd already has, by spec §11). Copy is an internal migration helper, not a customer-path surface.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := in.Close(); cErr != nil && err == nil {
			err = cErr
		}
	}()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := out.Close(); cErr != nil && err == nil {
			err = cErr
		}
	}()
	// Fast path: attempt copy-on-write clone (FICLONE ioctl, ~0.05ms on XFS/Btrfs/reflink-ext4).
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, out.Fd(), ficloneIoctl, in.Fd()); errno == 0 {
		return nil
	}
	_, err = io.Copy(out, in)
	return err
}

// traceparentFromContext renders the W3C traceparent header (issue #555
// PR-4) from the active SpanContext on ctx. Returns an empty string
// when no span is active (legacy single-box without OTel, or pre-Open
// Telemetry tests). The empty string is shipped over vsock and the
// guest-resume hook no-ops on it (the runner's TRACEPARENT env is
// simply unset).
//
// Format: 32-hex trace_id + "-" + 16-hex span_id + "-" + 2-hex flags.
// The flags byte is "01" (sampled) — the guest-side OTel SDK inherits
// the sampling decision from the parent trace.
func traceparentFromContext(ctx context.Context) string {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return fmt.Sprintf("%s-%s-%02x",
		sc.TraceID().String(),
		sc.SpanID().String(),
		uint8(sc.TraceFlags()),
	)
}
