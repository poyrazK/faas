//go:build linux

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// Guest device layout (spec §4.6): the normal two-drive path uses drive0
// (vda) as the shared read-only base and drive1 (vdb) as the per-app writable
// layer. Full-rootfs artifacts also use drive1 for wire compatibility, but
// carry a trusted marker and are pivoted into directly without the shared-base
// overlay.
const (
	layerDevice        = "/dev/vdb"
	layerMount         = "/overlay"
	newRoot            = "/overlay/merged"
	builderPATH        = "/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	railpackMiseSource = "/usr/local/lib/faas/mise/mise-2026.7.6"
	railpackMiseTarget = "/tmp/railpack/mise/mise-2026.7.6"
)

// bootMode is which branch of the build (BuildManifest present) vs app
// (AppManifest present) guest-init took. decideMode is split out so unit
// tests can drive it with testing/fstest.MapFS.
type bootMode int

const (
	modeApp bootMode = iota
	modeBuild
	modeJob
)

// main is guest PID 1. Any fatal error here panics the VM (panic=1 in boot args
// reboots it), which schedd observes as a failed wake.
func main() {
	if err := boot(); err != nil {
		fmt.Fprintf(os.Stderr, "guest-init: %v\n", err)
		os.Exit(1)
	}
}

func boot() error {
	guestStage("boot")
	if err := mountBasics(); err != nil {
		return fmt.Errorf("mount basics: %w", err)
	}
	guestStage("mount-basics")
	root, err := assembleOverlay()
	if err != nil {
		return fmt.Errorf("assemble overlay: %w", err)
	}
	guestStage("assemble-overlay")
	if err := pivotInto(root); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	guestStage("pivot")
	mode, buildManifest, err := decideMode(os.DirFS("/"))
	if err != nil {
		return err
	}
	guestStage(fmt.Sprintf("mode-%d", mode))
	// Job VMs (issue #1184 Workstream A / ADR-099) are
	// single-shot: load /etc/faas/job.json, exec the customer's
	// command, ship the vsock DGRAM, poweroff. No readiness
	// probe, no listener port, no cgroup2 (the host-side
	// per-instance scope from vmmd is still enforced — same
	// contract as every other VM). runJob is total: it powers
	// off on every path, so we never reach the cgroup2 /
	// resume-hook bindings below.
	if mode == modeJob {
		guestStage("before-job-supervisor")
		_ = RunJob(slog.Default())
		// Unreachable: RunJob poweroff()s the VM on every path.
		return nil
	}
	// Builder VMs run BuildKit/runc as their only workload. The daemon needs a
	// cgroup mount, but its runc workers enter a user namespace so they do not
	// require host-level device-BPF policy support. The guest cgroup namespace
	// is private to this VM; the host-side vmmd cgroup fence remains the
	// authoritative outer limit.
	if mode == modeBuild {
		guestStage("before-builder-cgroup")
		if err := mountCgroup2(); err != nil {
			return writeAndPoweroff(buildManifest, fmt.Errorf("builder cgroup2 mount: %w", err), "")
		}
		guestStage("after-builder-cgroup")
		return runBuild(buildManifest)
	}

	// Issue #463 / ADR-069 / PR-B AC #4: mount cgroup2
	// inside the guest AFTER pivot_root so the mount lives
	// on the new root. Tolerant: a guest kernel without
	// CONFIG_CGROUP_V2=y fails the mount silently and the
	// per-workload partition is skipped (the host-side
	// per-instance scope from vmmd is still enforced).
	if err := mountCgroup2(); err != nil {
		slog.Default().Warn("cgroup2 mount failed", "err", err)
	}

	// ADR-022: bind the AF_VSOCK resume listener BEFORE the supervisor starts
	// the app, so a post-restore dial from vmmd can never race the listener
	// coming up. We tolerate a bind failure (e.g. AF_VSOCK not compiled into
	// the guest kernel) on cold boot — fresh kernel entropy doesn't need a
	// resume hook. On restore, vmmd's TriggerResumeHook will then time out
	// dial-resume and fail closed (per spec §11 V6).
	if err := listenResumeHook(slog.Default()); err != nil {
		slog.Default().Warn("vsock resume listener unavailable", "err", err)
	}

	// Issue #554 / ADR-078: bind the liveness probe listener on
	// vsock 1028 STREAM alongside the resume hook. Same direction
	// (host→guest), same wire envelope (4B msg-type + 4B body-len
	// + JSON body), different port. Like the resume hook we
	// tolerate a bind failure (e.g. AF_VSOCK not compiled into
	// the guest kernel) on cold boot — a working VM without
	// liveness still cold-boots; vmmd's poll goroutine will then
	// fail dial and the host's per-instance liveness receiver
	// logs at Debug, falling back to the §13 idle reaper.
	if err := listenLivenessHook(slog.Default()); err != nil {
		slog.Default().Warn("vsock liveness listener unavailable", "err", err)
	}

	// Wave 0 PR-C / ADR-047: stateless-runtime advisory. FAN_CLASS_NOTIF
	// on a closed set of state-shaped paths; debounced batches shipped
	// over AF_VSOCK DGRAM (port=1025, msg_type=2) to the host. Returns
	// are tolerated — a guest without CONFIG_FANOTIFY=y still boots,
	// the contract is "no signal" not "won't boot".
	if err := runStatelessAdvisory(slog.Default()); err != nil {
		slog.Default().Warn("stateless advisory unavailable", "err", err)
	}

	// PR #470-FU-B (issue #470): the framework-ready unix-socket
	// proxy. The proxy at /run/guest-init/framework-ready.sock is
	// the runner-facing entry point — runners connect to it with
	// one line ("<runtime> <warmup_ms>") and the proxy frames the
	// vsock DGRAM (port 1027, msg_type 4) payload and forwards to
	// the host. The proxy must start BEFORE the supervisor starts
	// the runners so the first request can't race the proxy coming
	// up. Soft-fail: bind errors log at Warn and the platform
	// contract is "no warm-capture signal" not "won't boot" — the
	// engine's warm-capture wait in PR #470-FU-A times out and
	// falls through to init-tier.
	if err := startFrameworkReadyProxy(slog.Default()); err != nil {
		slog.Default().Warn("framework_ready proxy unavailable", "err", err)
	}

	// Issue #667 / ADR-078 (consolidated follow-up PR): the
	// tail-events unix-socket proxy. The proxy at
	// /run/guest-init/tail-events.sock is the runner-facing entry
	// point for waitUntil(promise) terminal events — the runner's
	// tail host (guest/runners/internal/tail_host.go) connects to
	// it with one line ("<outcome_byte> <elapsed_ms>") and the
	// proxy frames the 16-byte vsock DGRAM (port 1027, msg_type
	// 0x04) payload and forwards to the host. The proxy must
	// start BEFORE the supervisor starts the runners so the first
	// tail terminal can't race the proxy coming up. Soft-fail:
	// bind errors log at Warn and the platform contract is "no
	// telemetry" not "won't boot" — the snapshotAndPark 5s
	// watchdog on schedd handles a missing tail receipt.
	if err := startTailEventsProxy(slog.Default()); err != nil {
		slog.Default().Warn("tail_events proxy unavailable", "err", err)
	}

	// Issue #463 / ADR-069 / ADR-071 / PR-C §3,§4: the sidecar
	// events proxy. Outbound-only vsock DGRAM on the same port
	// (1027) as framework_ready; the leading type byte
	// disambiguates the event class (0x01 = framework_ready,
	// 0x02 = sidecar_init_exit, 0x03 = sidecar_restart). The
	// proxy is held by runWorkloads so the orchestrator can emit
	// init_ok / init_failed on the init sidecar exit paths and
	// surface restart events from supervisor.OnCrash. Soft-fail:
	// bind errors log at Warn and the contract is "no signal"
	// not "won't boot" — the supervisor's restart policy remains
	// the source of truth for "did the deploy succeed".
	sidecarProxy, sidecarErr := startSidecarEventsProxy(slog.Default())
	if sidecarErr != nil {
		slog.Default().Warn("sidecar events proxy unavailable", "err", sidecarErr)
	}

	//nolint:forbidigo // api.AppManifestPath is a compile-time constant defined in pkg/api (/etc/faas/app.json) — the manifest is written into the guest's rootfs by the builder before boot, the customer never writes it. Inside the microVM, the path is not customer-spoofable.
	f, err := os.Open(api.AppManifestPath)
	if err != nil {
		return fmt.Errorf("open manifest: %w", err)
	}
	manifest, err := api.ReadManifest(f)
	_ = f.Close()
	if err != nil {
		return err
	}

	// G2: read /etc/faas/secrets.env (unsealed JSON, written by vmmd at
	// wake time) and stash the entry count on the supervisor via a small
	// closure so runAppWithEnv can pull them. A missing or malformed file is
	// not fatal — the app runs without env secrets (consistent with
	// the quota=0 path).
	secrets, secErr := loadSecrets(slog.Default())
	if secErr != nil {
		// Don't surface the value, just the fact that the file failed.
		slog.Default().Warn("secrets.env could not be loaded; proceeding without secrets", "err_kind", errorKind(secErr))
	}

	// Issue #395 / ADR-045: read /etc/faas/env.json (plaintext JSON
	// map, written by vmmd at wake time) and thread it into the
	// 4-layer precedence BuildEnvWithSecrets enforces. A missing or
	// malformed file is not fatal — the app runs without API env
	// (consistent with the secrets layer above). The path is
	// siblings-of secrets, deliberately separate so a JSON-decode
	// failure on one doesn't propagate to the other.
	apiEnv, apiErr := loadAPIEnv(slog.Default())
	if apiErr != nil {
		slog.Default().Warn("env.json could not be loaded; proceeding without api env", "err_kind", errorKind(apiErr))
	}

	// Issue #463 / ADR-069 / PR-B: discover the workload roster
	// (deployment-level main + sidecars). A missing
	// /etc/faas/workloads.json is the legacy single-workload path
	// — boot falls through to the existing runAppWithEnv-driven
	// Supervisor and the characterize probe. A present file
	// hands off to runWorkloads which runs init sidecars
	// sequentially, then main + type="sidecar" workloads in
	// parallel under per-workload Supervisors.
	roster, rosterErr := discoverRoster(os.DirFS("/"))
	if rosterErr == nil && len(roster.Sidecars) > 0 {
		return runWorkloads(manifest, roster, secrets, apiEnv, slog.Default(), sidecarProxy)
	}
	// Roster absent or empty Sidecars = legacy path. Log the
	// roster error if it was a parse failure (the legacy
	// "file missing" case is silent — most pre-PR-B VMs have
	// no roster file).
	if rosterErr != nil && !isNotExist(rosterErr) {
		slog.Default().Warn("workloads.json could not be parsed; proceeding without sidecars", "err_kind", errorKind(rosterErr))
	}

	// ADR-051 Phase 4: Supervisor holds atomic.Pointer (Run is a
	// pointer receiver, see supervise.go). We assign the struct
	// fields one at a time so the Start closure can refer to
	// `supRef` — referencing `&sup` from inside its own struct
	// literal would trip Go's "undefined: sup" before the
	// assignment. The wiring below is the canonical fix.
	policy, maxRestarts := supervisorPolicyFromManifest(manifest)
	supRef := &Supervisor{Max: maxRestarts, Policy: policy}
	supRef.Start = func() error { return runAppWithEnv(manifest, secrets, apiEnv, supRef) }
	supRef.OnCrash = func(attempt int, err error) {
		fmt.Fprintf(os.Stderr, "guest-init: app restart (restart %d/%d policy=%s): %v\n", attempt, maxRestarts, policy, err)
	}
	// ADR-051 Phase 4: characterize the workload by observing the
	// first cold boot. Runs in parallel with sup.Run() so the
	// PID-fd walk finds the customer's listener without blocking
	// the boot. The host (pkg/fcvm/vmm.go::WaitCharacterization
	// Report in PR-D) gates the RUNNING transition on the report
	// arriving; a timeout here falls back to scan-hint class (never
	// fails the deploy — that's worse than today's opaque "guest
	// not ready after 30s" path).
	go runCharacterizationForSup(supRef, manifest)
	// M-2 / //code-review PR #1202 finding #7: a single boot-scoped
	// cancellable context is shared between the HEALTHCHECK poll
	// goroutine and the PID 1 signal-handler loop so both observe
	// the same shutdown boundary. The previous shape passed
	// context.Background() to both — the poll goroutine's DGRAM
	// socket stayed open after the supervisor exited and the
	// signal-handler loop returned, leaking fd + goroutine + a
	// stuck ring-buffer drain across guest-init restarts. The
	// cancel() is wired into the runSignalHandlers return path so
	// when the supervisor finishes (clean exit, crash-loop, or
	// graceful stop), both subsystems unwind together.
	bootCtx, bootCancel := context.WithCancel(context.Background())
	defer bootCancel()
	// M-2 / ADR-139 §Decision 1: HEALTHCHECK poll goroutine.
	// Soft-fail on bind (e.g. guest kernel without AF_VSOCK) —
	// the engine's existing :8080 TCP-accept probe continues to
	// gate readiness, so the customer doesn't lose the boot.
	if err := runHealthcheckPoll(bootCtx, manifest, slog.Default()); err != nil {
		slog.Default().Warn("healthcheck poll unavailable", "err", err)
	}
	// M-2 / ADR-138 §Decision 1 / issue #474 — install the PID 1
	// signal handler before invoking the supervisor. The handler
	// multiplexes (a) the customer's STOPSIGNAL forwarded to the
	// supervisor for graceful stop, (b) the SIGCHLD reaper loop
	// so every forked child is reaped (no zombies), and (c)
	// forwarding of guest-init-received signals to the tracked
	// workload. Returns when the supervisor exits (clean, crash-
	// loop exhausted, or graceful-stop completed).
	return runSignalHandlers(bootCtx, manifest, supRef, slog.Default())
}

