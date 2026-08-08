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
	"github.com/onebox-faas/faas/pkg/events"
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
	// pre-PR-C fixtures). Mirrors schedd/vmmd/gatewayd wiring.
	events *events.Platform
	// builderNodeID is the compute_node name builderd writes onto every
	// provenance row (ADR-038). Defaulted to "default-local" on the
	// one-box; cmd/builderd sets it from a Config field. test
	// fixtures can leave it empty; recordProvenance stamps empty
	// which pgstore's NULLIF / nullString roundtrip permits.
	builderNodeID string
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

	fw, ver, err := b.detector.DetectWithVersion(dep.SourcePath)
	if err != nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureUserError, "framework detect: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	b.emitBuildLog(ctx, build.ID, "detected framework: "+string(fw)+"\n")
	if ver != "" {
		b.emitBuildLog(ctx, build.ID, "inferred source-declared version: "+ver+"\n")
	}

	// Cache check: content-addressed by sha256(source). A hit means we
	// produced this exact app layer before and can short-circuit the VM
	// spawn entirely (this is the ≥2× speedup gate, spec §14 M6).
	srcHash, err := hashFile(dep.SourcePath)
	if err != nil {
		b.markFailed(ctx, dep.ID, build.ID, state.FailureInfra, "source hash: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	if cached, ok := b.cache.Lookup(srcHash, fw, acct.Plan); ok {
		// Cache hit is one of the two real "build started" sites
		// (the other is the spawn path below). Observe the
		// queue-wait here so a no-slot requeue on a sibling row
		// does not inflate the histogram with sub-second noise
		// (PR-B review finding M-3).
		b.ops.ObserveBuildQueueWait(time.Since(build.EnqueuedAt))
		buildStart = time.Now()
		b.emitBuildLog(ctx, build.ID, "build started (cache hit)\n")
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
		// ADR-038: stamp provenance BEFORE markSucceeded so the
		// build row + provenance row land within the same critical
		// section from the customer's perspective. The populator
		// reads build.StartedAt (set by ClaimQueuedBuild) and
		// finishedAt = time.Now() (this markSucceeded hasn't
		// stamped finished_at yet). Best-effort; failure logs at
		// WARN inside recordProvenance.
		b.recordProvenance(ctx, build, dep, app, acct, srcHash, true, ver)
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

	// Past the slot decision — this is one of the two real "build
	// started" sites (the other is the cache hit path above).
	// Observe queue-wait here so a no-slot requeue doesn't
	// double-count it (PR-B review finding M-3).
	b.ops.ObserveBuildQueueWait(time.Since(build.EnqueuedAt))
	buildStart = time.Now()
	b.emitBuildLog(ctx, build.ID, "build started\n")

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
	if err := b.cache.Store(srcHash, fw, acct.Plan, out.OCIImage, out.LogTailBytes); err != nil {
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
	// ADR-038: stamp provenance BEFORE markSucceeded. Same shape as
	// the cache-hit branch above; empty buildkit_version +
	// railpack_version + base_digest + runner_digest + sbom_storage_key
	// (Phase 3 populator fills them in).
	b.recordProvenance(ctx, build, dep, app, acct, srcHash, false, ver)
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
	// ADR-048 §4: best-effort builder-time metering. The
	// lookup can fail (DB outage, race with another writer) — a
	// dropped row loses this build's telemetry but does NOT
	// affect the build outcome (already stamped succeeded).
	b.recordBuilderUsage(ctx, buildID, buildStart, string(state.BuildSucceeded))
	// issue #517 / PR-C / ADR-064 — emit wake.build_succeeded
	// on the events table. The build row + the deployment row
	// are both already in scope (UpdateBuildStatus just
	// succeeded; DeploymentByID reads through the same Store).
	// Best-effort: a failure here is logged at Warn + counted on
	// wake_phase_emitted_total{result="failed"}; it does NOT
	// roll back the build row (the build is already terminal).
	b.emitBuildSucceeded(ctx, buildID, time.Since(buildStart))
}

// emitBuildSucceeded writes the wake.build_succeeded row. Extracted
// from markSucceeded so the lookup logic stays clear of the build
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

// recordProvenance is the ADR-038 populator. Called from the two
// markSucceeded sites in processClaimedBuild (cache-hit path + fresh-
// build path). Stamps build_provenance with the "what ran?" record:
// source SHA, account plan, deployment URL/commit, builder node ID,
// and the build's started_at / finished_at timestamps. Empty fields
// (buildkit_version, railpack_version, base_digest, runner_digest,
// sbom_storage_key) are populated by Phase 3 (cosign sign + syft
// SBOM); the columns exist today so Phase 3 is a zero-cost schema
// change.
//
// Best-effort: a failed INSERT logs at WARN. The build itself still
// succeeds — the builds row is the authoritative customer-visible
// success/fail transition; provenance is observational metadata.
// The reader (apid GET /v1/builds/{id}/provenance) renders 404
// when this INSERT didn't land, surfacing the failure as a
// missing-provenance 404 rather than a build-succeeded-with-
// no-evidence.
//
// finishedAt may be zero in the cache-hit path because the row's
// finished_at is stamped inside UpdateBuildStatus, which runs in
// markSucceeded AFTER this call returns. We use time.Now() in that
// case so the record lands with a non-zero finished_at; the next
// call (none expected — this is a one-shot per build) would
// overwrite.
func (b *Builderd) recordProvenance(ctx context.Context, build state.Build, dep state.Deployment, app state.App, acct state.Account, srcSHA string, isCacheHit bool, frameworkVer string) {
	finishedAt := build.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	prov := state.BuildProvenance{
		BuildID:        build.ID,
		BuildkitVer:    "",
		RailpackVer:    "",
		BaseDigest:     "",
		SourceSHA256:   srcSHA,
		SourceURL:      dep.SourceURL,
		CommitSHA:      dep.CommitSHA,
		Plan:           string(acct.Plan),
		RunnerDigest:   "",
		BuilderNodeID:  b.builderNodeID,
		StartedAt:      build.StartedAt,
		FinishedAt:     finishedAt,
		SBOMStorageKey: "",
		FrameworkVer:   frameworkVer,
	}
	if isCacheHit {
		// Cache-hit builds have empty started_at on the row in the rare
		// path where ClaimQueuedBuild set started_at=now BUT the
		// caller's UpdateBuildStatus(stamp_finished=true) under markSucceeded
		// hasn't run yet. We keep the read of build.StartedAt so a
		// redelivered path sees the value post-claim; the only
		// fallback needed is for finished_at, not started_at.
		_ = isCacheHit // field documented in ADR-038 §provenance row contents
	}
	if err := b.store.CreateBuildProvenance(ctx, prov); err != nil {
		b.ops.ObserveProvenanceWrite("error")
		b.log.Warn("builderd: record provenance failed (build still succeeded)", "build", build.ID, "err", err)
		return
	}
	b.ops.ObserveProvenanceWrite("ok")
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
