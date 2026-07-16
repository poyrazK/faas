// Package sched — daemon glue that translates pg_notify events into ledger
// updates and instance state writes. schedd is the sole writer to the
// instances table (spec §Component ownership); this file owns the loop that
// reacts to apid's notifications and drives the reaper tick. All instance
// mutation (create, transition, snapshot, destroy) goes through the Engine —
// the Loop is pure glue that decides *when* to act, not *how*.
package sched

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Loop subscribes to the pg_notify channels schedd cares about and reacts. It
// runs the idle reaper on a 10 s tick and cron on a 60 s tick (spec §4.3). The
// Engine holds the store, ledger, and vmmd client; the Loop only orchestrates.
type Loop struct {
	pool   *pgxpool.Pool
	engine *Engine
	log    *slog.Logger
}

func NewLoop(pool *pgxpool.Pool, engine *Engine, log *slog.Logger) *Loop {
	return &Loop{pool: pool, engine: engine, log: log}
}

// Run blocks until ctx is cancelled. It owns three event sources: the LISTEN
// subscriber, the reaper tick, and the cron tick.
func (l *Loop) Run(ctx context.Context) error {
	notif, cancel, err := db.Subscribe(ctx, l.pool, []string{
		db.NotifyAppChanged,
		db.NotifyDeploymentChanged,
		db.NotifySnapshotPrime,
	})
	if err != nil {
		return err
	}
	defer cancel()

	reaperT := time.NewTicker(10 * time.Second)
	defer reaperT.Stop()
	cronT := time.NewTicker(60 * time.Second)
	defer cronT.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			if !ok {
				return nil
			}
			l.handleNotification(ctx, n)
		case <-reaperT.C:
			l.runReaper(ctx)
		case <-cronT.C:
			l.runCronTick(ctx)
		}
	}
}

// handleNotification decodes the JSON payload and applies the policy.
//
//   - app_changed / deployment_changed: informational. Wake materialises an
//     instance on demand (first request), so no eager instance creation here.
//   - snapshot_prime: imaged finished building a deployment's layer; boot it
//     once, snapshot it, and park it (spec §5 step 6, ADR-018).
func (l *Loop) handleNotification(ctx context.Context, n db.Notification) {
	switch n.Channel {
	case db.NotifyAppChanged:
		l.log.Debug("app_changed", "payload", n.Payload)
	case db.NotifyDeploymentChanged:
		l.log.Debug("deployment_changed", "payload", n.Payload)
	case db.NotifySnapshotPrime:
		var p struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"deployment_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			l.log.Warn("sched: bad snapshot_prime payload", "err", err)
			return
		}
		if p.AppID == "" || p.DeploymentID == "" {
			l.log.Warn("sched: snapshot_prime missing ids", "payload", n.Payload)
			return
		}
		if err := l.engine.Prime(ctx, p.AppID, p.DeploymentID); err != nil {
			l.log.Warn("sched: prime failed", "app", p.AppID, "deployment", p.DeploymentID, "err", err)
		}
	}
}

// runReaper builds a read-only snapshot of every instance and applies the idle /
// RAM-pressure selectors, delegating each action to the Engine:
//   - ReapIdle → Engine.Park (snapshot + park; snapshot reused on next wake).
//   - SelectEvictions → Engine.Evict (destroy; next wake cold-boots, ADR-005).
func (l *Loop) runReaper(ctx context.Context) {
	store := l.engine.Store()
	apps, err := store.ListAllApps(ctx)
	if err != nil {
		l.log.Warn("reaper: list apps", "err", err)
		return
	}
	now := time.Now()
	var snapshot []InstanceInfo
	for _, a := range apps {
		plan := api.Plan("")
		if acct, err := store.AccountByID(ctx, a.AccountID); err == nil {
			plan = acct.Plan
		}
		instances, err := store.ListInstancesForApp(ctx, a.ID)
		if err != nil {
			continue
		}
		for _, ins := range instances {
			snapshot = append(snapshot, InstanceInfo{
				Instance:     ins.ID,
				AppID:        ins.AppID,
				Plan:         plan,
				State:        state.State(ins.State),
				RAMMB:        ins.RAMMB,
				LastRequest:  ins.LastRequestAt,
				Started:      ins.StartedAt,
				IdleTimeoutS: a.IdleTimeoutS,
			})
		}
	}
	resident := l.engine.Ledger().ResidentRAM()
	for _, id := range ReapIdle(now, snapshot) {
		if err := l.engine.Park(ctx, id); err != nil {
			l.log.Warn("reaper: idle park", "instance", id, "err", err)
		}
	}
	for _, id := range SelectEvictions(resident, now, snapshot) {
		if err := l.engine.Evict(ctx, id); err != nil {
			l.log.Warn("reaper: eviction", "instance", id, "err", err)
		}
	}
}

// runCronTick is the placeholder for cron firing. M5 keeps the table CRUD; the
// actual HTTP-POST-through-gatewayd path lands with cron (M7).
func (l *Loop) runCronTick(ctx context.Context) {
	crons, err := l.engine.Store().ListEnabledCrons(ctx)
	if err != nil {
		l.log.Warn("cron: list", "err", err)
		return
	}
	if len(crons) == 0 {
		return
	}
	l.log.Debug("cron tick", "enabled", len(crons))
}
