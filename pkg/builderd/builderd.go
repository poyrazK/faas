// Package builderd — build orchestrator + ephemeral builder microVMs (spec
// §4.5, ADR-003, ADR-005).
//
// builderd is the ONLY process that runs Railpack/buildkit (spec §4.5). It
// claims a queued build, sources the source tarball, detects the framework,
// spawns a builder microVM, streams logs back, and on success hands the
// produced app layer to imaged via the existing snapshot_prime handshake.
//
// Build slots (CLAUDE.md "Builder slots"):
//   - 1 guaranteed slot — lives in faas-cp.slice.
//   - 1 opportunistic slot — only when tenant residency < 60%.
//
// The VM spawn itself is `//go:build metal`; this file holds the pure-Go
// orchestration so the slot/cache/log/detect logic is unit-tested without
// /dev/kvm.
package builderd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Notifier is the pg_notify surface builderd uses. db.Notify satisfies it.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// ResidencyProbe reports live tenant-RAM residency. schedd's Ledger is the
// authoritative source; builderd consults it before allocating the
// opportunistic 2nd slot. A nil probe is treated as "no extra slot" — safer
// default than "always allow".
type ResidencyProbe interface {
	ResidentMB() int
}

// VM is the small builder-VM surface. The metal implementation lives in vm_metal.go
// (//go:build metal); the non-metal stub returns ErrNotMetal so unit tests
// skip the spawn without panicking.
//
// Spawn returns when vmmd has accepted the cold-boot (NOT when the build
// itself finishes) — the orchestrator then calls WaitForCompletion to
// block on the in-VM build.

// ErrNotMetal is the sentinel returned by the non-metal VM stub.
var ErrNotMetal = errors.New("builderd: VM spawn is metal-only; use a fake VM in unit tests")

// Config is the on-disk shape of /etc/faas/builderd.toml. Every field has a
// working default.
type Config struct {
	// CacheDir is where built app layers are content-addressed for cache hits.
	// Empty => /var/cache/faas/builds.
	CacheDir string `toml:"cache_dir"`
	// SourceSpoolDir mirrors apid's source spool; builderd reads from here.
	SourceSpoolDir string `toml:"source_spool_dir"`
	// ResidentProbeSocket is where builderd reaches schedd's residency
	// reporting. Empty disables the opportunistic 2nd slot.
	ResidentProbeSocket string `toml:"resident_probe_socket"`
	// MetricsAddr is the bind address for /metrics. Empty disables it.
	MetricsAddr string `toml:"metrics_addr"`
	// BuildTimeoutSeconds mirrors pkg/api/limits.go BuildTimeoutSeconds;
	// the per-build deadline. 0 falls back to the limit.
	BuildTimeoutSeconds int `toml:"build_timeout_seconds"`
}

// Builderd is the orchestrator. It is the cmd/builderd main loop.
type Builderd struct {
	store    state.Store
	notif    Notifier
	vm       VM
	cache    *Cache
	detector *Detector
	resid    ResidencyProbe
	cfg      Config
	log      *slog.Logger
}

// New wires a Builderd. vm may be nil in unit tests (the orchestrator still
// runs the orchestration; spawn returns ErrNotMetal).
func New(store state.Store, notif Notifier, vm VM, cache *Cache, det *Detector, resid ResidencyProbe, cfg Config, log *slog.Logger) *Builderd {
	if log == nil {
		log = slog.Default()
	}
	if cache == nil {
		cache = NewCache(defaultCacheDir(cfg))
	}
	if det == nil {
		det = NewDetector()
	}
	if cfg.BuildTimeoutSeconds == 0 {
		cfg.BuildTimeoutSeconds = api.BuildTimeoutSeconds
	}
	return &Builderd{
		store:    store,
		notif:    notif,
		vm:       vm,
		cache:    cache,
		detector: det,
		resid:    resid,
		cfg:      cfg,
		log:      log,
	}
}

// BuildResult is the outcome of one queued build.
type BuildResult struct {
	BuildID    string
	LayerPath  string
	LayerBytes int64
	CacheHit   bool
}