func guestStage(stage string) {
	_, _ = fmt.Fprintf(os.Stderr, "guest-init: stage %s\n", stage)
}

// runAppWithEnv is the secrets+apiEnv-aware entrypoint — same execve
// path as runApp but with both layers merged over the manifest env.
// Empty/nil secrets AND empty/nil apiEnv short-circuits to the bare
// BuildEnv path; nil in one of them short-circuits to the other layer's
// 3-arg shape via BuildEnvWithSecrets's nil-tolerant map reads.
func runAppWithEnv(m api.AppManifest, secrets, apiEnv map[string]string, sup *Supervisor) error {
	return runAppWithRAM(m, secrets, apiEnv, sup, 0)
}

// runAppWithRAM is the workload-aware variant of runAppWithEnv. ramMB is
// supplied by the workload roster when present; zero preserves the legacy
// single-workload path, whose host-side cgroup is the authoritative cap.
func runAppWithRAM(m api.AppManifest, secrets, apiEnv map[string]string, sup *Supervisor, ramMB int) error {
	argv := m.Entrypoint
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.EffectiveWorkingDir()
	env := BuildEnvWithSecrets(os.Environ(), m, secrets, apiEnv)
	// Issue #460 / ADR-053 (PR-C): stamp PORT=<m.EffectivePort()>
	// onto the exec'd env so the runner shim can bind the
	// per-deployment override port. Appended AFTER
	// BuildEnvWithSecrets so a customer-set PORT in manifest env,
	// apiEnv, or sealed secrets cannot accidentally override the
	// platform contract — guest-init is the canonical source of
	// truth for the override port. Unconditional: m.Port == 0
	// still injects PORT=8080, which is harmless for runners that
	// already bind :8080 and matches the vmmd wire-level default.
	// StampOverridePortEnv is the pure helper the unit test pins;
	// keeping the live edit here means the precedence assertion
	// tests the exact code path the production execve uses.
	env = StampOverridePortEnv(env, m.EffectivePort())
	// Issue #555 PR-4: stamp TRACEPARENT onto the runner env. The
	// W3C trace context was shipped from the host via the vsock
	// resume hook; the supervisor reads it via GetResumeTraceparent
	// at Start() time. Empty = no OTel configured, the env is
	// unchanged.
	env = StampTraceparentEnv(env, GetResumeTraceparent())
	cmd.Env = env
	// ADR-051 Phase 4 Slice A PR-B: tee the customer's stdout/stderr
	// into the supervisor's ring buffer so the characterize probe can
	// populate the report's LogTail field. The MultiWriter preserves
	// the live console stream (os.Stdout) so operators watching
	// journalctl -u faas-vmmd still see the boot log in real time.
	// When sup is nil (unit tests that exercise runAppWithEnv directly
	// without a supervisor), we fall back to the legacy bare stdout
	// wiring — those tests don't read LogTail.
	if sup != nil {
		mw := io.MultiWriter(os.Stdout, sup.LogBuffer())
		cmd.Stdout, cmd.Stderr = mw, mw
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	if uid := lookupUID(m.EffectiveUser()); uid > 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)},
		}
	}
	// ADR-051 Phase 4: expose the forked cmd to the supervisor so
	// runCharacterizationForSup can read the PID via LastAppPID().
	// The supervisor's Run() loop captures the cmd at every
	// restart; runAppWithEnv executes once per restart.
	if sup != nil {
		sup.TrackCommand(cmd)
	}
	// Issue #463 / ADR-069 / PR-B AC #4: per-workload
	// in-guest cgroup v2 partition for the main workload.
	// mkdir + write memory.max BEFORE Start. The main
	// workload gets a "main-app" leaf so its OOM is
	// scoped separately from any sidecar's. Legacy
	// single-workload wakes have no per-workload RAM value
	// and therefore skip this child leaf; the host-side
	// writePlanCgroup remains their authoritative cap.
	mainLeaf, cgroupErr := prepareWorkloadCgroup("main", "app", ramMB, slog.Default())
	if cgroupErr != nil {
		return fmt.Errorf("prepare main workload cgroup: %w", cgroupErr)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("run %v: %w", argv, err)
	}
	// Place the forked child into the leaf. Same race
	// posture as runSidecar — see placeIntoLeaf's doc.
	if mainLeaf != "" {
		placeIntoLeaf(mainLeaf, cmd.Process.Pid, slog.Default())
	}
	// Cluster C / ADR-121: spawn the per-workload cgroup.events
	// oom_kill listener (guest/init/cgroup_partition_linux.go::
	// WatchOOM) for the duration of the main workload's lifetime.
	// The listener exits on first fire (the workload is dead
	// when an oom_kill lands) and on the context cancel that
	// runAppWithEnv inherits from the boot path. This is the
	// runtime-VM path — the build VM uses a different
	// classification surface (classify(exitCode=137) →
	// FailureOOM) and does not need this listener.
	//
	if mainLeaf != "" {
		oomCtx, oomCancel := context.WithCancel(context.Background())
		go func() {
			defer oomCancel()
			werr := WatchOOM(oomCtx, mainLeaf, ramMB,
				func(peakMB, pMB int) {
					if eerr := EmitWorkloadOOM(oomCtx, peakMB, pMB); eerr != nil {
						slog.Default().Warn("EmitWorkloadOOM failed",
							"peak_mb", peakMB, "plan_mb", pMB, "err", eerr)
					}
				}, slog.Default())
			if werr != nil && !errors.Is(werr, context.Canceled) {
				slog.Default().Debug("WatchOOM listener returned",
					"err", werr, "leaf", mainLeaf)
			}
		}()
		// Cancel the OOM listener when the workload exits
		// (graceful or crash). The cmd.Wait() return is the
		// lifecycle signal; the listener exits within one
		// 1s poll tick on next wake.
		defer func() { oomCancel() }()
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("run %v: %w", argv, err)
	}
	return nil
}

