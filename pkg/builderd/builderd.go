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
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
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

// ErrNoSlot is returned when the slot allocator (DecideSlot) rules
// the build out — the 1 + 1 opportunistic builder budget is fully
// consumed by other in-flight builds (spec §14). processClaimedBuild
// REQUEUES the row (preserving FIFO position) before returning
// ErrNoSlot, so the durability-net worker (cmd/builderd/main.go::workerLoop)
// and any later LISTEN-driven caller both observe the row as
// queued-and-awaiting-slot. PR-B §B.5 — the requeue lives inside
// processClaimedBuild so the LISTEN path and the poll path share one
// implementation.
var ErrNoSlot = errors.New("builderd: no builder slot available")

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
	// ops is the build-metrics sink (ADR-030). nil in unit tests that
	// don't care about metrics; all observations guard on nil (the
	// ObserveBuild* methods are also nil-safe). Wired in production via
	// WithOpsMetrics from cmd/builderd.
	ops *wire.OpsMetrics
	// slotDecide is the slot-allocation hook. Production wires
	// b.slotDecide = DecideSlot in New; tests inject a closure to
	// exercise the no-slot requeue path without standing up a full
	// ResidencyProbe rig. nil falls back to DecideSlot(b.resid, …)
	// inside processClaimedBuild.
	slotDecide func(ResidencyProbe, int) SlotDecision
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

// WithOpsMetrics attaches the build-metrics sink (ADR-030) and returns the
// same Builderd for chaining. Mirrors pkg/sched.Engine.WithOpsMetrics.
// cmd/builderd wires the daemon's real *wire.OpsMetrics; leaving it unset
// (the unit-test default) makes every observation a no-op.
func (b *Builderd) WithOpsMetrics(ops *wire.OpsMetrics) *Builderd {
	b.ops = ops
	return b
}

// WithSlotDecider swaps the slot-allocation function for tests so the
// no-slot requeue path (PR-B §B.5) can be exercised without standing
// up a full ResidencyProbe rig. Production callers must NOT use this
// — DecideSlot is the canonical implementation.
func (b *Builderd) WithSlotDecider(f func(ResidencyProbe, int) SlotDecision) *Builderd {
	b.slotDecide = f
	return b
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
//  1. ClaimQueuedBuild — atomic queued → running CAS. Returns
//     ErrNotFound when the row is missing or already running/succeeded/
//     failed; we drop duplicate build_queued notifications (apid write
//     path + imaged reaper, PR-A) silently.
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
	build, err := b.store.ClaimQueuedBuild(ctx, buildID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Already claimed (duplicate notify) or terminal. Drop
			// silently — the other claimant owns it.
			return BuildResult{}, nil
		}
		return BuildResult{}, fmt.Errorf("builderd: claim build %s: %w", buildID, err)
	}
	return b.processClaimedBuild(ctx, build)
}

// ProcessNext is the durability-net worker surface (PR-B). It picks
// the next queued build via SELECT … FOR UPDATE SKIP LOCKED, runs the
// canonical pipeline, and returns ErrNotFound when the queue is
// empty (the worker sleeps without surfacing an error). On slot
// denial (DecideSlot returns !Allowed) the row is re-queued rather
// than marked failed — the worker calls store.RequeueBuild right
// after seeing ErrNoSlot so the build stays in the FIFO position
// until a slot opens up. cmd/builderd's workerLoop in main.go owns
// the cadence.
func (b *Builderd) ProcessNext(ctx context.Context) (BuildResult, error) {
	build, err := b.store.ClaimNextQueuedBuild(ctx)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return BuildResult{}, state.ErrNotFound
		}
		return BuildResult{}, fmt.Errorf("builderd: claim next build: %w", err)
	}
	return b.processClaimedBuild(ctx, build)
}