// ProcessOne claims the next queued build (or processes the buildID passed in
// by the pg_notify handler) and runs it end-to-end:
//
//  1. Mark running (started=true, finished=false).
//  2. Detect framework from the source tarball.
//  3. Cache lookup — if hit, skip the VM spawn entirely.
//  4. Allocate a slot (gate against tenant residency if 2nd).
//  5. Spawn the builder VM.
//  6. On success: SetDeploymentRootfs + snapshot_prime (the existing
//     imaged handshake — same as a registry image deploy).
//  7. On failure: classify (oom/timeout/user_error/infra) and write it.
//
// The caller (cmd/builderd's loop) is the only writer to the build row.
func (b *Builderd) ProcessOne(ctx context.Context, buildID string) (BuildResult, error) {
	build, err := b.store.BuildByID(ctx, buildID)
	if err != nil {
		return BuildResult{}, fmt.Errorf("builderd: load build %s: %w", buildID, err)
	}
	dep, err := b.store.DeploymentByID(ctx, build.DeploymentID)
	if err != nil {
		return BuildResult{}, fmt.Errorf("builderd: load deployment: %w", err)
	}
	app, err := b.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return BuildResult{}, fmt.Errorf("builderd: load app: %w", err)
	}

	if err := b.store.UpdateBuildStatus(ctx, build.ID, state.BuildRunning, "", true, false); err != nil {
		return BuildResult{}, fmt.Errorf("builderd: mark running: %w", err)
	}
	defer b.emitBuildLog(ctx, build.ID, "build started\n")

	fw, err := b.detector.Detect(dep.SourcePath)
	if err != nil {
		b.markFailed(ctx, build.ID, state.FailureUserError, "framework detect: "+err.Error())
		return BuildResult{}, err
	}
	b.emitBuildLog(ctx, build.ID, "detected framework: "+string(fw)+"\n")

	// Cache check: content-addressed by sha256(source). A hit means we
	// produced this exact app layer before and can short-circuit the VM
	// spawn entirely (this is the ≥2× speedup gate, spec §14 M6).
	srcHash, err := hashFile(dep.SourcePath)
	if err != nil {
		b.markFailed(ctx, build.ID, state.FailureInfra, "source hash: "+err.Error())
		return BuildResult{}, err
	}
	if cached, ok := b.cache.Lookup(srcHash, fw); ok {
		b.emitBuildLog(ctx, build.ID, fmt.Sprintf("cache hit (%s, %d bytes) — skipping vm spawn\n", cached.Path, cached.Bytes))
		if err := b.store.SetDeploymentRootfs(ctx, dep.ID, cached.Path, cached.Bytes); err != nil {
			b.markFailed(ctx, build.ID, state.FailureInfra, "set rootfs: "+err.Error())
			return BuildResult{}, err
		}
		// imaged handles the cache-hit tarball the same as a fresh build:
		// it converts the OCI image into an app-layer ext4 and re-emits
		// NotifySnapshotPrime for schedd. The split (snapshot_boot for
		// imaged, snapshot_prime for schedd) avoids the race where schedd
		// tries to mount a .tar as a virtio-blk drive.
		if err := b.notif.Notify(ctx, db.NotifySnapshotBoot,
			fmt.Sprintf(`{"app_id":"%s","deployment_id":"%s"}`, app.ID, dep.ID)); err != nil {
			b.markFailed(ctx, build.ID, state.FailureInfra, "notify boot: "+err.Error())
			return BuildResult{}, err
		}
		b.markSucceeded(ctx, build.ID)
		return BuildResult{BuildID: build.ID, LayerPath: cached.Path, LayerBytes: cached.Bytes, CacheHit: true}, nil
	}

	// Slot allocation (CLAUDE.md: builds never outrank tenant wakes).
	slot := DecideSlot(b.resid, api.RAMAdmissionCeilingMB)
	if !slot.Allowed {
		b.markFailed(ctx, build.ID, state.FailureInfra, "no builder slot: "+slot.Reason)
		return BuildResult{}, errors.New("builderd: no slot")
	}
	b.emitBuildLog(ctx, build.ID, fmt.Sprintf("allocated builder slot (%s)\n", slot.Label))

	if b.vm == nil {
		b.markFailed(ctx, build.ID, state.FailureInfra, "vm driver not wired (metal only)")
		return BuildResult{}, ErrNotMetal
	}

	timeout := time.Duration(b.cfg.BuildTimeoutSeconds) * time.Second
	vmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	handle, err := b.vm.Spawn(vmCtx, VMRequest{
		BuildID:      build.ID,
		TenantID:     app.AccountID,
		DeploymentID: dep.ID,
		SourcePath:   dep.SourcePath,
		Framework:    fw,
		LogPath:      dep.LogPath,
		RAMMB:        api.BuildVMRAMMB,
		TimeoutSec:   b.cfg.BuildTimeoutSeconds,
	})
	if err != nil {
		// Translate a context-deadline to timeout-class; everything else is infra.
		fc := state.FailureInfra
		if errors.Is(err, context.DeadlineExceeded) {
			fc = state.FailureTimeout
		}
		b.markFailed(ctx, build.ID, fc, "vm spawn: "+err.Error())
		return BuildResult{}, err
	}

	out, err := b.vm.WaitForCompletion(vmCtx, handle)
	if err != nil {
		// Translate a context-deadline to timeout-class; everything else is infra.
		fc := state.FailureInfra
		if errors.Is(err, context.DeadlineExceeded) {
			fc = state.FailureTimeout
		}
		b.markFailed(ctx, build.ID, fc, "vm wait: "+err.Error())
		return BuildResult{}, err
	}
	if out.ExitCode != 0 {
		// Prefer the failure class the guest-init captured in build-done.json
		// (one of: "FailureUserError", "FailureInfra", "FailureOOM",
		// "FailureTimeout"). Falls back to the canonical exit-code table for
		// cases where the VM died before guest-init wrote it (kill -9, OOM at
		// guest-init, etc).
		fc := state.FailureUserError
		switch out.FailureClass {
		case "FailureUserError":
			fc = state.FailureUserError
		case "FailureInfra":
			fc = state.FailureInfra
		case "FailureOOM":
			fc = state.FailureOOM
		case "FailureTimeout":
			fc = state.FailureTimeout
		case "":
			switch out.ExitCode {
			case 137:
				fc = state.FailureOOM
			case 124:
				fc = state.FailureTimeout
			}
		}
		b.markFailed(ctx, build.ID, fc, fmt.Sprintf("build exited %d", out.ExitCode))
		return BuildResult{}, fmt.Errorf("builderd: vm exit %d", out.ExitCode)
	}

	// Stamp the cache so the next build of the same source is a hit.
	if err := b.cache.Store(srcHash, fw, out.OCIImage, out.LogTailBytes); err != nil {
		b.log.Warn("builderd: cache store failed (continuing)", "err", err)
	}
	// Stamp the produced layer path onto the deployment row. imaged will
	// receive a snapshot_boot notification, convert the OCI tarball into
	// a per-app ext4 (drive1), and re-emit NotifySnapshotPrime for schedd
	// to cold-boot + snapshot. Splitting the channel prevents schedd from
	// trying to mount the OCI tarball as a virtio-blk drive (it would 400).
	if err := b.store.SetDeploymentRootfs(ctx, dep.ID, out.OCIImage, out.LogTailBytes); err != nil {
		b.markFailed(ctx, build.ID, state.FailureInfra, "set rootfs: "+err.Error())
		return BuildResult{}, err
	}
	if err := b.notif.Notify(ctx, db.NotifySnapshotBoot,
		fmt.Sprintf(`{"app_id":"%s","deployment_id":"%s"}`, app.ID, dep.ID)); err != nil {
		b.markFailed(ctx, build.ID, state.FailureInfra, "notify boot: "+err.Error())
		return BuildResult{}, err
	}
	b.markSucceeded(ctx, build.ID)
	return BuildResult{BuildID: build.ID, LayerPath: out.OCIImage, LayerBytes: out.LogTailBytes}, nil
}

