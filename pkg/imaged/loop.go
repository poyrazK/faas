// Package imaged — daemon loop. The Loop is the M8 readiness glue: it owns
// the LISTEN subscriber (notifications arrive as db.Notification), the
// nightly GC tick (spec §4.6: keep current+previous per app, fleet budget
// pressure evicts biggest accounts first), and the one-shot FC-version
// sweep (spec §4.4: "on FC upgrade, mark all snapshots stale", ADR-005).
//
// All filesystem + state mutation goes through Handler. The Loop only
// orchestrates when each subsystem acts.
package imaged

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Loop is the imaged M8 daemon loop. cmd/imaged constructs it after wiring
// the Handler's collaborators (store, notifier, OCI puller, builder).
type Loop struct {
	handler   *Handler
	store     state.Store
	pool      *pgxpool.Pool
	log       *slog.Logger
	now       func() time.Time
	lvUsedPct func(ctx context.Context) (float64, error)
	gcEvery   time.Duration // default 24h; tests shrink to ms
	detectFC  func(ctx context.Context) (string, error)
	appsRoot  string

	// Injected channels so tests never block on time.Sleep. Defaults are
	// built in NewLoop and can be overridden by WithGCChannel/WithFCSweepCh.
	gcCh <-chan time.Time
	fcCh <-chan struct{}
}

// LoopConfig bundles the dependencies NewLoop needs. Kept as a struct so
// tests can build it once with stub collaborators instead of threading six
// positional args through.
type LoopConfig struct {
	Handler   *Handler
	Store     state.Store
	Pool      *pgxpool.Pool
	Log       *slog.Logger
	Now       func() time.Time
	LvUsedPct func(ctx context.Context) (float64, error)
	DetectFC  func(ctx context.Context) (string, error)
	AppsRoot  string
	GCEvery   time.Duration
}

// NewLoop returns a Loop wired with sane defaults. The caller (cmd/imaged)
// supplies real collaborators; tests build a LoopConfig with fakes.
func NewLoop(cfg LoopConfig) *Loop {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if cfg.GCEvery == 0 {
		cfg.GCEvery = 24 * time.Hour
	}
	return &Loop{
		handler:   cfg.Handler,
		store:     cfg.Store,
		pool:      cfg.Pool,
		log:       cfg.Log,
		now:       cfg.Now,
		lvUsedPct: cfg.LvUsedPct,
		detectFC:  cfg.DetectFC,
		appsRoot:  cfg.AppsRoot,
		gcEvery:   cfg.GCEvery,
	}
}

// WithGCChannel swaps the GC tick channel. Used by tests to drive a
// deterministic tick boundary without sleeping.
func (l *Loop) WithGCChannel(ch <-chan time.Time) *Loop {
	if ch != nil {
		l.gcCh = ch
	}
	return l
}

// WithFCSweepCh swaps the one-shot FC sweep channel. Tests send on this
// channel to fire the sweep manually.
func (l *Loop) WithFCSweepCh(ch <-chan struct{}) *Loop {
	if ch != nil {
		l.fcCh = ch
	}
	return l
}