// errorKind collapses an error chain to a stable string suitable for a
// slog attribute. We never log the raw err (it could carry a malformed
// secrets file path or partial bytes), only its structural class.
func errorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fs.ErrPermission):
		return "permission"
	case isNotExist(err):
		return "absent"
	default:
		// JSON unmarshal failure or read failure other than ENOENT.
		if strings.HasPrefix(err.Error(), "secrets: parse") {
			return "parse"
		}
		if strings.HasPrefix(err.Error(), "secrets: read") {
			return "read"
		}
		return "other"
	}
}

// decideMode picks the boot branch by looking at which manifest file exists.
//
// Mode priority (issue #1184 / ADR-099):
//  1. job.json with kind=="job"  → modeJob
//  2. build.json (kind=="build") → modeBuild
//  3. else                       → modeApp (legacy)
//
// Job VMs never co-exist with build.json (different workload
// class), so the precedence is "what file wins". If both
// somehow appear (vmmd staging bug), job wins — the customer's
// command is the authoritative intent.
//
// Split out from boot() so unit tests can drive it with
// testing/fstest.MapFS instead of touching the real root fs.
// The path passed to fs.ReadFile must be RELATIVE (no leading "/") —
// fs.FS rejects absolute paths, and the real os.DirFS("/") used
// at boot happily accepts the relative form on Linux.
func decideMode(fsys fs.FS) (bootMode, api.BuildManifest, error) {
	// Job VMs take precedence (issue #1184 Workstream A /
	// ADR-099). The presence of /etc/faas/job.json with
	// kind=="job" is the canonical signal — the supervisor
	// reads the rest of the manifest (command, env, task
	// timeout, lease token) at runJob time, not here.
	if hasJobManifest(fsys) {
		return modeJob, api.BuildManifest{}, nil
	}
	if data, err := fs.ReadFile(fsys, "etc/faas/build.json"); err == nil {
		var m api.BuildManifest
		if jErr := json.Unmarshal(data, &m); jErr == nil {
			return modeBuild, m, nil
		}
	}
	return modeApp, api.BuildManifest{}, nil
}

// ensureBuilderResolver makes the builder guest independent of container
// runtime setup. OCI-to-ext4 conversion does not carry Docker's injected
// /etc/resolv.conf, and Alpine images may omit it entirely. If a resolver is
// already present, preserve the operator-provided configuration; otherwise
// install the same public fallback used by the Debian builder image.
func ensureBuilderResolver() error {
	return ensureResolverFile("/etc/resolv.conf")
}

func ensureResolverFile(path string) error {
	if data, err := os.ReadFile(path); err == nil && hasNameserver(data) {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale resolver file: %w", err)
	}
	const fallback = "# Gregale builder VM resolver\nnameserver 1.1.1.1\nnameserver 1.0.0.1\n"
	if err := os.WriteFile(path, []byte(fallback), 0o644); err != nil {
		return fmt.Errorf("write resolver file: %w", err)
	}
	return nil
}

func hasNameserver(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "nameserver ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "nameserver ")) != ""
		}
	}
	return false
}

// ensureRailpackMise seeds Railpack's tmpfs-backed cache with the musl mise
// binary shipped in the builder image. Railpack 0.31.1 downloads the glibc
// linux-x64 asset by default, which cannot execute in the Alpine rootfs.
func ensureRailpackMise() error {
	return stageExecutable(railpackMiseSource, railpackMiseTarget)
}

// builderShellCandidates follows builderPATH's lookup order. A real bash
// binary is required here: mise's python-build plugin invokes scripts with a
// #!/usr/bin/env bash shebang, and BusyBox sh is not a compatible substitute.
var builderShellCandidates = []string{
	"/usr/local/bin/bash",
	"/usr/sbin/bash",
	"/usr/bin/bash",
	"/sbin/bash",
	"/bin/bash",
}

// ensureBuilderShell verifies that the builder image provides a functioning
// bash executable. Do not synthesize /bin/bash -> /bin/sh: when /bin/sh is
// BusyBox, invoking it through the name "bash" makes BusyBox look for a bash
// applet and fail with the misleading "bash: applet not found" error.
func ensureBuilderShell() error {
	return validateBuilderShell(builderShellCandidates, builderEnv())
}

func validateBuilderShell(candidates, env []string) error {
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat %s: %w", candidate, err)
		}
		// BASH_VERSION is a Bash-only variable. A BusyBox shell reached through
		// a bash-named symlink may accept `-c` but is not a valid interpreter
		// for mise's Bash scripts.
		cmd := exec.Command(candidate, "-c", `test -n "${BASH_VERSION:-}"`)
		cmd.Env = env
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("builder shell %s is not a functional bash: %w (%s)", candidate, err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	return errors.New("builder image does not contain a functional bash executable; install the bash package")
}

func stageExecutable(source, target string) error {
	in, err := os.Open(source) //nolint:forbidigo // source is the builder image's vetted mise path.
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".mise-*")
	if err != nil {
		return fmt.Errorf("create target: %w", err)
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close binary: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	keep = true
	return nil
}