// markSucceeded updates the build row to BuildSucceeded, finished=true.
func (b *Builderd) markSucceeded(ctx context.Context, buildID string) {
	if err := b.store.UpdateBuildStatus(ctx, buildID, state.BuildSucceeded, "", false, true); err != nil {
		b.log.Warn("builderd: mark succeeded", "build", buildID, "err", err)
	}
}

// markFailed updates the build row with a failure_class + error and finished=true.
// The empty-string fc guard in pkg/state means a non-empty fc must be passed.
func (b *Builderd) markFailed(ctx context.Context, buildID string, fc state.FailureClass, msg string) {
	b.log.Warn("builderd: build failed", "build", buildID, "failure_class", fc, "msg", msg)
	b.emitBuildLog(ctx, buildID, "FAILED: "+msg+"\n")
	if err := b.store.UpdateBuildStatus(ctx, buildID, state.BuildFailed, fc, false, true); err != nil {
		b.log.Warn("builderd: mark failed", "build", buildID, "err", err)
	}
}

// emitBuildLog appends a line to the build log file (lazily opened) and fans
// out a build_log notification so any SSE subscriber sees it (UX spec §2.4).
// Best-effort: a failure here is logged but never blocks the build.
func (b *Builderd) emitBuildLog(ctx context.Context, buildID, line string) {
	if err := appendLog(ctx, b.store, buildID, line); err != nil {
		b.log.Warn("builderd: append log", "build", buildID, "err", err)
	}
	if b.notif == nil {
		return
	}
	payload := fmt.Sprintf(`{"build":"%s","line":%q}`, buildID, line)
	if err := b.notif.Notify(ctx, db.NotifyBuildLog, payload); err != nil {
		b.log.Warn("builderd: notify log", "build", buildID, "err", err)
	}
}

// defaultCacheDir honours Config.CacheDir; an empty value falls back to the
// canonical /var/cache/faas/builds path.
func defaultCacheDir(cfg Config) string {
	if cfg.CacheDir != "" {
		return cfg.CacheDir
	}
	return "/var/cache/faas/builds"
}