// Run blocks until ctx is cancelled. It owns three event sources: the LISTEN
// subscriber, the GC tick, the one-shot FC sweep, and ctx.Done. Mirrors
// pkg/sched/loop.go::Run.
//
// PR-B: builderd owns the build-queue durability surface now (its
// in-process worker polls with FOR UPDATE SKIP LOCKED). imaged no
// longer re-emits db.NotifyBuildQueued on a reap tick; only the
// deployment-side signals (NotifyDeploymentChanged,
// NotifySnapshotBoot, NotifySnapshotWritten, NotifyAppChanged) drive
// imaged's handlers.
//
// Tests can drive the loop without a pool by passing nil Pool; in that
// mode the LISTEN subscriber is skipped and the loop is purely ticker
// driven.
func (l *Loop) Run(ctx context.Context) error {
	if l.gcCh == nil {
		t := time.NewTicker(l.gcEvery)
		defer t.Stop()
		l.gcCh = t.C
	}
	if l.fcCh == nil {
		// One-shot FC sweep at startup. The channel yields exactly one
		// struct{} after construction.
		once := make(chan struct{}, 1)
		once <- struct{}{}
		l.fcCh = once
	}

	var notif <-chan db.Notification
	if l.pool != nil {
		var err error
		// F-11: switched from Subscribe to SubscribeWithReconnect — the
		// outer channel stays open across Postgres restarts so the daemon
		// keeps reacting to deploys/rolls/deletes instead of going silent
		// (the silent-LISTEN-close bug). The wrapper owns its own cancel
		// via its deferred goroutine; ctx cancel propagates.
		//
		// PR-B: NotifyBuildQueued is no longer here — builderd owns the
		// build-queue durability surface (in-process worker + SKIP
		// LOCKED) and is the single consumer of the build_queued
		// channel. imaged reacts to NotifyDeploymentChanged +
		// NotifySnapshotBoot for the deploy pipeline.
		notif, err = db.SubscribeWithReconnect(ctx, l.pool, []string{
			db.NotifyDeploymentChanged,
			db.NotifySnapshotBoot,
			db.NotifySnapshotWritten,
			db.NotifyAppChanged,
		}, l.log)
		if err != nil {
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			// F-11: outer channel never closes on conn drop (the wrapper
			// resubscribes internally). The only `ok==false` path is ctx
			// cancel. Leave the safety branch for paranoia.
			if !ok {
				return nil
			}
			l.handler.HandleNotification(ctx, n)
		case <-l.gcCh:
			l.runGCTick(ctx, l.now())
		case <-l.fcCh:
			// F-08: drain fcCh only on a successful sweep. A failed
			// detectFC leaves the buffered value on the channel so the
			// next select iteration retries the detect.
			if l.runFCSweep(ctx) {
				l.fcCh = nil
			}
		}
	}
}

// runGCTick is the F1 GC body. Always runs the per-app "current +
// previous" cleanup. When lv-fc usage is at or above the alarm threshold,
// also walks biggest accounts first until pressure is relieved.
func (l *Loop) runGCTick(ctx context.Context, now time.Time) {
	pct, err := l.lvUsedPct(ctx)
	pressure := err == nil && !math.IsNaN(pct) && pct >= api.SnapshotBudgetAlarmPct
	l.log.Info("imaged: gc tick",
		"now", now.Format(time.RFC3339),
		"lv_fc_pct", pct, "pressure", pressure)

	rows, err := l.store.ListSnapshotsForGC(ctx)
	if err != nil {
		l.log.Warn("imaged: gc list", "err", err)
		return
	}

	// Step A: per-app keep current+previous. Always runs.
	stale := perAppKeepCurrentPrevious(rows)
	if len(stale) > 0 {
		if err := l.deleteSnapshotsAndFiles(ctx, stale); err != nil {
			l.log.Warn("imaged: per-app gc", "err", err)
		}
	}
	if !pressure {
		return
	}

	// Step B: fleet budget pressure. Re-probe lv-fc after each delete;
	// exit when below threshold or no further evictions possible.
	for {
		pct, err = l.lvUsedPct(ctx)
		if err != nil || math.IsNaN(pct) || pct < api.SnapshotBudgetAlarmPct {
			return
		}
		rows, err = l.store.ListSnapshotsForGC(ctx)
		if err != nil {
			return
		}
		evicted := evictOldestFromHeaviestAccount(rows)
		if len(evicted) == 0 {
			l.log.Warn("imaged: pressure gc no candidates", "lv_fc_pct", pct)
			return
		}
		if err := l.deleteSnapshotsAndFiles(ctx, evicted); err != nil {
			l.log.Warn("imaged: pressure gc", "err", err)
			return
		}
	}
}