// processClaimedBuild runs the canonical pipeline for a build that
// has already been CAS-claimed (via ClaimQueuedBuild or
// ClaimNextQueuedBuild). Both ProcessOne (LISTEN-driven) and
// ProcessNext (poll-driven) call into here so the only divergence
// between the two surfaces is the claim SQL — the rest of the
// pipeline (cache check, slot allocation, VM spawn, wait, classify,
// terminal write) is shared 1:1.
func (b *Builderd) processClaimedBuild(ctx context.Context, build state.Build) (BuildResult, error) {
	dep, err := b.store.DeploymentByID(ctx, build.DeploymentID)
	if err != nil {
		return BuildResult{}, fmt.Errorf("builderd: load deployment: %w", err)
	}
	app, err := b.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return BuildResult{}, fmt.Errorf("builderd: load app: %w", err)
	}

	// started_at was set by ClaimQueuedBuild; the legacy UpdateBuildStatus
	// call here would clobber it, so we skip it. (Previously this line
	// started_at = now() via UpdateBuildStatus; the CAS covers that.)
	defer b.emitBuildLog(ctx, build.ID, "build started\n")

	// Build telemetry (ADR-030). Queue wait = time the build sat between
	// apid's CreateBuild (enqueued_at) and this dequeue point; observed
	// once here so only builds that actually started count. Build
	// duration is observed inside markSucceeded/markFailed (the single
	// choke points for every terminal path) using `buildStart` and the
	// outcome they decide. All observers are nil-safe (ops may be unset
	// in tests).
	b.ops.ObserveBuildQueueWait(time.Since(build.EnqueuedAt))
	buildStart := time.Now()

	fw, err := b.detector.Detect(dep.SourcePath)
	if err != nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureUserError, "framework detect: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	b.emitBuildLog(ctx, build.ID, "detected framework: "+string(fw)+"\n")

	// Cache check: content-addressed by sha256(source). A hit means we
	// produced this exact app layer before and can short-circuit the VM
	// spawn entirely (this is the ≥2× speedup gate, spec §14 M6).
	srcHash, err := hashFile(dep.SourcePath)
	if err != nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "source hash: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	if cached, ok := b.cache.Lookup(srcHash, fw); ok {
		b.emitBuildLog(ctx, build.ID, fmt.Sprintf("cache hit (%s, %d bytes) — skipping vm spawn\n", cached.Path, cached.Bytes))
		if err := b.store.SetDeploymentRootfs(ctx, dep.ID, cached.Path, sched.AppLayerKey(app.Slug, dep.ID), cached.Bytes); err != nil {
			b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "set rootfs: "+err.Error(), buildStart)
			return BuildResult{}, err
		}
		// imaged handles the cache-hit tarball the same as a fresh build:
		// it converts the OCI image into an app-layer ext4 and re-emits
		// NotifySnapshotPrime for schedd. The split (snapshot_boot for
		// imaged, snapshot_prime for schedd) avoids the race where schedd
		// tries to mount a .tar as a virtio-blk drive.
		if err := b.notif.Notify(ctx, db.NotifySnapshotBoot,
			fmt.Sprintf(`{"app_id":"%s","deployment_id":"%s"}`, app.ID, dep.ID)); err != nil {
			b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "notify prime: "+err.Error(), buildStart)
			return BuildResult{}, err
		}
		b.markSucceeded(ctx, build.ID, "cache_hit", buildStart)
		return BuildResult{BuildID: build.ID, LayerPath: cached.Path, LayerBytes: cached.Bytes, CacheHit: true}, nil
	}

	// Slot allocation (CLAUDE.md: builds never outrank tenant wakes).
	decider := b.slotDecide
	if decider == nil {
		decider = DecideSlot
	}
	slot := decider(b.resid, api.RAMAdmissionCeilingMB)
	if !slot.Allowed {
		// Requeue the row (NOT markFailed) so a later tick / notify
		// can re-attempt it. RequeueBuild clears started_at but
		// preserves enqueued_at so the FIFO position survives a
		// wake-surge. The build is observable as "queued" the
		// whole time — no false DeployFailed flip on the
		// deployment row. Best-effort: if the requeue itself
		// fails (Postgres restart, etc), the row is in a
		// running state with no live owner; the worker will
		// never see it again. PR-C follow-up: stuck-running
		// sweep (ADR-031).
		if err := b.store.RequeueBuild(ctx, build.ID); err != nil {
			b.log.Warn("builderd: requeue on no-slot", "build", build.ID, "err", err)
		}
		b.emitBuildLog(ctx, build.ID, fmt.Sprintf("no slot (%s) — requeued\n", slot.Reason))
		return BuildResult{}, ErrNoSlot
	}
	b.emitBuildLog(ctx, build.ID, fmt.Sprintf("allocated builder slot (%s)\n", slot.Label))

	if b.vm == nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "vm driver not wired (metal only)", buildStart)
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
		b.markFailed(ctx, dep.ID, build.ID, fc, "vm spawn: "+err.Error(), buildStart)
		return BuildResult{}, err
	}

	out, err := b.vm.WaitForCompletion(vmCtx, handle)
	if err != nil {
		// Translate a context-deadline to timeout-class; everything else is infra.
		fc := state.FailureInfra
		if errors.Is(err, context.DeadlineExceeded) {
			fc = state.FailureTimeout
		}
		b.markFailed(ctx, dep.ID, build.ID, fc, "vm wait: "+err.Error(), buildStart)
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
		b.markFailed(ctx, dep.ID, build.ID, fc, fmt.Sprintf("build exited %d", out.ExitCode), buildStart)
		return BuildResult{}, fmt.Errorf("builderd: vm exit %d", out.ExitCode)
	}

	// Enforce AppLayerMaxMB before stamping the rootfs or populating the
	// cache (spec §4.5: 256 / 512 / 1024 / 2048 MB per plan). Without this
	// gate a customer could pay for Hobby but ship a 2 GB app layer that
	// would silently bloat the per-VM memory.overhead accounting on the
	// next cold boot. Use the produced tarball's on-disk size — that's
	// the truth we'll snapshot, not LogTailBytes (which only counts the
	// in-VM build log tail).
	st, statErr := os.Stat(out.OCIImage)
	if statErr != nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "stat produced layer: "+statErr.Error(), buildStart)
		return BuildResult{}, statErr
	}
	acct, acctErr := b.store.AccountByID(ctx, app.AccountID)
	if acctErr != nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "load account: "+acctErr.Error(), buildStart)
		return BuildResult{}, acctErr
	}
	lim, known := api.LimitsFor(acct.Plan)
	if !known {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "unknown plan: "+string(acct.Plan), buildStart)
		return BuildResult{}, errors.New("builderd: unknown plan")
	}
	if sizeMB := (st.Size() + (1 << 20) - 1) >> 20; sizeMB > int64(lim.AppLayerMaxMB) {
		msg := fmt.Sprintf("app layer %d MB exceeds plan cap %d MB", sizeMB, lim.AppLayerMaxMB)
		b.markFailed(ctx, dep.ID, build.ID, state.FailureUserError, msg, buildStart)
		return BuildResult{}, errors.New("builderd: " + msg)
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
	if err := b.store.SetDeploymentRootfs(ctx, dep.ID, out.OCIImage, sched.AppLayerKey(app.Slug, dep.ID), out.LogTailBytes); err != nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "set rootfs: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	if err := b.notif.Notify(ctx, db.NotifySnapshotBoot,
		fmt.Sprintf(`{"app_id":"%s","deployment_id":"%s"}`, app.ID, dep.ID)); err != nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "notify prime: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	b.markSucceeded(ctx, build.ID, "ok", buildStart)
	return BuildResult{BuildID: build.ID, LayerPath: out.OCIImage, LayerBytes: out.LogTailBytes}, nil
}