// runBuild is the builder-VM path (M6). It extracts the source tarball,
// invokes the chosen build engine (Railpack / buildctl / auto), writes
// build-done.json with the outcome, and powers off. poweroff is what makes
// firecracker exit cleanly with the build's exit code (vmmd's
// DestroyResponse.exit_code on the wire — see pkg/vmmdgrpc/server.go).
func runBuild(m api.BuildManifest) error {
	if m.BuildContext == "" {
		m.BuildContext = "/build/src"
	}
	if m.Workdir == "" {
		m.Workdir = "/build/src"
	}
	if m.OutDir == "" {
		m.OutDir = "/build/out"
	}
	if err := os.MkdirAll(m.BuildContext, 0o755); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mkdir build context: %w", err), "")
	}
	if err := os.MkdirAll(m.Workdir, 0o755); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mkdir workdir: %w", err), "")
	}
	if err := os.MkdirAll(m.OutDir, 0o755); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mkdir outdir: %w", err), "")
	}
	// BuildKit is launched rootless inside a user namespace. The build
	// workspace is disposable VM-local state, and OCI-to-ext4 materialisation
	// can preserve an image owner that does not match the mapped worker uid.
	// Make the exact build paths writable before starting the rootless daemon;
	// without this, BuildKit fails before it can create its worker state.
	for _, dir := range []string{"/build", m.BuildContext, m.Workdir, m.OutDir} {
		if err := os.Chmod(dir, 0o777); err != nil {
			return writeAndPoweroff(m, fmt.Errorf("chmod build workspace %s: %w", dir, err), "")
		}
	}
	if err := seedBuildEntropy(); err != nil {
		return writeAndPoweroff(m, err, "")
	}
	isDockerfile := m.Framework == api.FrameworkDockerfile
	if !isDockerfile {
		if err := os.MkdirAll(filepath.Join(m.OutDir, "rootfs"), 0o755); err != nil {
			return writeAndPoweroff(m, fmt.Errorf("mkdir railpack rootfs: %w", err), "")
		}
	}

	// 1. Extract source tarball. guest-init has no login-shell PATH.
	if m.SourceTarPath != "" {
		if out, err := exec.Command("/bin/tar", "-xaf", m.SourceTarPath, "-C", m.BuildContext).CombinedOutput(); err != nil {
			return writeAndPoweroff(m, fmt.Errorf("tar extract: %w (%s)", err, out), "")
		}
		// The legacy packer wraps a self-contained source directory in one
		// transport directory. Only flatten when the build context and
		// working directory are the same; a workspace build must preserve
		// repository-relative siblings below BuildContext.
		if m.BuildContext == m.Workdir {
			if err := flattenSingleSourceDir(m.BuildContext); err != nil {
				return writeAndPoweroff(m, fmt.Errorf("normalize source root: %w", err), "")
			}
		}
		// Prepare the entire repository context, including sibling workspace
		// packages, without erasing executable bits or following symlinks.
		if err := prepareBuildSourceModes(m.BuildContext); err != nil {
			return writeAndPoweroff(m, fmt.Errorf("prepare source permissions: %w", err), "")
		}
	}

	// 2. Start BuildKit inside the builder VM. Railpack is a BuildKit
	// frontend, as is the Dockerfile path, so both build modes need the
	// daemon. The VM is already the isolation boundary and the enclosing
	// unshare below is the user/mount boundary; keep the executor in the
	// guest's network namespace because the minimal image has no CNI setup.
	if err := os.MkdirAll("/run/buildkit", 0o755); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mkdir buildkit run dir: %w", err), "")
	}
	// The merged root is an overlayfs view whose writable upper is the same
	// builder drive. BuildKit keeps a BoltDB, lock, and runc state tree under
	// its root; those operations are more restrictive than a simple touch and
	// can be rejected by overlayfs from the nested user namespace. Mount the
	// builder drive directly and keep only this disposable state on ext4.
	builderDriveMount := "/run/faas-builder-drive"
	if err := os.MkdirAll(builderDriveMount, 0o755); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mkdir builder drive mount: %w", err), "")
	}
	if err := syscall.Mount(layerDevice, builderDriveMount, "ext4", 0, ""); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mount builder drive scratch: %w", err), "")
	}
	for _, dir := range []string{builderDriveMount, builderDriveMount + "/upper", builderDriveMount + "/upper/build"} {
		if err := os.Chmod(dir, 0o777); err != nil {
			return writeAndPoweroff(m, fmt.Errorf("chmod builder scratch %s: %w", dir, err), "")
		}
	}
	// BuildKit is launched rootless in a separate user+mount namespace. Its
	// persistent worker root is on the direct ext4 scratch mount above, avoiding
	// the overlayfs lock/state failure while retaining rootless runc's guest-safe
	// mount policy. The VM plus this outer namespace remain the isolation
	// boundary.
	buildkitRoot := builderDriveMount + "/upper/build/.buildkit"
	if err := os.MkdirAll(buildkitRoot, 0o777); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("mkdir buildkit root: %w", err), "")
	}
	if err := os.Chmod(buildkitRoot, 0o777); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("chmod buildkit root: %w", err), "")
	}
	guestStage("builder-workspace")
	if diagnostic, err := probeBuilderWorkspace(buildkitRoot); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("builder workspace preflight: %w", err), diagnostic)
	}
	guestStage("workspace-probe")
	if err := ensureFuseDevice(); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("fuse device: %w", err), "")
	}
	guestStage("fuse")
	if err := ensureBuilderResolver(); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("builder resolver: %w", err), "")
	}
	guestStage("resolver")
	if err := ensureRailpackMise(); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("railpack mise: %w", err), "")
	}
	if err := ensureBuilderShell(); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("builder shell: %w", err), "")
	}
	guestStage("mise")
	// Railpack resolves language toolchains and package registries from inside
	// the builder VM. Fail fast when the guest's DNS path is broken instead of
	// spending the whole build budget in a registry client retry loop.
	dnsCtx, dnsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	dnsCheck := exec.CommandContext(dnsCtx, "/usr/bin/getent", "hosts", "registry.npmjs.org")
	dnsOut, dnsErr := dnsCheck.CombinedOutput()
	dnsCancel()
	if dnsErr != nil {
		return writeAndPoweroff(m, fmt.Errorf("registry DNS preflight: %w", dnsErr), string(dnsOut))
	}
	if m.Framework == api.FrameworkRailpackNode || m.Framework == api.FrameworkAuto {
		// A fresh Firecracker guest may need a few seconds for its first
		// routed TLS connection even after DNS is available. Keep this as a
		// fail-fast guard, but leave enough room for the initial handshake.
		httpCtx, httpCancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, reqErr := http.NewRequestWithContext(httpCtx, http.MethodGet, "https://nodejs.org/dist/index.json", nil)
		var httpErr error
		if reqErr == nil {
			resp, doErr := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if doErr != nil {
				httpErr = doErr
			} else {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode >= http.StatusBadRequest {
					httpErr = fmt.Errorf("status %s", resp.Status)
				}
			}
		} else {
			httpErr = reqErr
		}
		httpCancel()
		if httpErr != nil {
			return writeAndPoweroff(m, fmt.Errorf("node toolchain HTTPS preflight: %w", httpErr), "")
		}
	}
	// Railpack's generated plan pulls its builder/runtime layers from GHCR;
	// a registry challenge is healthy here and proves the guest can reach the
	// endpoint that BuildKit will subsequently authenticate against.
	ghcrCtx, ghcrCancel := context.WithTimeout(context.Background(), 5*time.Second)
	ghcrReq, ghcrReqErr := http.NewRequestWithContext(ghcrCtx, http.MethodGet, "https://ghcr.io/v2/", nil)
	var ghcrErr error
	if ghcrReqErr == nil {
		resp, doErr := (&http.Client{Timeout: 5 * time.Second}).Do(ghcrReq)
		if doErr != nil {
			ghcrErr = doErr
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= http.StatusInternalServerError {
				ghcrErr = fmt.Errorf("status %s", resp.Status)
			}
		}
	} else {
		ghcrErr = ghcrReqErr
	}
	ghcrCancel()
	if ghcrErr != nil {
		return writeAndPoweroff(m, fmt.Errorf("GHCR HTTPS preflight: %w", ghcrErr), "")
	}
	guestStage("network-preflight")
	runcCheck := exec.Command("/usr/local/bin/runc", "--version")
	runcCheck.Env = builderEnv()
	if runcOut, runcErr := runcCheck.CombinedOutput(); runcErr != nil {
		return writeAndPoweroff(m, fmt.Errorf("runc preflight: %w (%s)", runcErr, runcOut), string(runcOut))
	}
	if err := os.MkdirAll("/run/runc", 0o755); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("runc root: %w", err), "")
	}
	runcList := exec.Command("/usr/local/bin/runc", "--root", "/run/runc", "list")
	runcList.Env = builderEnv()
	if runcOut, runcErr := runcList.CombinedOutput(); runcErr != nil {
		return writeAndPoweroff(m, fmt.Errorf("runc list preflight: %w (%s)", runcErr, runcOut), string(runcOut))
	}
	guestStage("runc-preflight")

	var buildkitLog bytes.Buffer
	bk := exec.Command(
		"/usr/bin/unshare",
		rootlessBuildkitUnshareArgs(
			"/usr/local/bin/buildkitd",
			"--debug",
			"--rootless",
			"--root", buildkitRoot,
			"--addr", "unix:///run/buildkit/buildkitd.sock",
			"--oci-worker-binary", "/usr/local/bin/runc",
			// The builder drive is deliberately 28 GiB. The image includes
			// fuse-overlayfs and the guest kernel has FUSE built in; use the
			// rootless COW snapshotter so committing a layer does not scan/copy the
			// complete native snapshot tree after every RUN instruction.
			"--oci-worker-snapshotter", "fuse-overlayfs",
			"--oci-worker-net", "host",
		)...,
	)
	// Keep the daemon and its rootless worker tree in a private process group so
	// the timeout path can terminate BuildKit descendants before powering off.
	bk.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// BuildKit uses both the user namespace probe and USER when selecting its
	// rootless defaults. guest-init is PID 1 and can inherit a stale USER value
	// from the host-side launch environment, so make the mapped namespace root
	// unambiguous while keeping the worker's rootless flag explicit above.
	bk.Env = builderEnv("USER=root", "HOME=/root")
	// Keep the full daemon trace in the durable build marker while mirroring
	// it to the VM console. A rootless solve can spend minutes in an image
	// fetch/unpack step; without the console mirror the host only sees a hot
	// Firecracker process and cannot distinguish progress from a deadlock.
	bk.Stdout = io.MultiWriter(os.Stdout, &buildkitLog)
	bk.Stderr = io.MultiWriter(os.Stderr, &buildkitLog)
	if err := bk.Start(); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("start buildkitd: %w", err), tailOf(buildkitLog.Bytes(), m.LogTailBytes))
	}
	guestStage("buildkit-started")
	defer func() {
		if bk.Process == nil {
			return
		}
		_ = syscall.Kill(-bk.Process.Pid, syscall.SIGKILL)
		_ = bk.Process.Kill()
	}()
	for i := 0; i < 25; i++ {
		if _, err := os.Stat("/run/buildkit/buildkitd.sock"); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := os.Stat("/run/buildkit/buildkitd.sock"); err != nil {
		return writeAndPoweroff(m, fmt.Errorf("buildkitd socket never appeared: %w", err), tailOf(buildkitLog.Bytes(), m.LogTailBytes))
	}
	guestStage("buildkit-socket")
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 60*time.Second)
	check := exec.CommandContext(checkCtx, "/usr/local/bin/buildctl", "debug", "workers")
	check.Env = builderEnv("BUILDKIT_HOST=unix:///run/buildkit/buildkitd.sock")
	workerOut, workerErr := check.CombinedOutput()
	checkCancel()
	if workerErr != nil {
		diagnostic := append(append([]byte(nil), buildkitLog.Bytes()...), []byte(workerErr.Error()+"\n")...)
		diagnostic = append(diagnostic, workerOut...)
		// A worker can fail after the socket is created but before the gRPC
		// readiness call observes it. Wait briefly so BuildKit's fatal exit
		// status is included in the build marker instead of reporting only
		// the client-side closed-socket error.
		_ = bk.Process.Signal(syscall.SIGQUIT)
		waitCh := make(chan error, 1)
		go func() { waitCh <- bk.Wait() }()
		select {
		case waitErr := <-waitCh:
			time.Sleep(100 * time.Millisecond)
			diagnostic = append(diagnostic, buildkitLog.Bytes()...)
			diagnostic = append(diagnostic, []byte(fmt.Sprintf("buildkitd exit: %v\n", waitErr))...)
		case <-time.After(2 * time.Second):
			_ = bk.Process.Kill()
			select {
			case waitErr := <-waitCh:
				diagnostic = append(diagnostic, []byte(fmt.Sprintf("buildkitd exit after kill: %v\n", waitErr))...)
			case <-time.After(1 * time.Second):
				diagnostic = append(diagnostic, []byte("buildkitd exit: wait timeout\n")...)
			}
		}
		return writeAndPoweroff(m, fmt.Errorf("buildkitd readiness: %w (%s)", workerErr, workerOut), tailOf(diagnostic, m.LogTailBytes))
	}
	guestStage("buildkit-ready")

	if m.Framework != api.FrameworkDockerfile && strings.TrimSpace(m.RuntimeBaseRef) == "" {
		return writeAndPoweroff(m, fmt.Errorf("missing runtime_base_ref for %s build", m.Framework), "")
	}
	var restoreRailpackConfig func() error
	if m.Framework != api.FrameworkDockerfile {
		var err error
		restoreRailpackConfig, err = prepareRailpackConfig(m)
		if err != nil {
			return writeAndPoweroff(m, fmt.Errorf("prepare Railpack runtime base: %w", err), "")
		}
	}

	// 3. Pick the build command.
	argv := buildArgv(m)

	// 3. Run with a wall-clock timeout (we already get OOM protection from
	//    cgroup v2 memory.max on the Firecracker config — see spec §11).
	timeoutSec := m.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = api.BuildTimeoutSeconds
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.Command(argv[0], argv[1:]...)
	// Railpack/buildctl can leave descendants holding stdout/stderr or FUSE
	// mounts after the context deadline. Kill the whole build process group and
	// bound the wait so writeAndPoweroff always runs even if a descendant is
	// stuck in an uninterruptible FUSE wait.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = m.Workdir
	// Railpack is a BuildKit frontend too; both it and buildctl need the
	// daemon address in their environment. Keeping this unconditional avoids
	// the misleading success path where Railpack detects the app and only
	// fails once it starts its install step.
	// BuildKit v0.31.x tears down a session after a single 15-second health
	// check timeout. A slow bare-metal guest can legitimately spend several
	// minutes importing a remote layer, so use the upstream custom-header
	// contract through the patched buildctl client. Five minutes is still a
	// bounded liveness check and the build wall-clock timeout remains the outer
	// safety limit.
	cmd.Env = builderEnv(
		"USER=root",
		"HOME=/root",
		"TMPDIR=/tmp",
		"BUILDKIT_HOST=unix:///run/buildkit/buildkitd.sock",
		"BUILDKIT_SESSION_HEALTH_TIMEOUT_MS=300000",
	)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	if err := cmd.Start(); err != nil {
		if restoreRailpackConfig != nil {
			_ = restoreRailpackConfig()
		}
		return writeAndPoweroff(m, fmt.Errorf("start build command: %w", err), "")
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	var err error
	select {
	case err = <-waitCh:
	case <-ctx.Done():
		killProcessGroup(cmd)
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
		// Use exit 124 for the durable build marker even when the stuck
		// descendant prevents exec.Cmd.Wait from returning.
		err = context.DeadlineExceeded
	}
	fmt.Printf("guest-init: build command finished, err=%v, output bytes=%d\n", err, buf.Len())
	if buf.Len() > 0 {
		fmt.Printf("--- build output ---\n%s\n--- end build output ---\n", buf.String())
	}
	var combined bytes.Buffer
	if buf.Len() > 0 {
		combined.WriteString("[build]\n")
		combined.WriteString(tailOf(buf.Bytes(), m.LogTailBytes/2))
	}
	if err != nil && buildkitLog.Len() > 0 {
		combined.WriteString("\n[buildkitd]\n")
		combined.WriteString(tailOf(buildkitLog.Bytes(), m.LogTailBytes/2))
	}
	if restoreRailpackConfig != nil {
		if restoreErr := restoreRailpackConfig(); restoreErr != nil {
			if err == nil {
				err = fmt.Errorf("restore Railpack config: %w", restoreErr)
			} else {
				err = errors.Join(err, fmt.Errorf("restore Railpack config: %w", restoreErr))
			}
		}
	}
	return writeAndPoweroff(m, err, combined.String())
}