// runFCSweep is the F2 startup body. Returns true when the sweep ran
// to completion (whether or not anything was marked stale — an
// empty-stale-marker is still a successful run). Run() uses the return
// value to decide whether to drain fcCh; on failure the channel stays
// open so the next tick retries the detect call (F-08). Errors are
// logged, never returned — FC detection failure must not block imaged
// startup (a degraded box still serves traffic).
func (l *Loop) runFCSweep(ctx context.Context) bool {
	if l.detectFC == nil {
		l.log.Warn("imaged: fc sweep skipped: no detectFC wired")
		return false
	}
	ver, err := l.detectFC(ctx)
	if err != nil {
		// F-08: do not drain fcCh on error — next tick retries the
		// detect. A permanently-broken `firecracker` binary on PATH
		// produces repeated Warn logs; the daemon stays up so the
		// operator notices and fixes the path.
		l.log.Warn("imaged: fc detect", "err", err)
		return false
	}
	n, err := l.handler.MarkFCSnapshotsStale(ctx, ver)
	if err != nil {
		l.log.Warn("imaged: fc sweep mark", "err", err)
		return false
	}
	// F-07: also evict stale snapshots past the retention window. The
	// prior sweep only flipped stale=true, leaving rows on disk
	// indefinitely — disk leaks on every FC upgrade.
	evicted, err := l.store.DeleteSnapshotsStaleOlderThan(ctx, api.SnapshotStaleRetention)
	if err != nil {
		l.log.Warn("imaged: fc sweep evict", "err", err)
		return true // mark-stale succeeded; partial eviction still counts as progress
	}
	l.log.Info("imaged: fc sweep done",
		"fc_version", ver, "marked_stale", n, "evicted", evicted)
	return true
}

// deleteSnapshotsAndFiles is the shared cleanup helper. Takes the tuples
// produced by perAppKeepCurrentPrevious / evictOldestFromHeaviestAccount;
// each tuple carries the snap row id (for MarkOldSnapshotsStale /
// DeleteSnapshotsByID) and the deployment id (for the storage key
// under sched.SnapshotMemKey / sched.SnapshotVMStateKey). Marks the rows
// stale first so schedd's per-row freshness check refuses them in the
// brief mark→delete window, bulk-deletes the rows, then drops the
// on-disk artifacts via the Storage backend. F-05 fixes the prior
// snapshot-id/deployment-id namespace mismatch that prevented any
// filesystem cleanup from running.
//
// Issue #96 (ADR-025 axis 2) reframes cleanup as storage.Delete calls;
// the LocalStorageBackend swallows ErrNotFound so a transient race with
// schedd's restore path can't turn into an error. A future remote
// backend without LocalArtifactLister compatibility will need its own
// GC; we log + skip in that case (a remote registry has its own
// lifecycle).
func (l *Loop) deleteSnapshotsAndFiles(ctx context.Context, ts []deleteTarget) error {
	if len(ts) == 0 {
		return nil
	}
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	if _, err := l.store.MarkOldSnapshotsStale(ctx, ids); err != nil {
		return err
	}
	if _, err := l.store.DeleteSnapshotsByID(ctx, ids); err != nil {
		return err
	}
	be := l.handler.storageFor()
	for _, t := range ts {
		// snap blobs: delete both files; the storage backend swallows
		// missing keys so a transient race with restore is harmless.
		if err := be.Delete(ctx, state.SnapMemKey(t.DeploymentID)); err != nil {
			l.log.Warn("imaged: gc remove snap mem", "deployment", t.DeploymentID, "err", err)
		}
		if err := be.Delete(ctx, state.SnapVMStateKey(t.DeploymentID)); err != nil {
			l.log.Warn("imaged: gc remove snap vmstate", "deployment", t.DeploymentID, "err", err)
		}
		// Per-app ext4 (drive1) — derive the key the same way buildImageLayer
		// writes it. We need the app slug for the key; fall back to a
		// single DeploymentByID + AppByID lookup when the GC algorithms
		// (which don't carry the slug) returned an empty AppSlug.
		slug := t.AppSlug
		if slug == "" {
			dep, derr := l.store.DeploymentByID(ctx, t.DeploymentID)
			if derr == nil {
				app, aerr := l.store.AppByID(ctx, dep.AppID)
				if aerr == nil {
					slug = app.Slug
				}
			}
		}
		if slug != "" {
			if err := be.Delete(ctx, sched.AppLayerKey(slug, t.DeploymentID)); err != nil {
				l.log.Warn("imaged: gc remove ext4", "deployment", t.DeploymentID, "err", err)
			}
		}
	}
	// Best-effort: if the backend supports LocalArtifactLister (it does
	// in production via LocalStorageBackend + PrefixRouter) the next
	// nightly tick will see the smaller set without our help. We log a
	// debug-level hint when the backend isn't capable of list so the
	// remote-driver future is observable in the daemon's metrics.
	if _, ok := be.(storage.LocalArtifactLister); !ok {
		l.log.Warn("imaged: gc backend cannot list; rely on remote driver to reclaim space",
			"backend", fmt.Sprintf("%T", be))
	}
	return nil
}
