package sched

// ADR-098: schedd consumes the app_delete pg_notify channel published
// by apid (handlers_apps.go::deleteApp), evicts any in-flight wake,
// and reconciles the app's live instances through vmmd.
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

// AppDeleteSubscriber consumes NotifyAppDelete, evicts any in-flight
// wake for the deleted app via the wake coordinator, and immediately
// destroys live VMs belonging to the soft-deleted app.
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
// "the app was deleted". A missed message is recovered by the
// durable lifecycle sweep in Loop.runReaper.
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

// evictApp evicts any in-flight wake and reconciles live instances for appID.
// Both operations are idempotent, so notification redelivery is safe.
func (d *AppDeleteSubscriber) evictApp(ctx context.Context, appID string) {
	if d.engine.wakeCoord == nil {
		// Defensive: a test wiring an Engine without wakeCoord
		// shouldn't crash on this path.
		// The lifecycle cleanup is still safe and should not depend on
		// the optional wake coordinator test seam.
	} else {
		d.engine.wakeCoord.Forget(appID)
	}
	acted, err := d.engine.ReconcileDeletedApp(ctx, appID)
	if err != nil {
		d.log.Warn("schedd: app-delete subscriber lifecycle reconcile failed",
			"app", appID, "acted", acted, "err", err)
		return
	}
	d.log.Info("schedd: app-delete subscriber reconciled app",
		"app", appID, "acted", acted)
}