// probeBuilderWorkspace verifies the exact user/mount namespace boundary used
// by BuildKit before starting the daemon. The builder root is deliberately on
// the writable VM drive; a namespace-specific EACCES here otherwise gets
// reduced to BuildKit's generic "mkdir ... permission denied" message.
func probeBuilderWorkspace(buildkitRoot string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := exec.Command(
		"/usr/bin/unshare",
		rootlessBuildkitUnshareArgs(
			"/bin/sh",
			"-c",
			`id; stat -c '%n mode=%a uid=%u gid=%g' /build "$1"; mkdir -p "$1/.probe"; touch "$1/.probe/write"; rm -rf "$1/.probe"`,
			"faas-builder-workspace",
			buildkitRoot,
		)...,
	)
	probe.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	probe.Env = builderEnv()
	var out bytes.Buffer
	probe.Stdout = &out
	probe.Stderr = &out
	if err := probe.Start(); err != nil {
		return out.String(), err
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- probe.Wait() }()
	select {
	case err := <-waitCh:
		if err != nil {
			return out.String(), err
		}
		return out.String(), nil
	case <-ctx.Done():
		_ = syscall.Kill(-probe.Process.Pid, syscall.SIGKILL)
		select {
		case <-waitCh:
		case <-time.After(time.Second):
		}
		return out.String(), ctx.Err()
	}
}

// rootlessBuildkitUnshareArgs creates the user and mount namespaces used by
// the real builder. Mapping only the invoking UID/GID to namespace root
// (the portable `-U -r -m -f` form) leaves image-owned IDs such as GID 42
// unmapped. BuildKit then fails while unpacking otherwise valid layers with
// `lchown ... invalid argument`. The builder image deliberately ships
// util-linux plus newuidmap/newgidmap and a bounded subordinate-ID range, so
// ask unshare to map that range as well as the current root identity.
func rootlessBuildkitUnshareArgs(command ...string) []string {
	args := []string{
		"--user",
		"--map-auto",
		"--map-root-user",
		"--mount",
		"--fork",
	}
	return append(args, command...)
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = cmd.Process.Kill()
	}
}

// ensureFuseDevice creates the standard FUSE character device when devtmpfs
// did not populate it. Firecracker does not emulate a PCI device for FUSE;
// the guest kernel provides the driver directly, so this node is sufficient
// when CONFIG_FUSE_FS is enabled. If the kernel lacks FUSE, return the exact
// device error and let the build marker classify it instead of hiding the
// rootless snapshotter failure.
func ensureFuseDevice() error {
	const fusePath = "/dev/fuse"
	if _, err := os.Stat(fusePath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := syscall.Mknod(fusePath, syscall.S_IFCHR|0o600, int(unix.Mkdev(10, 229))); err != nil {
		return err
	}
	return nil
}

// flattenSingleSourceDir normalizes the common archive shape produced by the
// CLI, where every source entry is under one directory (for example
// hello-node/package.json). The host detector accepts that shape as the
// project root, so the guest must expose the same view to Railpack after
// extraction. Archives that already contain root files are left unchanged.
func flattenSingleSourceDir(workdir string) error {
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	root := filepath.Join(workdir, entries[0].Name())
	children, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := os.Rename(filepath.Join(root, child.Name()), filepath.Join(workdir, child.Name())); err != nil {
			return fmt.Errorf("move %s: %w", child.Name(), err)
		}
	}
	if err := os.Remove(root); err != nil {
		return fmt.Errorf("remove archive root: %w", err)
	}
	return nil
}

