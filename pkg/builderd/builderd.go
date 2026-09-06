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
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/whycopy"
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
	// SourceWaitTimeout is how long a claimed build waits for its
	// source tarball to appear in the spool before requeueing.
	// The LISTEN/NOTIFY claim races the box-to-box spool sync
	// (pg_notify lands in ~ms; the tarball rsync needs ~1s), so on a
	// split-box fleet the notify-driven claim can observe the spool
	// dir before the file lands. Zero falls back to the 10s default
	// in New — the legacy instant-fail behaviour is gone.
	SourceWaitTimeout time.Duration `toml:"source_wait_timeout"`
	// ResidentProbeSocket is where builderd reaches schedd's residency
	// reporting. Empty disables the opportunistic 2nd slot.
	ResidentProbeSocket string `toml:"resident_probe_socket"`
	// MetricsAddr is the bind address for /metrics. Empty disables it.
	MetricsAddr string `toml:"metrics_addr"`
	// BuildTimeoutSeconds mirrors pkg/api/limits.go BuildTimeoutSeconds;
	// the per-build deadline. 0 falls back to the limit.
	BuildTimeoutSeconds int `toml:"build_timeout_seconds"`
	// FairnessWindow is the per-account claim-fairness lookback
	// (issue #196 B2.2). A claim that finds an account in
	// recent_build_claims within this window will skip that account's
	// queued builds and prefer a quieter one. If every queued
	// account is in the window, the claim falls back to FIFO so no
	// build is starved. Zero disables the fairness filter (worker
	// behaves like the pre-B2.2 FIFO claim). Default 30s; a longer
	// window trades queue latency for fairness.
	FairnessWindow time.Duration `toml:"fairness_window"`
	// BuilderNodeID is the compute_node name stamped onto every
	// provenance row this Builderd writes (ADR-038, Tier 3 / issue
	// #197 B3.1). Defaulted to "default-local" on the one-box by
	// cmd/builderd. Empty in unit tests; recordProvenance treats an
	// empty value as NOT-NULL-allowed-via-nullString so the
	// stamp is benign.
	BuilderNodeID string `toml:"builder_node_id"`
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
	// events is the pkg/events.Platform (issue #517 / PR-C /
	// ADR-064). When non-nil, the markSucceeded / markFailed
	// helpers emit wake.build_succeeded / wake.build_failed on
	// the events table. nil opts out (the unit-test default +
	// pre-PR-C fixtures). Mirrors schedd/vmmd/gatewayd-internal wiring.
	events *events.Platform
	// builderNodeID is the compute_node name builderd writes onto every
	// provenance row (ADR-038). Defaulted to "default-local" on the
	// one-box; cmd/builderd sets it from a Config field. test
	// fixtures can leave it empty; recordProvenance stamps empty
	// which pgstore's NULLIF / nullString roundtrip permits.
	builderNodeID string
	// slotDecide is the slot-allocation hook. Production leaves it nil
	// so acquireSlot uses DecideSlot; tests inject a closure to
	// exercise the no-slot requeue path without standing up a full
	// ResidencyProbe rig. nil falls back to DecideSlot(b.resid, …)
	// inside processClaimedBuild.
	slotDecide func(ResidencyProbe, int) SlotDecision
	// slotMu and activeSlots enforce the process-wide 1 guaranteed +
	// 1 opportunistic builder budget. DecideSlot only evaluates tenant
	// residency; it cannot account for another build racing through the
	// LISTEN path or the durable worker.
	slotMu      sync.Mutex
	activeSlots int
	// sourceStorage is the optional remote source handoff used by split-box
	// deployments. Local/single-box deployments leave it nil and continue to
	// read the source spool directly.
	sourceStorage storage.StorageBackend
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
	if cfg.SourceWaitTimeout == 0 {
		cfg.SourceWaitTimeout = 10 * time.Second
	}
	return &Builderd{
		store:         store,
		notif:         notif,
		vm:            vm,
		cache:         cache,
		detector:      det,
		resid:         resid,
		cfg:           cfg,
		log:           log,
		builderNodeID: cfg.BuilderNodeID,
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

// WithEvents attaches the events Platform (issue #517 / PR-C /
// ADR-064) and returns the same Builderd for chaining. Mirrors
// schedd.Engine.WithEvents / vmmd.Server.WithEvents. When non-nil,
// the markSucceeded / markFailed helpers emit
// wake.build_succeeded / wake.build_failed; nil opts out (the
// unit-test default + pre-PR-C fixtures).
func (b *Builderd) WithEvents(p *events.Platform) *Builderd {
	b.events = p
	return b
}

// WithSourceStorage attaches the optional source-archive backend used by a
// split-box deployment and returns the same Builderd for chaining. The
// backend is consulted only when the local source path is absent; this keeps
// local deployments on their existing filesystem path and makes retries
// idempotent after a successful materialization.
func (b *Builderd) WithSourceStorage(be storage.StorageBackend) *Builderd {
	b.sourceStorage = be
	return b
}

// withSlotDecider swaps the slot-allocation function for tests so the
// no-slot requeue path (PR-B §B.5) can be exercised without standing
// up a full ResidencyProbe rig. Unexported so production callers
// cannot reach it — DecideSlot is the canonical implementation and
// the build-path security invariant "builds never outrank tenant
// wakes" must not be bypassable from outside `package builderd`.
// Tests inside the package re-export it as `WithSlotDecider` via
// `pkg/builderd/testhelpers_test.go` (which is in `package builderd`,
// not `package builderd_test`, so the unexported field stays
// reachable).
func (b *Builderd) withSlotDecider(f func(ResidencyProbe, int) SlotDecision) *Builderd {
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
//
// B2.2 (issue #196): the claim switches to
// ClaimNextQueuedBuildWithFairness(cfg.FairnessWindow) which prefers
// accounts whose last claim is older than the window. Zero window
// disables the filter (falls back to FIFO via the CTE's
// "not exists" branch). The fairness record is written by
// processClaimedBuild after AppByID resolves the account — see
// that comment for the +1 SQL trip rationale.
func (b *Builderd) ProcessNext(ctx context.Context) (BuildResult, error) {
	var build state.Build
	var err error
	if b.cfg.FairnessWindow > 0 {
		build, err = b.store.ClaimNextQueuedBuildWithFairness(ctx, b.cfg.FairnessWindow)
	} else {
		build, err = b.store.ClaimNextQueuedBuild(ctx)
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return BuildResult{}, state.ErrNotFound
		}
		return BuildResult{}, fmt.Errorf("builderd: claim next build: %w", err)
	}
	return b.processClaimedBuild(ctx, build)
}

// stopIfBuildCancelled rejects all terminal or unobservable claims. The final
// CompleteBuild transaction also fences the claim's started_at, closing the
// check-to-publication race with cancellation, reaping, and requeueing.
func (b *Builderd) stopIfBuildCancelled(ctx context.Context, buildID string) bool {
	current, err := b.store.BuildByID(ctx, buildID)
	if err != nil {
		b.log.Warn("builderd: cannot verify running build", "build", buildID, "err", err)
		return true
	}
	if current.Status == state.BuildRunning {
		return false
	}
	b.emitBuildLog(ctx, buildID, "build no longer running — stopping\n")
	return true
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
	// Issue #197 B3.11: the cache key is partitioned by plan. A Hobby
	// customer's cached layer must not serve a Pro build (the layer
	// was built against the Hobby cap, not the Pro cap). Load the
	// account eagerly so the cache lookup below can include the plan.
	acct, acctErr := b.store.AccountByID(ctx, app.AccountID)
	if acctErr != nil {
		return BuildResult{}, fmt.Errorf("builderd: load account: %w", acctErr)
	}
	if b.stopIfBuildCancelled(ctx, build.ID) {
		return BuildResult{}, nil
	}

	// B2.2 (issue #196): record the claim so the next
	// ClaimNextQueuedBuildWithFairness round excludes this account
	// from the "fresh" set. We do this AFTER the deployment/app load
	// (which already paid for the round-trip) — adding a separate
	// SQL trip just for the record would be wasteful. The record is
	// best-effort: a failure here is logged at WARN and the claim
	// itself succeeds (losing one window of fairness, not the build).
	// When FairnessWindow == 0 the fairness path is fully disabled
	// and we skip the record to keep the SQL footprint identical to
	// pre-B2.2.
	if b.cfg.FairnessWindow > 0 && app.AccountID != "" {
		if recErr := b.store.RecordRecentBuildClaim(ctx, app.AccountID, build.ID); recErr != nil {
			b.log.Warn("builderd: record recent build claim",
				"build", build.ID, "account", app.AccountID, "err", recErr)
		}
	}

	// started_at was set by ClaimQueuedBuild; the legacy UpdateBuildStatus
	// call here would clobber it, so we skip it. (Previously this line
	// started_at = now() via UpdateBuildStatus; the CAS covers that.)
	//
	// Note: we deliberately do NOT emit a "build started" log here.
	// "started" is a property of the spawn (or cache hit) decision,
	// not the claim. The two real "started" sites below — the cache
	// hit path and the spawn path — each emit the line so a no-slot
	// requeue, a detect-failure markFailed, or a spawn failure never
	// log a misleading "build started" before any actual work
	// happened.

	// Build telemetry (ADR-030). buildStart anchors the
	// build_duration_seconds histogram observed inside
	// markSucceeded/markFailed (the single choke points for every
	// terminal path). The queue-wait observation is deferred until
	// past the slot decision so a no-slot requeue does not inflate
	// the histogram (PR-B review finding M-3). buildStart is set
	// here so every terminal funnel — including no-slot — has a
	// valid wall-clock anchor; it is overwritten with a more precise
	// value at the two real "started" sites (cache hit, spawn)
	// below to keep the duration histogram honest about the
	// cache/scratch distinction. ObserveBuild* methods are
	// nil-safe at the call sites (M-6 fix).
	buildStart := time.Now()

	// Split-box spool sync is eventually consistent: pg_notify lands
	// on builderd in ~ms, while the apid→compute-node rsync of the
	// source tarball needs ~1s. A notify-driven claim can therefore
	// observe the spool before the file exists and previously failed
	// the build outright. When SourceWaitTimeout > 0, wait for the
	// source to appear; if it still hasn't by the deadline, requeue
	// (same contract as ErrNoSlot: the row stays queued, the durable
	// worker re-claims it on the next tick).
	if err := b.materializeSource(ctx, build.ID, dep.SourcePath); err != nil {
		b.emitBuildLog(ctx, build.ID, fmt.Sprintf("source storage unavailable — requeued (%v)\n", err))
		if rerr := b.store.RequeueBuild(ctx, build.ID); rerr != nil {
			b.log.Warn("builderd: requeue on source-storage failure", "build", build.ID, "err", rerr)
		}
		return BuildResult{}, err
	}
	if b.cfg.SourceWaitTimeout > 0 {
		if err := b.waitForSource(ctx, build.ID, dep.SourcePath, b.cfg.SourceWaitTimeout); err != nil {
			b.emitBuildLog(ctx, build.ID, fmt.Sprintf("source spool lag — requeued (%v)\n", err))
			if rerr := b.store.RequeueBuild(ctx, build.ID); rerr != nil {
				b.log.Warn("builderd: requeue on source-lag", "build", build.ID, "err", rerr)
			}
			return BuildResult{}, err
		}
	}
	if b.stopIfBuildCancelled(ctx, build.ID) {
		return BuildResult{}, nil
	}

	fw, ver, err := b.detector.DetectWithVersionAtRoot(dep.SourcePath, dep.SourceRoot)
	if err != nil {
		b.markFailed(ctx, build, state.FailureUserError, "framework detect: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	b.emitBuildLog(ctx, build.ID, "detected framework: "+string(fw)+"\n")
	if ver != "" {
		b.emitBuildLog(ctx, build.ID, "inferred source-declared version: "+ver+"\n")
	}

	// Railpack must build FROM the same immutable runtime base that imaged
	// will use when it materialises the deployment layer. Without this handoff
	// Railpack starts from railpack-runtime and imaged correctly rejects the
	// resulting OCI chain as incompatible with the Gregale runner base.
	runtimeName := app.Runtime
	runtimeBaseRef, baseErr := resolveBuildRuntimeBaseRef(runtimeName, fw, os.Getenv)
	if baseErr != nil {
		b.markFailed(ctx, build, state.FailureInfra, "resolve runtime base: "+baseErr.Error(), buildStart)
		return BuildResult{}, baseErr
	}

	// The cache recipe includes the selected member as well as the complete
	// source context. Sibling apps can share archive bytes without sharing
	// their produced artifact. Keep srcHash itself for source provenance.
	srcHash, err := hashFile(dep.SourcePath)
	if err != nil {
		b.markFailed(ctx, build, state.FailureInfra, "source hash: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	if b.stopIfBuildCancelled(ctx, build.ID) {
		return BuildResult{}, nil
	}
	recipe := BuildCacheRecipe{
		SourceSHA256: srcHash, SourceRoot: dep.SourceRoot,
		Framework: fw, Plan: acct.Plan, RuntimeBaseRef: runtimeBaseRef,
	}
	buildEnvironment, cacheAvailable := b.resolveBuildEnvironment()
	if cacheAvailable {
		recipe.BuilderBaseIdentity = buildEnvironment.BuilderBaseIdentity
		recipe.TargetPlatform = buildEnvironment.TargetPlatform
	}
	if cached, ok := b.lookupCurrentCacheEntry(recipe, buildEnvironment, cacheAvailable, dep.ID); ok {
		if b.stopIfBuildCancelled(ctx, build.ID) {
			b.cache.ReleaseLease(cached.Path)
			return BuildResult{}, nil
		}
		// Cache hit is one of the two real "build started" sites
		// (the other is the spawn path below). Observe the
		// queue-wait here so a no-slot requeue on a sibling row
		// does not inflate the histogram with sub-second noise
		// (PR-B review finding M-3).
		b.ops.ObserveBuildQueueWait(time.Since(build.EnqueuedAt))
		buildStart = time.Now()
		b.emitBuildLog(ctx, build.ID, "build started (cache hit)\n")
		b.emitBuildLog(ctx, build.ID, fmt.Sprintf("cache hit (%s, %d bytes) — skipping vm spawn\n", cached.Path, cached.Bytes))
		completed, completeErr := b.completeBuild(ctx, build, dep, app, acct, srcHash, ver,
			BuildResult{BuildID: build.ID, LayerPath: cached.Path, LayerBytes: cached.Bytes, CacheHit: true}, buildStart)
		if completeErr != nil || completed.BuildID == "" {
			b.cache.ReleaseLease(cached.Path)
		}
		return completed, completeErr

	}

	// Slot allocation (CLAUDE.md: builds never outrank tenant wakes).
	slot, releaseSlot, acquired := b.acquireSlot()
	if !acquired {
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
	defer releaseSlot()
	b.emitBuildLog(ctx, build.ID, fmt.Sprintf("allocated builder slot (%s)\n", slot.Label))
	if b.stopIfBuildCancelled(ctx, build.ID) {
		return BuildResult{}, nil
	}

	// Past the slot decision — this is one of the two real "build
	// started" sites (the other is the cache hit path above).
	// Observe queue-wait here so a no-slot requeue doesn't
	// double-count it (PR-B review finding M-3).
	b.ops.ObserveBuildQueueWait(time.Since(build.EnqueuedAt))
	buildStart = time.Now()
	b.emitBuildLog(ctx, build.ID, "build started\n")

	if b.vm == nil {
		b.markFailed(ctx, build, state.FailureInfra, "vm driver not wired (metal only)", buildStart)
		return BuildResult{}, ErrNotMetal
	}

	timeout := time.Duration(b.cfg.BuildTimeoutSeconds) * time.Second
	vmCtx, cancel := context.WithTimeout(ctx, timeout)

	handle, err := b.vm.Spawn(vmCtx, VMRequest{
		BuildID:            build.ID,
		TenantID:           app.AccountID,
		DeploymentID:       dep.ID,
		SourcePath:         dep.SourcePath,
		SourceRoot:         dep.SourceRoot,
		Framework:          fw,
		Runtime:            runtimeName,
		RuntimeBaseRef:     runtimeBaseRef,
		DependencyCacheKey: dependencyCacheKeyForApp(app, fw, dep.SourceRoot, runtimeBaseRef),
		LogPath:            dep.LogPath,
		RAMMB:              api.BuildVMRAMMB,
		TimeoutSec:         b.cfg.BuildTimeoutSeconds,
		Plan:               string(acct.Plan),
	})
	if err != nil {
		// Translate a context-deadline to timeout-class; everything else is infra.
		fc := state.FailureInfra
		if errors.Is(err, context.DeadlineExceeded) {
			fc = state.FailureTimeout
		}
		b.markFailed(ctx, build, fc, "vm spawn: "+err.Error(), buildStart)
		cancel()
		return BuildResult{}, err
	}
	// Spawn only needs the build-scoped deadline while vmmd accepts the cold
	// boot. Do not carry that deadline into WaitForCompletion: Destroy must be
	// allowed to use its export headroom to collect build-done.json and the OCI
	// tarball after the in-guest build reaches its own timeout.
	cancel()
	if handle.DependencyCacheKey != "" {
		if handle.DependencyCacheRestored {
			b.emitBuildLog(ctx, build.ID, "dependency cache restored — reusing matching install layers\n")
		} else {
			b.emitBuildLog(ctx, build.ID, "dependency cache cold — installing dependencies\n")
		}
	}

	watchCtx, stopWatch := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			current, readErr := b.store.BuildByID(watchCtx, build.ID)
			if readErr == nil && (current.Status != state.BuildRunning || !current.StartedAt.Equal(build.StartedAt)) {
				cancelCtx, cancelVM := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
				err := b.vm.Cancel(cancelCtx, build.ID)
				cancelVM()
				if err == nil {
					return
				}
				b.log.Warn("builderd: interrupt stale build", "build", build.ID, "err", err)
			}
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	out, err := b.vm.WaitForCompletion(ctx, handle)
	stopWatch()
	<-watchDone
	if err != nil {
		// Translate a context-deadline to timeout-class; everything else is infra.
		fc := state.FailureInfra
		if errors.Is(err, context.DeadlineExceeded) {
			fc = state.FailureTimeout
		}
		b.markFailed(ctx, build, fc, "vm wait: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	if b.stopIfBuildCancelled(ctx, build.ID) {
		return BuildResult{}, nil
	}
	if out.DependencyCacheStoreError != "" {
		b.log.Warn("builderd: dependency cache store failed (continuing)", "build", build.ID, "err", out.DependencyCacheStoreError)
		b.emitBuildLog(ctx, build.ID, "dependency cache could not be saved — the next sync may reinstall dependencies\n")
	} else if out.DependencyCacheStored {
		b.emitBuildLog(ctx, build.ID, "dependency cache saved for the next developer sync\n")
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
		// Error-explanations cluster (spec §6.4 amendment 1): when
		// classifyBuildFailure populated a typed RFC 7807 code (the
		// BuildDone.FailureCode that guest-init stamps on the build
		// manifest), lift the whycopy prose + persist hint/why/fix
		// on the deployment row so `gregale inspect <slug> --errors`
		// surfaces the post-mortem. The legacy markFailed path stays
		// for the coarse-grained exit-code-only case (no code
		// emitted) — that's the path the `failure_class` UX copy
		// already covers; markFailedEx is the typed-code addition.
		if out.FailureCode != "" {
			b.markFailedEx(ctx, build, fc, out.FailureCode, out.FailurePkg, fmt.Sprintf("build exited %d", out.ExitCode), buildStart)
		} else {
			b.markFailed(ctx, build, fc, fmt.Sprintf("build exited %d", out.ExitCode), buildStart)
		}
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
		b.markFailed(ctx, build, state.FailureInfra, "stat produced layer: "+statErr.Error(), buildStart)
		return BuildResult{}, statErr
	}
	artifactBytes := st.Size()
	lim, known := api.LimitsFor(acct.Plan)
	if !known {
		b.markFailed(ctx, build, state.FailureInfra, "unknown plan: "+string(acct.Plan), buildStart)
		return BuildResult{}, errors.New("builderd: unknown plan")
	}
	if sizeMB := (st.Size() + (1 << 20) - 1) >> 20; sizeMB > int64(lim.AppLayerMaxMB) {
		msg := fmt.Sprintf("app layer %d MB exceeds plan cap %d MB", sizeMB, lim.AppLayerMaxMB)
		b.markFailed(ctx, build, state.FailureUserError, msg, buildStart)
		return BuildResult{}, errors.New("builderd: " + msg)
	}
	if b.stopIfBuildCancelled(ctx, build.ID) {
		return BuildResult{}, nil
	}

	result, err := b.completeBuild(ctx, build, dep, app, acct, srcHash, ver,
		BuildResult{BuildID: build.ID, LayerPath: out.OCIImage, LayerBytes: artifactBytes}, buildStart)
	if err != nil || result.BuildID == "" {
		return result, err
	}
	// Only cache a successfully committed build produced by the same builder
	// environment observed before VM launch. A concurrent base restage leaves
	// the successful deployment intact but deliberately skips cache publication.
	if cacheAvailable && b.buildEnvironmentStillCurrent(buildEnvironment) {
		if err := b.cache.StoreBuild(recipe, out.OCIImage, artifactBytes); err != nil {
			b.log.Warn("builderd: cache store failed (continuing)", "err", err)
		}
	}
	return result, nil

}

func (b *Builderd) resolveBuildEnvironment() (BuildEnvironment, bool) {
	environment, err := currentBuildEnvironment(b.vm)
	if err != nil {
		b.log.Warn("builderd: build cache unavailable; builder environment identity is not current", "err", err)
		return BuildEnvironment{}, false
	}
	return environment, true
}

func (b *Builderd) buildEnvironmentStillCurrent(want BuildEnvironment) bool {
	have, err := currentBuildEnvironment(b.vm)
	if err != nil {
		b.log.Warn("builderd: build cache skipped; builder environment identity is unavailable", "err", err)
		return false
	}
	if have != want {
		b.log.Warn("builderd: build cache skipped; builder environment changed",
			"before", want.BuilderBaseIdentity, "after", have.BuilderBaseIdentity,
			"before_platform", want.TargetPlatform, "after_platform", have.TargetPlatform)
		return false
	}
	return true
}

func (b *Builderd) lookupCurrentCacheEntry(recipe BuildCacheRecipe, environment BuildEnvironment, available bool, deploymentID string) (CacheEntry, bool) {
	if !available {
		return CacheEntry{}, false
	}
	cached, ok, err := b.cache.LeaseBuild(recipe, deploymentID)
	if err != nil {
		b.log.Warn("builderd: build cache lease failed; rebuilding", "deployment", deploymentID, "err", err)
		return CacheEntry{}, false
	}
	if !ok {
		return CacheEntry{}, false
	}
	if !b.buildEnvironmentStillCurrent(environment) {
		b.cache.ReleaseLease(cached.Path)
		return CacheEntry{}, false
	}
	return cached, true
}

// snapshotBootPayload identifies the compute node that produced the local
// OCI export. snapshot_boot is a fleet-wide PostgreSQL notification, but the
// rootfs_path it carries indirectly is node-local until imaged publishes the
// app layer. Including the builder identity lets each imaged daemon discard
// work owned by a sibling node instead of opening a path it cannot see.
//
// An empty builderNodeID preserves the single-box/test shape and omits the
// optional field for backwards compatibility with existing fixtures.
func (b *Builderd) snapshotBootPayload(appID, deploymentID string) string {
	payload := fmt.Sprintf(`{"app_id":"%s","deployment_id":"%s"}`, appID, deploymentID)
	if b.builderNodeID == "" {
		return payload
	}
	return fmt.Sprintf(`{"app_id":"%s","deployment_id":"%s","node_id":"%s"}`,
		appID, deploymentID, b.builderNodeID)
}

// observeSucceeded records best-effort telemetry after CompleteBuild commits.
func (b *Builderd) observeSucceeded(ctx context.Context, buildID, code string, buildStart time.Time) {
	b.ops.ObserveBuildCount(code)
	b.ops.ObserveBuildDuration(code, time.Since(buildStart))
	b.recordBuilderUsage(ctx, buildID, buildStart, string(state.BuildSucceeded))
	b.emitBuildSucceeded(ctx, buildID, time.Since(buildStart))
}

// emitBuildSucceeded writes the wake.build_succeeded row. Extracted
// from observeSucceeded so the lookup logic stays clear of the build
// row's UpdateBuildStatus / recordBuilderUsage bookkeeping. The
// function is best-effort: every failure path is logged and
// counter'd; we never panic and never roll back the build row.
func (b *Builderd) emitBuildSucceeded(ctx context.Context, buildID string, duration time.Duration) {
	if b.events == nil {
		return
	}
	build, err := b.store.BuildByID(ctx, buildID)
	if err != nil {
		b.log.Warn("builderd: emit build_succeeded lookup", "build", buildID, "err", err)
		return
	}
	dep, err := b.store.DeploymentByID(ctx, build.DeploymentID)
	if err != nil {
		b.log.Warn("builderd: emit build_succeeded deployment lookup", "build", buildID, "err", err)
		return
	}
	b.events.Emit(ctx, events.BuildSucceeded{
		EmitAt:       time.Now().UTC(),
		AppID:        dep.AppID,
		DeploymentID: build.DeploymentID,
		ImageDigest:  dep.ImageDigest,
		DurationMs:   duration.Milliseconds(),
	})
}

// markFailed updates the build row with a failure_class + error and finished=true,
// and flips the owning deployment to DeployFailed so the dashboard reflects
// reality (instead of leaving it stuck in DeployBuilding forever).
// The empty-string fc guard in pkg/state means a non-empty fc must be passed.
// Also observes build_duration_seconds with outcome="failed".
func (b *Builderd) markFailed(ctx context.Context, claim state.Build, fc state.FailureClass, msg string, buildStart time.Time) bool {
	depID, buildID := claim.DeploymentID, claim.ID
	if err := b.store.FailBuild(ctx, claim, fc, msg); err != nil {
		b.log.Warn("builderd: mark failed", "build", buildID, "err", err)
		return false
	}

	// ops_total{op="build",code=<fc>} — the §12 build-success ratio counts
	// everything except code="user_error" as a success (ADR-030).
	b.ops.ObserveBuildCount(string(fc))
	b.ops.ObserveBuildDuration("failed", time.Since(buildStart))
	b.log.Warn("builderd: build failed", "build", buildID, "deployment", depID, "failure_class", fc, "msg", msg)
	b.emitBuildLog(ctx, buildID, "FAILED: "+msg+"\n")
	// ADR-117 §3: stamp the active stage as failed so the SSE
	// `event: stage` consumer on /v1/deployments/{id}/logs emits
	// `status:"failed"` for the row that was in flight when the
	// build blew up. MarkDeploymentStageFailed (PR-A review fix)
	// moves the active stage into history with the failed stamp
	// rather than overwriting `history[len-1]` (which would have
	// been the previously-closed stage, not the one in flight).
	// Best-effort: stage stamps never roll back the deployment
	// status flip above. The deployment lookup matches the one
	// in emitBuildSucceeded (line 619) so we don't have a second
	// store reader.
	if _, serr := b.store.MarkDeploymentStageFailed(ctx, depID, time.Now(), msg); serr != nil {
		b.log.Warn("builderd: stamp failed stage", "deployment", depID, "err", serr)
	}
	// ADR-048 §4: builder-time metering on terminal build
	// events, success or failure — the box burned cycles
	// either way. The AppendBuilderUsage call is idempotent on
	// (build_id) so a redelivered webhook / meterd restart /
	// sweeper re-flip is a no-op.
	b.recordBuilderUsage(ctx, buildID, buildStart, string(state.BuildFailed))
	// issue #517 / PR-C / ADR-064 — emit wake.build_failed on
	// the events table. The build row is already stamped
	// failed + the deployment is already flipped to
	// DeployFailed; the timeline gets the typed counterpart
	// alongside the legacy audit row. Best-effort: failures
	// are logged + counter'd, never rolled back.
	b.emitBuildFailed(ctx, buildID, string(fc), msg)
	return true
}

// markFailedEx is the error-explanations cluster (spec §6.4 amendment 1)
// counterpart of markFailed. It carries the typed RFC 7807 code that
// classifyBuildFailure lifted from build-done.json (one of
// app_arch_mismatch / dep_install_failed) plus the package manager
// discriminator for dep_install_failed (npm / pip / go / cargo). The
// whycopy catalog is consulted for hint/why/fix prose and persisted
// on the deployment row via SetDeploymentFailedEx so `gregale inspect
// <slug> --errors` surfaces the customer-facing post-mortem.
//
// The call still funnels through the legacy markFailed path for the
// build-row updates (UpdateBuildStatus, UpdateDeploymentStatus,
// recordBuilderUsage, emitBuildFailed) — those are unaffected by the
// typed-code addition. The only difference is the deployment row
// gets error_code + error_hint/why/fix/relevant_logs stamped in
// place of the bare error text flip.
//
// pkg is the package-manager discriminator for dep_install_failed
// (the only cluster code that has one); empty for all other codes.
// It flows through as the whycopy.Observed argument so the catalog
// can render "npm install failed" vs "pip install failed" copy.
func (b *Builderd) markFailedEx(ctx context.Context, claim state.Build, fc state.FailureClass, code, pkg, msg string, buildStart time.Time) {
	depID, buildID := claim.DeploymentID, claim.ID
	// 1. Run the legacy build-row / counters / metering path unchanged
	// so all downstream observers (metrics, dashboard, build row
	// table) see the same shape as a non-typed-code failure.
	if !b.markFailed(ctx, claim, fc, msg, buildStart) {
		return
	}
	// 2. Lift the customer-facing prose from the whycopy catalog.
	// Decorate is a no-op when the catalog has no row for the code —
	// the legacy deployment row stays with the bare error text flip.
	problem := &api.Problem{
		Code:   code,
		Status: 422,
		Title:  code,
		Detail: msg,
	}
	var observed any
	if pkg != "" {
		observed = pkg
	}
	_ = whycopy.Decorate(problem, code, observed)
	// 3. Persist the prose on the deployment row. Best-effort: a
	// failure here is logged but does not block the legacy
	// markFailed counters / metering / events — the deployment row
	// will be re-synced on the next build attempt over the same row.
	if _, err := b.store.SetDeploymentFailedEx(ctx, depID, code, msg, problem.Hint, problem.Why, problem.Fix, nil); err != nil {
		b.log.Warn("builderd: stamp deployment failed (ex)", "deployment", depID, "build", buildID, "code", code, "err", err)
	}
}

// emitBuildFailed writes the wake.build_failed row. Mirrors
// emitBuildSucceeded in lookup shape + best-effort semantics.
// The reason parameter is "<fc>: <msg>" so the timeline surfaces
// the failure class + the original builderd error in one field.
func (b *Builderd) emitBuildFailed(ctx context.Context, buildID, fc, msg string) {
	if b.events == nil {
		return
	}
	build, err := b.store.BuildByID(ctx, buildID)
	if err != nil {
		b.log.Warn("builderd: emit build_failed lookup", "build", buildID, "err", err)
		return
	}
	dep, err := b.store.DeploymentByID(ctx, build.DeploymentID)
	if err != nil {
		b.log.Warn("builderd: emit build_failed deployment lookup", "build", buildID, "err", err)
		return
	}
	b.events.Emit(ctx, events.BuildFailed{
		EmitAt:       time.Now().UTC(),
		AppID:        dep.AppID,
		DeploymentID: build.DeploymentID,
		ImageDigest:  dep.ImageDigest,
		Reason:       fc + ": " + msg,
	})
}

// recordBuilderUsage is the ADR-048 §4 helper: looks up the
// build → deployment → app → account chain and stamps one
// builder_usage row at build completion. Best-effort — a
// failure logs Warn but does NOT propagate; the build row
// stays authoritative for the dashboard. Caller has already
// stamped the build status before this helper runs.
func (b *Builderd) recordBuilderUsage(ctx context.Context, buildID string, buildStart time.Time, kind string) {
	build, err := b.store.BuildByID(ctx, buildID)
	if err != nil {
		b.log.Warn("builderd: builder-usage lookup", "build", buildID, "err", err)
		return
	}
	dep, err := b.store.DeploymentByID(ctx, build.DeploymentID)
	if err != nil {
		b.log.Warn("builderd: builder-usage deployment lookup",
			"build", buildID, "deployment", build.DeploymentID, "err", err)
		return
	}
	app, err := b.store.AppByID(ctx, dep.AppID)
	if err != nil {
		b.log.Warn("builderd: builder-usage app lookup",
			"build", buildID, "app", dep.AppID, "err", err)
		return
	}
	finishedAt := time.Now()
	seconds := int64(finishedAt.Sub(buildStart).Seconds())
	if seconds < 0 {
		// Clock skew or buildStart stamped in the future;
		// clamp to 0 so the SUM rollup never inflates with
		// a negative delta.
		seconds = 0
	}
	if err := b.store.AppendBuilderUsage(ctx, app.AccountID, app.ID, buildID, finishedAt, string(build.Kind), seconds); err != nil {
		b.log.Warn("builderd: append builder usage", "build", buildID, "err", err)
	}
}

// completeBuild commits the claim, artifact and provenance before publishing
// any success signal. imaged polls the committed records if NOTIFY is lost.
func (b *Builderd) completeBuild(ctx context.Context, build state.Build, dep state.Deployment, app state.App, acct state.Account, srcSHA, frameworkVer string, result BuildResult, buildStart time.Time) (BuildResult, error) {
	prov := state.BuildProvenance{
		BuildID: build.ID, SourceSHA256: srcSHA, SourceURL: dep.SourceURL,
		CommitSHA: dep.CommitSHA, Plan: string(acct.Plan), BuilderNodeID: b.builderNodeID,
		StartedAt: build.StartedAt, FinishedAt: time.Now(), FrameworkVer: frameworkVer,
	}
	if err := b.store.CompleteBuild(ctx, build, result.LayerPath, sched.AppLayerKey(app.Slug, dep.ID), result.LayerBytes, prov); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return BuildResult{}, nil
		}
		b.ops.ObserveProvenanceWrite("error")
		return BuildResult{}, fmt.Errorf("builderd: commit build completion: %w", err)
	}
	b.ops.ObserveProvenanceWrite("ok")
	code := "ok"
	if result.CacheHit {
		code = "cache_hit"
	}
	b.observeSucceeded(ctx, build.ID, code, buildStart)
	if b.notif != nil {
		if err := b.notif.Notify(ctx, db.NotifySnapshotBoot, b.snapshotBootPayload(app.ID, dep.ID)); err != nil {
			b.log.Warn("builderd: image notification failed; durable imaged poll will recover", "build", build.ID, "err", err)
		}
	}
	return result, nil
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

// materializeSource downloads a split-box source archive into the local
// source spool when the apid-created path is not present. A missing remote
// object is intentionally not an error here: waitForSource retries the
// remote lookup within the existing bounded source-lag policy, while
// registry failures are returned so the caller can requeue immediately.
func (b *Builderd) materializeSource(ctx context.Context, buildID, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("builderd: stat source: %w", err)
	}
	if b.sourceStorage == nil {
		return nil
	}
	rc, err := b.sourceStorage.Get(ctx, "sources/"+buildID+".tar.gz")
	if err != nil {
		if storage.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("builderd: fetch source archive: %w", err)
	}
	defer func() { _ = rc.Close() }()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("builderd: create source spool: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".source-download-*")
	if err != nil {
		return fmt.Errorf("builderd: create source temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("builderd: download source archive: %w", err)
	}
	if err := tmp.Chmod(0o640); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("builderd: chmod source archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("builderd: close source archive: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("builderd: install source archive: %w", err)
	}
	return nil
}

// waitForSource blocks until the source tarball at path exists or the
// timeout expires. It is the split-box spool-sync guard: the
// notify-driven claim can beat both the rsync to the compute node and
// eventual visibility of the OCI manifest. Local spool checks run at
// 100ms; remote not-found checks run at 500ms so a transient registry
// visibility delay is absorbed without a tight registry polling loop.
func (b *Builderd) waitForSource(ctx context.Context, buildID, path string, timeout time.Duration) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("builderd: stat source: %w", err)
	}
	deadline := time.Now().Add(timeout)
	localPoll := time.NewTicker(100 * time.Millisecond)
	defer localPoll.Stop()
	var remotePoll *time.Ticker
	var remoteC <-chan time.Time
	if b.sourceStorage != nil {
		remotePoll = time.NewTicker(500 * time.Millisecond)
		defer remotePoll.Stop()
		remoteC = remotePoll.C
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-localPoll.C:
			if _, err := os.Stat(path); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("builderd: stat source: %w", err)
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("builderd: source %s did not appear within %s", path, timeout)
			}
		case <-remoteC:
			if err := b.materializeSource(ctx, buildID, path); err != nil {
				return err
			}
			if _, err := os.Stat(path); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("builderd: stat source: %w", err)
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("builderd: source %s did not appear within %s", path, timeout)
			}
		}
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
