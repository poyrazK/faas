// Package sched — daemon glue that translates pg_notify events into ledger
// updates and instance state writes. schedd is the sole writer to the
// instances table (spec §Component ownership); this file owns the loop that
// reacts to apid's notifications and the reaper tick.
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

// Loop subscribes to apid's pg_notify channels and reacts. It also runs the
// idle reaper on a 10s tick (spec §4.3). It does NOT actually drive vmmd
// gRPC — that's wired in the M5 follow-up; here the loop logs the desired
// transitions so an operator can see the policy firing.
type Loop struct {
	pool   *pgxpool.Pool
	store  state.Store
	ledger *Ledger
	log    *slog.Logger
}

func NewLoop(pool *pgxpool.Pool, store state.Store, ledger *Ledger, log *slog.Logger) *Loop {
	return &Loop{pool: pool, store: store, ledger: ledger, log: log}
}

// Run blocks until ctx is cancelled. It owns three goroutines: the LISTEN
// subscriber, the reaper tick, and the cron tick.
func (l *Loop) Run(ctx context.Context) error {
	notif, cancel, err := db.Subscribe(ctx, l.pool, []string{
		db.NotifyAppChanged,
		db.NotifyDeploymentChanged,
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
func (l *Loop) handleNotification(ctx context.Context, n db.Notification) {
	switch n.Channel {
	case db.NotifyAppChanged:
		// App row changed (created/updated/deleted/parked/woken). The
		// reaper and ledger adapt on the next tick — explicit invalidation
		// isn't necessary because both read fresh from the store.
		l.log.Debug("app_changed", "payload", n.Payload)
	case db.NotifyDeploymentChanged:
		var p struct {
			AppID string `json:"app_id"`
			From  string `json:"from"`
			To    string `json:"to"`
			Kind  string `json:"kind"`
		}
		_ = json.Unmarshal([]byte(n.Payload), &p)
		l.log.Info("deployment_changed", "app", p.AppID, "from", p.From, "to", p.To, "kind", p.Kind)
	}
}

// runReaper walks every running instance and applies the idle / RAM-pressure
// selectors. In M5 we log the desired actions; the actual park calls land in
// the M5 follow-up that wires vmmd gRPC.
func (l *Loop) runReaper(ctx context.Context) {
	apps, err := l.allApps(ctx)
	if err != nil {
		l.log.Warn("reaper: list apps", "err", err)
		return
	}
	now := time.Now()
	var snapshot []InstanceInfo
	for _, a := range apps {
		acct, err := l.store.AccountByID(ctx, a.AccountID)
		plan := api.Plan("")
		if err == nil {
			plan = acct.Plan
		}
		instances, err := l.store.ListInstancesForApp(ctx, a.ID)
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
	resident := l.ledger.ResidentRAM()
	for _, id := range ReapIdle(now, snapshot) {
		l.log.Info("reaper: park idle", "instance", id)
		l.ledger.Release(id)
	}
	for _, id := range SelectEvictions(resident, now, snapshot) {
		l.log.Info("reaper: evict under pressure", "instance", id)
		l.ledger.Release(id)
	}
}

// runCronTick is the placeholder for cron firing. M5 keeps the table CRUD;
// the actual HTTP-POST-through-gatewayd path lands with the sched
// implementation that wires schedd → gatewayd directly.
func (l *Loop) runCronTick(_ context.Context) {
	crons, err := l.store.ListEnabledCrons(context.Background())
	if err != nil {
		l.log.Warn("cron: list", "err", err)
		return
	}
	if len(crons) == 0 {
		return
	}
	l.log.Debug("cron tick", "enabled", len(crons))
}

// allApps returns every app on the box. Used by the reaper + cron loops; in
// production this would be paginated / cached, but at M5 scale (one-box,
// single-digit apps) it's fine.
func (l *Loop) allApps(ctx context.Context) ([]state.App, error) {
	var out []state.App
	for _, appID := range l.knownApps(ctx) {
		app, err := l.store.AppByID(ctx, appID)
		if err != nil {
			continue
		}
		out = append(out, app)
	}
	return out, nil
}

func (l *Loop) knownApps(ctx context.Context) []string {
	// MemStore + PgStore don't expose a "list every app" path that doesn't
	// require an account id; for the one-box daemon we read the table via
	// a small SQL helper added here rather than widening the Store
	// interface.
	type allAppsLister interface {
		ListAllApps(ctx context.Context) ([]state.App, error)
	}
	if lister, ok := l.store.(allAppsLister); ok {
		apps, err := lister.ListAllApps(ctx)
		if err != nil {
			return nil
		}
		ids := make([]string, 0, len(apps))
		for _, a := range apps {
			ids = append(ids, a.ID)
		}
		return ids
	}
	_ = ctx
	return nil
}
