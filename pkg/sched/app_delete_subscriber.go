package sched

// ADR-098: schedd consumes the app_delete pg_notify channel published
// by apid (handlers_apps.go::deleteApp) and evicts any in-flight
// wake for the deleted app. The wake coordinator's Forget(appID)
// closes the leader's done channel with ErrAppDeleted so followers
// unwind without waiting for the wake-coord TTL.
//
// Mirrors pkg/sched/deletion_subscriber.go line-for-line: takes an
// already-opened `<-chan db.Notification` and a *Engine, runs the
// consumer loop until ctx is cancelled or the channel closes. The
// reconnect / Subscribe lifecycle is the caller's responsibility
// (cmd/schedd owns it via db.Subscribe; tests inject a fake producer).

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/db"
)

// AppDeleteSubscriber consumes NotifyAppDelete and evicts any
// in-flight wake for the deleted app via the wake coordinator.
//
// Lock discipline (load-bearing — see ADR-098 §Decision):
//
//	wakeCoord.Forget takes only wakeCoord.mu; it NEVER touches appMu.
//	pg-notify goroutines have no business holding appMu; the leader,
//	if any, is mid-boot and its appMu is unlocked between Phase 3
//	and Phase 4 per the engine's 4-phase contract. Inverting the
//	order would deadlock the Forget goroutine on the leader's appMu
//	and the wake-coord entry would never be evicted.
type AppDeleteSubscriber struct {
	engine *Engine
	log    *slog.Logger
}

// NewAppDeleteSubscriber wires a subscriber with the engine + log.
// The caller is responsible for opening the pg_notify feed (see
// db.Subscribe) and for any reconnect logic.
func NewAppDeleteSubscriber(engine *Engine, log *slog.Logger) *AppDeleteSubscriber {
	return &AppDeleteSubscriber{engine: engine, log: log}
}

// Run drains an already-opened channel until ctx is cancelled or
// the channel closes. Returns ctx.Err() on cancellation; any
// in-flight handle() call is given time to finish by the channel's
// natural delivery pacing.
//
// Each "keep going" decision is deliberate: pg_notify is
// best-effort; the apps table is the source of truth for
// "the app was deleted", and the apid handler that emits this
// notification has already deleted the row by the time the
// schedd-side subscriber observes it. A redelivered / missed
// message therefore costs at worst a stale wake on a deleted app,
// which the next state scan reaps.
func (d *AppDeleteSubscriber) Run(ctx context.Context, ch <-chan db.Notification) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			d.handle(ctx, n)
		}
	}
}

// handle is the per-message work unit. Parse, evict. Each step
// logs on failure but never propagates — the loop must outlive
// a transient bad event.
func (d *AppDeleteSubscriber) handle(ctx context.Context, n db.Notification) {
	if n.Channel != db.NotifyAppDelete {
		// Defensive: callers generally Subscribe to a single
		// channel, but a wider-list caller could route unrelated
		// traffic here. Ignore to avoid forgetting on a misrouted
		// payload.
		return
	}
	var payload struct {
		AppID string `json:"app_id"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		d.log.Warn("schedd: app-delete subscriber bad payload",
			"channel", n.Channel, "err", err, "payload_first_64", first64(n.Payload))
		return
	}
	if payload.AppID == "" {
		d.log.Warn("schedd: app-delete subscriber empty app_id in payload", "channel", n.Channel)
		return
	}
	d.evictApp(ctx, payload.AppID)
}

// evictApp evicts any in-flight wake for appID via the wake
// coordinator. Idempotent: Forget on an absent entry is a no-op
// (per wake_coord.go). The follower's await unwinds with
// ErrAppDeleted; the leader's await unwinds with ErrAppDeleted if
// the leader hadn't yet populated its outcome.
//
// The natural reaper collects the in-flight instance row (if any)
// on the next loop tick — schedd doesn't dial vmmd from this path
// because a WAKING instance never finished wake (vmmd has no live
// handle) and a RUNNING instance whose row is reaped by the engine
// is auto-collected by the engine's next state scan.
func (d *AppDeleteSubscriber) evictApp(_ context.Context, appID string) {
	if d.engine.wakeCoord == nil {
		// Defensive: a test wiring an Engine without wakeCoord
		// shouldn't crash on this path.
		return
	}
	d.engine.wakeCoord.Forget(appID)
	d.log.Info("schedd: app-delete subscriber forgot wake-coord entry",
		"app", appID)
}
