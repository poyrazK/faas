package sched

// pkg/sched/placement_claim.go — schedd-side async placement claim
// (Phase 2 / Gate A, migration 00091).
//
// Architectural note: the original plan placed the chooser inside
// cmd/apid (cmd/apid/placement.go). The depguard rule
// `apid-control-plane-only` (.golangci.yml:36-58) forbids apid from
// importing pkg/sched — scheduling is the schedd's job, not the
// control plane's. The mid-PR pivot moved the chooser here: apid
// writes apps.node_id = NULL on create, schedd stamps the owner
// asynchronously via pkg/sched.PlacementClaimSubscriber reacting
// to NotifyAppChanged (kind="created"). The conditional UPDATE in
// Store.SetAppNodeID serialises N peer schedds into exactly one
// winner; losers observe app.NodeID != "" on their next read and
// drop silently. See docs/adr/055-tier-a-per-node-schedd-and-placement.md.
//
// Shape parallels pkg/sched/deletion_subscriber.go and
// pkg/sched/egress_drift.go: drain goroutine over an already-opened
// <-chan db.Notification, no reconnect bookkeeping here (cmd/schedd
// owns the dial lifecycle via deps.subscribePlacementClaim).
//
// Idempotency rides on Engine.ClaimUnplaced itself: the function
// re-reads apps by id, and bails (returning nil) when node_id is
// already set. A redelivered notify is a single DB read + nil
// return; a peer that won the race observes the same nil return.
// "kind=claimed" events are emitted by Engine.ClaimUnplaced on
// success; this subscriber filters them out to avoid re-entry.

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/db"
)

// PlacementClaimSubscriber consumes NotifyAppChanged with
// kind="created" and atomically stamps apps.node_id via
// Engine.ClaimUnplaced. Sibling subscribers on the same channel
// (DeletionSubscriber, EgressDriftSubscriber) handle their own
// kinds; the dispatcher is the JSON `kind` field, not the
// channel name.
type PlacementClaimSubscriber struct {
	engine *Engine
	log    *slog.Logger
}

// NewPlacementClaimSubscriber wires a subscriber with the engine +
// log. The caller is responsible for opening the pg_notify feed
// (see db.Subscribe and cmd/schedd's deps.subscribePlacementClaim)
// and for any reconnect logic.
func NewPlacementClaimSubscriber(engine *Engine, log *slog.Logger) *PlacementClaimSubscriber {
	return &PlacementClaimSubscriber{engine: engine, log: log}
}

// Run drains an already-opened channel until ctx is cancelled or
// the channel closes. Returns ctx.Err() on cancellation; any
// in-flight handle() call is given time to finish by the
// channel's natural delivery pacing.
//
// Each "keep going" decision is deliberate: pg_notify is
// best-effort; the apps table is the source of truth for "the app
// exists and needs an owner", and the cold-start sweep (cmd/schedd
// main runs ListUnplacedApps once at boot) will eventually
// reconcile any notify that was lost to a schedd restart.
func (s *PlacementClaimSubscriber) Run(ctx context.Context, ch <-chan db.Notification) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			s.handle(ctx, n)
		}
	}
}

// handle is the per-message work unit. Parse, filter to
// kind="created", dispatch to Engine.ClaimUnplaced. Each step
// logs on failure but never propagates — the loop must outlive a
// transient bad event.
func (s *PlacementClaimSubscriber) handle(ctx context.Context, n db.Notification) {
	if n.Channel != db.NotifyAppChanged {
		// Defensive: callers generally Subscribe to a single
		// channel, but a wider-list caller could route unrelated
		// traffic here. Ignore to avoid claiming on a misrouted
		// payload.
		return
	}
	payload, err := db.ParseAppChangedPayload(n.Payload)
	if err != nil {
		s.engine.ops.ObserveNotificationPayloadRejected(db.NotifyAppChanged, "placement_claim")
		s.log.Warn("sched: placement claim bad payload",
			"channel", n.Channel, "payload", n.Payload, "err", err)
		return
	}
	// Filter: only the create event drives a claim. The other
	// kinds (`updated`, `deleted`, `parked`, `woken`, `renamed`,
	// `claimed`) are no-ops here — either the row is already
	// bound (update/rename/park/wake/claim) or the app is gone
	// (delete; the cold-start sweep skips deleted rows).
	if payload.Kind != "created" {
		return
	}
	if payload.AppID == "" {
		s.log.Warn("sched: placement claim: empty app_id in payload",
			"payload", n.Payload)
		return
	}
	if err := s.engine.ClaimUnplaced(ctx, payload.AppID); err != nil {
		s.log.Warn("sched: placement claim: claim failed",
			"app_id", payload.AppID, "err", err)
		// Do not return; the next notify or the cold-start sweep
		// will retry.
	}
}