// markSucceeded updates the build row to BuildSucceeded, finished=true.
// code is the ops_total{op="build"} label — "ok" for a fresh build or
// "cache_hit" for the cache short-circuit (ADR-030). Also observes the
// build_duration_seconds histogram with the matching `outcome` label,
// using buildStart as the wall-clock anchor (taken at ProcessOne's
// dequeue point).
func (b *Builderd) markSucceeded(ctx context.Context, buildID, code string, buildStart time.Time) {
	b.ops.ObserveBuildCount(code)
	b.ops.ObserveBuildDuration(code, time.Since(buildStart))
	if err := b.store.UpdateBuildStatus(ctx, buildID, state.BuildSucceeded, "", false, true); err != nil {
		b.log.Warn("builderd: mark succeeded", "build", buildID, "err", err)
	}
}

// markFailed updates the build row with a failure_class + error and finished=true,
// and flips the owning deployment to DeployFailed so the dashboard reflects
// reality (instead of leaving it stuck in DeployBuilding forever).
// The empty-string fc guard in pkg/state means a non-empty fc must be passed.
// Also observes build_duration_seconds with outcome="failed".
func (b *Builderd) markFailed(ctx context.Context, depID, buildID string, fc state.FailureClass, msg string, buildStart time.Time) {
	// ops_total{op="build",code=<fc>} — the §12 build-success ratio counts
	// everything except code="user_error" as a success (ADR-030).
	b.ops.ObserveBuildCount(string(fc))
	b.ops.ObserveBuildDuration("failed", time.Since(buildStart))
	b.log.Warn("builderd: build failed", "build", buildID, "deployment", depID, "failure_class", fc, "msg", msg)
	b.emitBuildLog(ctx, buildID, "FAILED: "+msg+"\n")
	if err := b.store.UpdateBuildStatus(ctx, buildID, state.BuildFailed, fc, false, true); err != nil {
		b.log.Warn("builderd: mark failed", "build", buildID, "err", err)
	}
	// Best-effort deployment status flip — mirrors imaged.transition
	// (pkg/imaged/handler.go:516). If this fails the build row is still
	// authoritative; the deployment row will be re-synced on the next
	// build attempt over the same row, or surfaced via the §17 G6
	// account-DR sweep.
	if err := b.store.UpdateDeploymentStatus(ctx, depID, state.DeployFailed, msg); err != nil {
		b.log.Warn("builderd: mark deployment failed", "deployment", depID, "build", buildID, "err", err)
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
