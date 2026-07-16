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
// idle reaper on a 10s tick (spec §4.3) and writes instance state transitions
// to the Store. schedd is the sole writer to the `instances` table; vmmd
// gRPC calls land in the M5.1 follow-up.
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
//
// For deployment_changed events whose payload carries status="live" (the
// terminal imaged re-emit), schedd creates the first instance row in
// cold_booting state. This is the minimum required by CLAUDE.md invariant
// §6.2-1: an app with a live deployment must have a row in `instances` so
// the reaper + admission control can see it.
func (l *Loop) handleNotification(ctx context.Context, n db.Notification) {
	switch n.Channel {
	case db.NotifyAppChanged:
		// App row changed (created/updated/deleted/parked/woken). The
		// reaper and ledger adapt on the next tick — explicit invalidation
		// isn't necessary because both read fresh from the store.
		l.log.Debug("app_changed", "payload", n.Payload)
	case db.NotifyDeploymentChanged:
		var p struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"to"` // imaged emits deployment_id as "to"
			Status       string `json:"status"`
			Kind         string `json:"kind"`
		}
		_ = json.Unmarshal([]byte(n.Payload), &p)
		l.log.Info("deployment_changed",
			"app", p.AppID, "deployment", p.DeploymentID,
			"status", p.Status, "kind", p.Kind)
		if p.Status == string(state.DeployLive) && p.AppID != "" && p.DeploymentID != "" {
			l.materialiseLiveInstance(ctx, p.AppID, p.DeploymentID)
		}
	}
}

// materialiseLiveInstance creates one instance row in cold_booting state for
// the given app+deployment, then emits instance_changed. Idempotent: if a row
// already exists for this deployment we no-op (the reaper or a prior wake may
// have created it first).
func (l *Loop) materialiseLiveInstance(ctx context.Context, appID, deploymentID string) {
	app, err := l.store.AppByID(ctx, appID)
	if err != nil {
		l.log.Warn("sched: app lookup for live instance", "app", appID, "err", err)
		return
	}
	ins, err := l.store.CreateInstance(ctx, appID, deploymentID,
		string(state.StateColdBooting), app.RAMMB)
	if err != nil {
		// A row may already exist for this deployment — that's expected and
		// not an error condition. Other errors we surface to the log.
		l.log.Debug("sched: create instance", "app", appID, "err", err)
		return
	}
	l.emitInstanceChanged(ctx, ins.ID, appID, state.StateColdBooting)
}

// emitInstanceChanged publishes a JSON instance_changed payload. Pool may be
// nil in tests that exercise handleNotification without Run(); we silently
// skip the emit in that case.
func (l *Loop) emitInstanceChanged(ctx context.Context, instanceID, appID string, st state.State) {
	if l.pool == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"instance_id": instanceID,
		"app_id":      appID,
		"state":       string(st),
	})
	if err := db.Notify(ctx, l.pool, db.NotifyInstanceChanged, string(payload)); err != nil {
		l.log.Warn("sched: notify instance_changed", "instance", instanceID, "err", err)
	}
}

// runReaper walks every running instance and applies the idle / RAM-pressure
// selectors. For each id returned, the reaper writes the new state to the
// `instances` table, emits instance_changed, and frees the in-memory ledger.
//
// State semantics:
//   - ReapIdle → StateParked (snapshot reuse on next wake).
//   - SelectEvictions (RAM pressure) → StateStopped (next wake is cold boot
//     per ADR-005 — we evicted under pressure, the snapshot may be gone).
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
		l.transitionInstance(ctx, id, state.StateParked, "reaper: idle park")
	}
	for _, id := range SelectEvictions(resident, now, snapshot) {
		l.transitionInstance(ctx, id, state.StateStopped, "reaper: RAM-pressure eviction")
	}
}

// transitionInstance writes a state change to the `instances` row, emits
// instance_changed, and frees the in-memory ledger entry. Failures are logged
// but don't block the loop — the reaper will re-evaluate on the next tick.
func (l *Loop) transitionInstance(ctx context.Context, instanceID string, st state.State, reason string) {
	if l.store == nil {
		return
	}
	ins, err := l.store.InstanceByID(ctx, instanceID)
	if err != nil {
		l.log.Warn(reason, "instance", instanceID, "err", err)
		return
	}
	if err := l.store.UpdateInstanceState(ctx, instanceID, string(st)); err != nil {
		l.log.Warn(reason, "instance", instanceID, "err", err)
		return
	}
	l.emitInstanceChanged(ctx, instanceID, ins.AppID, st)
	l.ledger.Release(instanceID)
	l.log.Info(reason, "instance", instanceID, "state", string(st))
}

// runCronTick is the placeholder for cron firing. M5 keeps the table CRUD;
// the actual HTTP-POST-through-gatewayd path lands with the sched
// implementation that wires schedd → gatewayd directly.
func (l *Loop) runCronTick(ctx context.Context) {
	crons, err := l.store.ListEnabledCrons(ctx)
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