// prepareRailpackConfig injects the platform-selected runtime base into the
// source project for the duration of `railpack prepare`. Railpack's prepare
// command only reads railpack.json from the project root; without this small
// transactional overlay it silently generates a plan FROM railpack-runtime,
// while imaged later expects the pinned Gregale runner base.
//
// Customer configuration is preserved byte-for-byte and restored before the
// BuildKit solve starts, so the generated platform-only setting cannot leak
// into the OCI context or alter a later retry. A symlink at this path is
// rejected to keep the generated write inside the extracted source tree.
func prepareRailpackConfig(m api.BuildManifest) (func() error, error) {
	path := filepath.Join(m.Workdir, "railpack.json")
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink", path)
	}

	existed := err == nil
	originalMode := os.FileMode(0o644)
	var original []byte
	if existed {
		originalMode = info.Mode().Perm()
		original, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}

	config := map[string]any{}
	if existed && len(strings.TrimSpace(string(original))) > 0 {
		if err := json.Unmarshal(original, &config); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	deploy, err := nestedObject(config, "deploy")
	if err != nil {
		return nil, fmt.Errorf("parse %s deploy: %w", path, err)
	}
	base, err := nestedObject(deploy, "base")
	if err != nil {
		return nil, fmt.Errorf("parse %s deploy.base: %w", path, err)
	}
	base["image"] = m.RuntimeBaseRef
	// Alpine runner bases and base-minimal cannot execute Railpack's default
	// apt install phase. Preserve an explicit customer list, but make the
	// platform default empty for musl runtimes and minimal scratch bases.
	if isAlpineRuntime(m.Runtime) || m.Runtime == "" || strings.Contains(m.RuntimeBaseRef, "base-minimal") {
		if _, ok := deploy["aptPackages"]; !ok {
			deploy["aptPackages"] = []any{}
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, originalMode); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	if existed {
		if err := os.Chmod(path, originalMode); err != nil {
			return nil, fmt.Errorf("restore mode %s: %w", path, err)
		}
	}

	return func() error {
		if existed {
			if err := os.WriteFile(path, original, originalMode); err != nil {
				return err
			}
			return os.Chmod(path, originalMode)
		}
		return os.Remove(path)
	}, nil
}

func nestedObject(parent map[string]any, key string) (map[string]any, error) {
	if value, ok := parent[key]; ok {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object", key)
		}
		return object, nil
	}
	object := map[string]any{}
	parent[key] = object
	return object, nil
}

func isAlpineRuntime(runtime string) bool {
	return runtime == "node22" || runtime == "go124-alpine"
}

// seedBuildEntropy injects a fresh seed staged by builderd into the guest
// kernel's entropy pool before BuildKit starts. BuildKit generates an RSA
// proxy CA during startup; without this step a cold Firecracker guest can
// block indefinitely in crypto/rand/getrandom despite having virtio-rng
// attached.
func seedBuildEntropy() error {
	seed, err := os.ReadFile(api.BuildEntropyPath)
	if err != nil {
		return fmt.Errorf("read build entropy seed: %w", err)
	}
	if err := addHostEntropy(seed); err != nil {
		return fmt.Errorf("seed build entropy: %w", err)
	}
	if err := os.Remove(api.BuildEntropyPath); err != nil {
		return fmt.Errorf("remove build entropy seed: %w", err)
	}
	return nil
}

// buildArgv constructs the build-engine argv for one BuildManifest. The
// Dockerfile framework uses buildctl directly. Railpack first prepares its
// BuildKit plan, then the Railpack frontend is invoked with buildctl and an
// OCI exporter. Keeping the exporter in BuildKit avoids Railpack's local
// filesystem exporter, which copies a complete runtime tree into a directory
// before builderd can consume it.
//
// Extracted from runBuild so the table-driven TestBuildArgv can pin the wire
// shape without spinning up a builder VM.
func buildArgv(m api.BuildManifest) []string {
	contextDir := manifestBuildContext(m)
	switch m.Framework {
	case api.FrameworkDockerfile:
		return []string{
			"/usr/local/bin/buildctl", "--addr", "unix:///run/buildkit/buildkitd.sock", "build",
			"--frontend", "dockerfile.v0",
			"--local", "context=" + contextDir,
			"--local", "dockerfile=" + m.Workdir,
			"--output", "type=oci,dest=" + m.OutDir + "/image.tar",
		}
	}
	planDir := filepath.Dir(m.OutDir)
	planPath := filepath.Join(planDir, "railpack-plan.json")
	infoPath := filepath.Join(planDir, "railpack-info.json")
	prepare := strings.Join([]string{
		"/usr/local/bin/railpack", "prepare", shellQuote(m.Workdir),
		"--plan-out", shellQuote(planPath),
		"--info-out", shellQuote(infoPath),
	}, " ")
	build := strings.Join([]string{
		"/usr/local/bin/buildctl", "--addr", "unix:///run/buildkit/buildkitd.sock", "build",
		"--frontend", "gateway.v0",
		"--opt", "source=ghcr.io/railwayapp/railpack-frontend:latest",
		"--opt", "filename=railpack-plan.json",
		"--local", "context=" + shellQuote(contextDir),
		"--local", "dockerfile=" + shellQuote(planDir),
		"--output", "type=oci,dest=" + shellQuote(filepath.Join(m.OutDir, "image.tar")),
		"--progress", "plain",
	}, " ")
	return []string{"/bin/sh", "-c", "set -x; " + prepare + " && exec " + build}
}

func manifestBuildContext(m api.BuildManifest) string {
	if m.BuildContext != "" {
		return m.BuildContext
	}
	return m.Workdir
}

// shellQuote quotes a path embedded in the small prepare/build command above.
// The paths are platform-generated today, but keeping this boundary explicit
// prevents a future manifest field from becoming shell syntax accidentally.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func builderEnv(extra ...string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra)+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PATH=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "PATH="+builderPATH)
	return append(env, extra...)
}

// packageRailpackOCI converts Railpack's local rootfs exporter into the OCI
// image-layout tarball consumed by builderd/imaged. Railpack intentionally
// exports a directory, while the rest of the platform contract is an OCI
// layout archive at /build/out/image.tar.
//
//nolint:unused // retained as a fallback for older Railpack exporters.
func packageRailpackOCI(rootfsDir, imagePath string) error {
	layerFile, err := os.CreateTemp(filepath.Dir(imagePath), ".railpack-layer-*.tar")
	if err != nil {
		return fmt.Errorf("create railpack layer: %w", err)
	}
	layerPath := layerFile.Name()
	defer func() {
		_ = layerFile.Close()
		_ = os.Remove(layerPath)
	}()

	hash := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(layerFile, hash))
	if err := filepath.Walk(rootfsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == rootfsDir {
			return nil
		}
		rel, err := filepath.Rel(rootfsDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		var linkName string
		if info.Mode()&os.ModeSymlink != 0 {
			linkName, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, linkName)
		if err != nil {
			return err
		}
		header.Name = rel
		header.ModTime = time.Unix(0, 0)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path) //nolint:forbidigo // path is inside the guest build workspace.
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}); err != nil {
		_ = tw.Close()
		return fmt.Errorf("walk railpack rootfs: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close railpack layer: %w", err)
	}
	if err := layerFile.Sync(); err != nil {
		return fmt.Errorf("sync railpack layer: %w", err)
	}
	if err := layerFile.Close(); err != nil {
		return fmt.Errorf("close railpack layer file: %w", err)
	}
	layerInfo, err := os.Stat(layerPath)
	if err != nil {
		return fmt.Errorf("stat railpack layer: %w", err)
	}
	layerDigest := "sha256:" + hex.EncodeToString(hash.Sum(nil))

	config := struct {
		Architecture string   `json:"architecture"`
		OS           string   `json:"os"`
		Config       struct{} `json:"config"`
		RootFS       struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}{Architecture: "amd64", OS: "linux"}
	config.RootFS.Type = "layers"
	config.RootFS.DiffIDs = []string{layerDigest}
	configBytes, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal railpack config: %w", err)
	}
	configDigest := digestBytes(configBytes)
	configName := "blobs/sha256/" + strings.TrimPrefix(configDigest, "sha256:")

	manifest := struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Config        struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
		} `json:"config"`
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
		} `json:"layers"`
	}{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json"}
	manifest.Config.MediaType = "application/vnd.oci.image.config.v1+json"
	manifest.Config.Digest = configDigest
	manifest.Config.Size = int64(len(configBytes))
	manifest.Layers = append(manifest.Layers, struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	}{MediaType: "application/vnd.oci.image.layer.v1.tar", Digest: layerDigest, Size: layerInfo.Size()})
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal railpack manifest: %w", err)
	}
	manifestDigest := digestBytes(manifestBytes)
	manifestName := "blobs/sha256/" + strings.TrimPrefix(manifestDigest, "sha256:")
	indexBytes, err := json.Marshal(struct {
		SchemaVersion int `json:"schemaVersion"`
		Manifests     []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
		} `json:"manifests"`
	}{SchemaVersion: 2, Manifests: []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	}{{MediaType: "application/vnd.oci.image.manifest.v1+json", Digest: manifestDigest, Size: int64(len(manifestBytes))}}})
	if err != nil {
		return fmt.Errorf("marshal railpack index: %w", err)
	}

	imageFile, err := os.Create(imagePath)
	if err != nil {
		return fmt.Errorf("create OCI image: %w", err)
	}
	closeImage := func() error { return imageFile.Close() }
	imageTar := tar.NewWriter(imageFile)
	writeBytes := func(name string, data []byte) error {
		if err := imageTar.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err := imageTar.Write(data)
		return err
	}
	if err := writeBytes("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)); err != nil {
		_ = closeImage()
		return fmt.Errorf("write OCI layout: %w", err)
	}
	if err := writeBytes("index.json", indexBytes); err != nil {
		_ = closeImage()
		return fmt.Errorf("write OCI index: %w", err)
	}
	if err := writeBytes(configName, configBytes); err != nil {
		_ = closeImage()
		return fmt.Errorf("write OCI config: %w", err)
	}
	if err := writeBytes(manifestName, manifestBytes); err != nil {
		_ = closeImage()
		return fmt.Errorf("write OCI manifest: %w", err)
	}
	if err := imageTar.WriteHeader(&tar.Header{Name: "blobs/sha256/" + strings.TrimPrefix(layerDigest, "sha256:"), Mode: 0o644, Size: layerInfo.Size()}); err != nil {
		_ = closeImage()
		return fmt.Errorf("write OCI layer header: %w", err)
	}
	layerReader, err := os.Open(layerPath) //nolint:forbidigo // layerPath is a guest-local temporary artifact.
	if err != nil {
		_ = closeImage()
		return fmt.Errorf("open OCI layer: %w", err)
	}
	_, copyErr := io.Copy(imageTar, layerReader)
	closeLayerErr := layerReader.Close()
	if copyErr != nil {
		_ = closeImage()
		return fmt.Errorf("copy OCI layer: %w", copyErr)
	}
	if closeLayerErr != nil {
		_ = closeImage()
		return fmt.Errorf("close OCI layer: %w", closeLayerErr)
	}
	if err := imageTar.Close(); err != nil {
		_ = closeImage()
		return fmt.Errorf("close OCI image tar: %w", err)
	}
	if err := closeImage(); err != nil {
		return fmt.Errorf("close OCI image: %w", err)
	}
	return nil
}

