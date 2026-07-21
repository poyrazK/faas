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
	"log/slog"
	"math"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
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

// Run blocks until ctx is cancelled. It owns four event sources: the LISTEN
// subscriber, the GC tick, the one-shot FC sweep, and ctx.Done. Mirrors
// pkg/sched/loop.go::Run.
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
	var cancel func()
	if l.pool != nil {
		var err error
		notif, cancel, err = db.Subscribe(ctx, l.pool, []string{
			db.NotifyDeploymentChanged,
			db.NotifyBuildQueued,
			db.NotifySnapshotBoot,
			db.NotifySnapshotWritten,
			db.NotifyAppChanged,
		})
		if err != nil {
			return err
		}
		defer cancel()
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			if !ok {
				notif = nil
				continue
			}
			l.handler.HandleNotification(ctx, n)
		case <-l.gcCh:
			l.runGCTick(ctx, l.now())
		case <-l.fcCh:
			l.runFCSweep(ctx)
			// Drain the channel so the sweep fires exactly once.
			l.fcCh = nil
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

// runFCSweep is the F2 startup body. One-shot; runs once when the FC sweep
// channel fires. Errors are logged, never returned — FC detection failure
// must not block imaged startup (a degraded box still serves traffic).
func (l *Loop) runFCSweep(ctx context.Context) {
	if l.detectFC == nil {
		l.log.Warn("imaged: fc sweep skipped: no detectFC wired")
		return
	}
	ver, err := l.detectFC(ctx)
	if err != nil {
		l.log.Warn("imaged: fc detect", "err", err)
		return
	}
	n, err := l.handler.MarkFCSnapshotsStale(ctx, ver)
	if err != nil {
		l.log.Warn("imaged: fc sweep", "err", err)
		return
	}
	l.log.Info("imaged: fc sweep done", "fc_version", ver, "marked_stale", n)
}

// deleteSnapshotsAndFiles is the shared cleanup helper. Marks the IDs
// stale first (so schedd's per-row freshness check refuses them in the
// brief window between mark and delete), bulk-deletes the rows, then
// removes the on-disk artifacts best-effort.
func (l *Loop) deleteSnapshotsAndFiles(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := l.store.MarkOldSnapshotsStale(ctx, ids); err != nil {
		return err
	}
	if _, err := l.store.DeleteSnapshotsByID(ctx, ids); err != nil {
		return err
	}
	for _, id := range ids {
		dir := sched.SnapDir() + "/" + id
		if err := os.RemoveAll(dir); err != nil {
			l.log.Warn("imaged: gc remove snap dir", "path", dir, "err", err)
		}
		dep, err := l.store.DeploymentByID(ctx, id)
		if err != nil {
			continue
		}
		app, err := l.store.AppByID(ctx, dep.AppID)
		if err != nil {
			continue
		}
		ext4 := dep.RootfsPath
		if ext4 == "" {
			ext4 = l.appsRoot + "/" + app.Slug + "/" + dep.ID + ".ext4"
		}
		if err := os.Remove(ext4); err != nil && !os.IsNotExist(err) {
			l.log.Warn("imaged: gc remove ext4", "path", ext4, "err", err)
		}
	}
	return nil
}