//nolint:unused // used by the retained Railpack OCI fallback above.
func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// classify maps an in-VM exit code to a builderd FailureClass. The vocabulary
// here matches the canonical names parsed by builderd's ProcessOne
// (FailureUserError / FailureInfra / FailureOOM / FailureTimeout).
func classify(exitCode int) string {
	switch exitCode {
	case 137:
		return "FailureOOM"
	case 124:
		return "FailureTimeout"
	case 0:
		return ""
	default:
		return "FailureUserError"
	}
}

// tailOf returns the last n bytes of data (or all of it if shorter). Used to
// truncate the build log so build-done.json stays small.
func tailOf(data []byte, n int) string {
	if n <= 0 || len(data) <= n {
		return string(data)
	}
	return string(data[len(data)-n:])
}

// writeAndPoweroff writes /etc/faas/build-done.json (vmmd's Destroy loopback-
// mounts the chroot drive1 to copy it out) and powers off the VM. Any
// failure here is logged but doesn't prevent the poweroff — vmmd will
// surface a fallback exit-code classification via the watch-dog capture.
func writeAndPoweroff(m api.BuildManifest, runErr error, logTail string) error {
	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else if errors.Is(runErr, context.DeadlineExceeded) {
			exitCode = 124
		} else {
			exitCode = 1
		}
	}
	fc := classify(exitCode)
	if logTail == "" && runErr != nil {
		logTail = runErr.Error()
	}
	done := api.BuildDone{
		SchemaVersion: 1,
		BuildID:       m.BuildID,
		ExitCode:      exitCode,
		OCIImagePath:  m.OutDir + "/image.tar",
		LogTail:       logTail,
		FailureClass:  fc,
	}
	if data, mErr := json.Marshal(done); mErr == nil {
		if f, openErr := os.OpenFile(api.BuildDonePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644); openErr != nil {
			fmt.Fprintf(os.Stderr, "guest-init: open build-done: %v\n", openErr)
		} else {
			if _, writeErr := f.Write(data); writeErr != nil {
				fmt.Fprintf(os.Stderr, "guest-init: write build-done: %v\n", writeErr)
			}
			if syncErr := f.Sync(); syncErr != nil {
				fmt.Fprintf(os.Stderr, "guest-init: sync build-done: %v\n", syncErr)
			}
			if closeErr := f.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "guest-init: close build-done: %v\n", closeErr)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "guest-init: marshal build-done: %v\n", mErr)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "guest-init: build failed: %v\n", runErr)
	}
	// Flush all overlay upper metadata/data before the forced poweroff. The
	// host exports the ext4 immediately after Firecracker exits; without an
	// explicit sync, the manifest and image can still be only in guest page
	// cache when the image is loop-mounted on the host.
	_ = exec.Command("/bin/sync").Run()
	// poweroff -f so vmmd's Destroy sees the exit code via firecracker's
	// natural exit. exec.CommandContext's timeout doesn't trigger poweroff —
	// we always get here.
	if err := exec.Command("/sbin/poweroff", "-f").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "guest-init: poweroff: %v\n", err)
	}
	// Surface as a guest-init error so the cmd survives long enough for
	// firecracker to capture the exit (poweroff schedules immediate halt).
	if runErr != nil {
		return runErr
	}
	return nil
}

// classifyTail is a debug-only helper used to shorten long log tails in
// writeBuildDone. Kept as a package-private function so tests can import it
// via an internal-test file (not used elsewhere; would be dead code on darwin).
var _ = strings.HasPrefix

// mountBasics mounts the pseudo-filesystems every app expects.
func mountBasics() error {
	type m struct{ src, dst, fs string }
	for _, mnt := range []m{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"tmpfs", "/tmp", "tmpfs"},
		{"devtmpfs", "/dev", "devtmpfs"},
	} {
		_ = os.MkdirAll(mnt.dst, 0o755)
		if err := syscall.Mount(mnt.src, mnt.dst, mnt.fs, 0, ""); err != nil {
			// /dev may already be a devtmpfs from the kernel; tolerate EBUSY.
			if mnt.dst != "/dev" {
				return fmt.Errorf("mount %s: %w", mnt.dst, err)
			}
		}
	}
	return nil
}

// assembleOverlay mounts the app layer and stacks it over the read-only base.
//
// PR-B / issue #463 / ADR-069: N+1 drive topology. drive0 (vda) is the
// shared read-only base; drive1 (vdb) is the per-app rw upper; drive2
// (vdc), drive3 (vdd), ... are sidecar drives mounted read-only as
// additional overlay lowers. The single writable upper stays on drive1
// (ADR-069 §"no shared writable layer between workloads"). The merged
// root's precedence is base → main → sidecar-0 → sidecar-1 → … so the
// deployment roster on the main upper and each sidecar's name-scoped
// runtime manifest are visible from the merged root even though the
// base ships none.
//
// The legacy 2-drive path (no sidecars) is preserved as the default
// branch: assembleOverlay does NOT touch /proc/partitions or the
// roster file unless a /etc/faas/workloads.json on drive1 lists
// sidecars. Pre-PR-B VMs never had a roster file, so this keeps the
// legacy shape working unchanged.
func assembleOverlay() (string, error) {
	if err := os.MkdirAll(layerMount, 0o755); err != nil {
		return "", err
	}
	if err := syscall.Mount(layerDevice, layerMount, "ext4", 0, ""); err != nil {
		return "", fmt.Errorf("mount layer %s: %w", layerDevice, err)
	}
	// Full-rootfs artifacts contain all OCI layers and a marker written by
	// pkg/rootfs.BuildFullRootfs. They use the existing layer device slot for
	// wire compatibility, but must become the pivot root directly; stacking
	// them as an overlay upper would leave the shared base visible underneath.
	marker := filepath.Join(layerMount, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
	if _, err := os.Stat(marker); err == nil {
		content, readErr := os.ReadFile(marker)
		if readErr != nil {
			return "", fmt.Errorf("read full-rootfs marker: %w", readErr)
		}
		if string(content) != api.FullRootfsMarkerValue {
			return "", fmt.Errorf("invalid full-rootfs marker payload")
		}
		return layerMount, nil
	} else if !isNotExist(err) {
		return "", fmt.Errorf("inspect full-rootfs marker: %w", err)
	}
	for _, d := range []string{"upper", "work", "merged"} {
		if err := os.MkdirAll(layerMount+"/"+d, 0o755); err != nil {
			return "", err
		}
	}
	// PR-B: discover sidecar count by reading the roster on drive1.
	// Absent file = legacy 2-drive path; present file with empty
	// sidecars = legacy supervisor shape; present file with non-empty
	// sidecars = mount each sidecar drive and stack as additional
	// read-only lowers.
	sidecarDevices, err := discoverSidecarDevices(layerMount)
	if err != nil {
		return "", fmt.Errorf("discover sidecars: %w", err)
	}
	for _, dev := range sidecarDevices {
		mp := layerMount + "/lower-" + dev.name
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", mp, err)
		}
		if err := syscall.Mount(dev.device, mp, "ext4", syscall.MS_RDONLY, ""); err != nil {
			return "", fmt.Errorf("mount sidecar %s at %s: %w", dev.device, mp, err)
		}
	}
	// Build lowerdir in stack order (lowest precedence first): base
	// = the kernel root (= `/`); sidecar-0, sidecar-1, ... appended
	// in stability order so sidecar-N has the highest precedence
	// among the read-only layers.
	lowerdir := "/"
	for _, dev := range sidecarDevices {
		lowerdir += ":" + layerMount + "/lower-" + dev.name
	}
	opts := "lowerdir=" + lowerdir +
		",upperdir=" + layerMount + "/upper" +
		",workdir=" + layerMount + "/work"
	if err := syscall.Mount("overlay", newRoot, "overlay", 0, opts); err != nil {
		return "", fmt.Errorf("mount overlay: %w", err)
	}
	return newRoot, nil
}

// sidecarDevice (issue #463 / ADR-069 / PR-B) is one entry on the
// sidecar drive list assembleOverlay mounts. name is the
// stable suffix used as the lower-<name> mountpoint and as the
// per-drive stamp suffix; device is the kernel device path
// (e.g. /dev/vdc). The PR-B plan keeps the device naming simple —
// sidecar-N lives on /dev/vd<c+1> where c is the sidecar index —
// so the helper doesn't need to walk /proc/partitions or decode
// virtio-blk names. If a future caller needs a different layout,
// the device field is the only thing to override.
type sidecarDevice struct {
	name   string // "sidecar-0", "sidecar-1", ...
	device string // "/dev/vdc", "/dev/vdd", ...
}

// discoverSidecarDevices reads the roster file from drive1 (the
// main workload's drive, mounted at mountRoot by the caller) and
// returns one entry per sidecar workload. The roster is the
// authoritative source for the sidecar count because the FC
// config's drive list and the wake-time wire agree on it — schedd
// is the single writer, vmmd mirrors it into BuildColdBootConfig,
// and guest-init learns it by reading the same file the
// orchestrator consumes. Without the roster file (legacy path),
// the function returns nil and assembleOverlay emits the legacy
// 2-drive overlay.
//
// A missing or malformed roster file is the legacy path: the
// function logs and returns nil. A roster file that lists more
// sidecars than the underlying device count can support (e.g.
// 3 sidecars but no vde) is caught at mount time by the syscall
// in assembleOverlay's loop; this helper never touches devices.
func discoverSidecarDevices(mountRoot string) ([]sidecarDevice, error) {
	rosterPath := mountRoot + "/" + workloadRosterPath
	data, err := os.ReadFile(rosterPath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil // legacy 2-drive path
		}
		return nil, fmt.Errorf("read roster %s: %w", rosterPath, err)
	}
	var roster workloadRoster
	if err := json.Unmarshal(data, &roster); err != nil {
		return nil, fmt.Errorf("parse roster %s: %w", rosterPath, err)
	}
	if len(roster.Sidecars) == 0 {
		return nil, nil // present but empty — legacy supervisor shape
	}
	out := make([]sidecarDevice, 0, len(roster.Sidecars))
	for i := range roster.Sidecars {
		// Device naming: /dev/vda = drive0 (base), /dev/vdb =
		// drive1 (main, the per-app rw upper). Sidecar 0 starts
		// at /dev/vdc (drive2) and increments. The cap of 2
		// sidecars per deployment (ADR-068) caps this at vdd.
		out = append(out, sidecarDevice{
			name:   fmt.Sprintf("sidecar-%d", i),
			device: fmt.Sprintf("/dev/vd%c", 'c'+i),
		})
	}
	return out, nil
}

// pivotInto makes root the new root filesystem.
func pivotInto(root string) error {
	if err := os.MkdirAll(root+"/oldroot", 0o755); err != nil {
		return err
	}
	if err := syscall.PivotRoot(root, root+"/oldroot"); err != nil {
		return err
	}
	if err := syscall.Chdir("/"); err != nil {
		return err
	}
	// Detach the old root lazily.
	_ = syscall.Unmount("/oldroot", syscall.MNT_DETACH)

	// Re-attach devtmpfs at the new root's /dev. The earlier mountBasics
	// devtmpfs was on the OLD root and is gone after pivot; the merged
	// overlay's /dev is whatever the base layer shipped (null/console/tty
	// only) — without a fresh devtmpfs the guest has no /dev/hwrng,
	// /dev/urandom, /dev/zero, etc. and the resume hook's reseed step
	// fails on ENOENT.
	if err := syscall.Mount("devtmpfs", "/dev", "devtmpfs", 0, ""); err != nil {
		slog.Default().Warn("post-pivot devtmpfs mount failed", "err", err)
	}
	// Same story for /proc, /sys, /tmp — mountBasics attached them to the
	// OLD root, so they're gone after pivot. Without /proc the resume hook
	// can't read /proc/sys/kernel/random/uuid to record its freshly-rekeyed
	// value (spec §11 V6).
	for _, mnt := range []struct{ src, dst, fs string }{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"tmpfs", "/tmp", "tmpfs"},
	} {
		_ = os.MkdirAll(mnt.dst, 0o755)
		if err := syscall.Mount(mnt.src, mnt.dst, mnt.fs, 0, ""); err != nil {
			slog.Default().Warn("post-pivot mount failed", "dst", mnt.dst, "err", err)
		}
	}
	return nil
}

// lookupUID resolves the app user name to a uid. The runner images create the
// app user at DefaultAppUID; unknown users fall back to that.
//
// M-3 commit 8: when the full-rootfs path is active, the
// builder writes the merged /etc/passwd entries into
// /etc/faas/app_passwd (binary table, see pkg/rootfs/writePasswdTable).
// This function reads that table when present. Missing file /
// missing entry → fall back to DefaultAppUID (1000) so the
// two-drive legacy path is unaffected.
//
// ADR-142 §Decision 3 (binary-search reader).
func lookupUID(user string) int {
	if user == api.DefaultAppUser {
		return api.DefaultAppUID
	}
	if uid, ok := readPasswdTable(user); ok {
		return uid
	}
	return api.DefaultAppUID
}

// runCharacterizationForSup is the boot-side glue between the
// characterize_linux.go probe and the supervisor. It owns the
// "what PID is the customer app" lifecycle and the "what was the
// last exit code" visibility that runCharacterization needs:
//
//   - `AppPID()` is polled by waitForBind every 50 ms until the
//     supervisor's lastCmd pointer is non-nil. If the supervisor
//     finishes without ever forking (e.g. a test-only stub Start),
//     AppPID returns -1 forever and the probe times out at the
//     bind-dealine → classified `job`.
//   - `WaitForExit()` blocks until the supervisor's Run returns.
//     We bridge by polling the supervisor's lastExitCode via
//     reflection-free access (every 50 ms) and surfacing it as
//     the report's ExitCode.
//
// Returns when runCharacterization returns (duration: ~10s
// deadline or earlier on bind+probe). Errors are warn-logged;
// the platform contract is "no signal" not "won't boot".
func runCharacterizationForSup(sup *Supervisor, manifest api.AppManifest) {
	log := slog.Default()
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	// Exit-code visibility is exposed via WaitForExit below, which
	// polls sup.LastExitCode synchronously. The supervisor's trackExit
	// fires synchronously inside Run() before it returns, so the polling
	// loop unblocks as soon as the app exits — no separate gate goroutine
	// or `done` channel is needed here.

	args := RunArgs{
		Manifest: manifest,
		AppPID:   func() int { return sup.LastAppPID() },
		WaitForExit: func() (int, error) {
			// Block until the supervisor's exit code stabilises.
			for sup.LastExitCode() == -1 {
				time.Sleep(50 * time.Millisecond)
			}
			return sup.LastExitCode(), nil
		},
		RingBufferTail: func() string {
			// Populated from the supervisor's ring buffer (Slice A
			// PR-B). The buffer is allocated lazily on the first
			// LogBuffer() call (cmd.Stdout wiring in runAppWithEnv),
			// so a sup without a forked app returns "" — same
			// shape as the pre-PR-B empty string. The wire-side
			// truncateLog at characterize_linux.go:198 clamps the
			// returned bytes to VsockCharacterizationMaxBody (32
			// KiB), so the 64 KiB buffer's over-budget tail never
			// overflows the JSON body.
			if sup == nil {
				return ""
			}
			return sup.LogTail()
		},
		Log: log,
	}
	runCharacterization(context.Background(), args)
}
