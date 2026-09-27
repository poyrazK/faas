package sched

// tier-2 PR-B (ADR-031 + ADR-033): live-instance drift consumer.
//
// PATCH /v1/apps/{slug} updates apps.egress_allowlist (the per-app
// outbound IP allowlist). apid emits NotifyAppChanged with
// kind="updated"; schedd's main loop currently logs the payload
// and moves on. ADR-033 §Context "the next bigger item" calls
// out the gap: a running netns keeps its old ruleset until the
// next Wake, so an operator that shrinks a Free → Pro allowlist
// sees the new CIDRs accepted by the new Wake but the old
// allowlist still in effect on every live instance until each
// one cycles.
//
// This subscriber closes the gap: it consumes the same
// app_changed feed, filters to kind="updated", looks up the
// current EgressAllowlist on the apps row, enumerates every
// live instance of the app (across all compute nodes), and
// pushes the new allowlist to each owning vmmd via
// RoutedVMM.UpdateEgressAllowlist. vmmd applies the patch
// in-place via incremental nft delete-by-handle + add (no
// netns teardown, no cold-wake tax).
//
// Shape parallels pkg/sched/deletion_subscriber.go: drain
// goroutine over an already-opened <-chan db.Notification, no
// reconnect bookkeeping here (cmd/schedd owns the dial lifecycle
// via deps.subscribeEgressDrift, the same shape as
// deps.subscribeDeletion). Idempotency rides on vmmd: a set-
// equal allowlist is a no-op on the vmmd side (samePrefixSet
// short-circuit), so a redelivered event is safe. A redelivered
// event that lands mid-write just refreshes the same baseline.

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	egressDriftReconcileInterval = 30 * time.Second
	egressDriftBatchLimit        = 500
)

// EgressDriftSubscriber consumes NotifyAppChanged with
// kind="updated" and fans the per-app egress allowlist out to
// every vmmd that owns a live instance of the app. Idempotent on
// the vmmd side; the subscriber never writes to the instances
// table (the engine + watchdog own that) and never wakes or
// parks anything.
type EgressDriftSubscriber struct {
	engine *Engine
	router RoutedVMM
	log    *slog.Logger
}

// NewEgressDriftSubscriber wires the consumer. router is the
// VMMRouter the engine already holds; the subscriber is
// read-only on the engine (it pulls AppByID + ListInstancesForApp
// via the store) and write-only on the router.
func NewEgressDriftSubscriber(engine *Engine, router RoutedVMM, log *slog.Logger) *EgressDriftSubscriber {
	return &EgressDriftSubscriber{engine: engine, router: router, log: log}
}

// Run drains an already-opened channel until ctx is cancelled or
// the channel closes. Returns ctx.Err() on cancellation; any
// in-flight handle() call is given time to finish by the
// channel's natural delivery pacing.
func (e *EgressDriftSubscriber) Run(ctx context.Context, ch <-chan db.Notification) error {
	ticker := time.NewTicker(egressDriftReconcileInterval)
	defer ticker.Stop()
	e.reconcilePending(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			e.reconcilePending(ctx)
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			if n.Channel == db.NotifyAppEgressPolicyChanged {
				payload, err := db.ParseAppEgressPolicyChangedPayload(n.Payload)
				if err != nil {
					e.engine.ops.ObserveNotificationPayloadRejected(db.NotifyAppEgressPolicyChanged, "egress_drift")
					e.log.Warn("schedd: egress drift bad durable payload", "err", err)
					continue
				}
				e.reconcileApp(ctx, payload.AppID)
				continue
			}
			if n.Channel == db.NotifyAppCPULimitPolicyChanged {
				payload, err := db.ParseAppCPULimitPolicyChangedPayload(n.Payload)
				if err != nil {
					e.engine.ops.ObserveNotificationPayloadRejected(db.NotifyAppCPULimitPolicyChanged, "app_cpu_policy")
					e.log.Warn("schedd: app CPU policy bad durable payload", "err", err)
					continue
				}
				e.reconcileAppCPULimit(ctx, payload.AppID)
				continue
			}
			e.handle(ctx, n)
		}
	}
}

func (e *EgressDriftSubscriber) convergenceStore() (state.AppEgressPolicyConvergenceStore, bool) {
	store, ok := e.engine.store.(state.AppEgressPolicyConvergenceStore)
	return store, ok
}

// reconcilePending is the durable repair path. It runs once when a LISTEN
// session starts and periodically thereafter, so a missed notification or a
// transient vmmd failure cannot strand a live instance on an old allowlist.
func (e *EgressDriftSubscriber) reconcilePending(ctx context.Context) {
	store, ok := e.convergenceStore()
	if ok {
		targets, err := store.ListPendingAppEgressPolicyTargets(ctx, "", state.AppEgressPolicyObservationFreshness/2, egressDriftBatchLimit)
		if err != nil {
			e.log.Warn("schedd: list pending app egress policies failed", "err", err)
		} else {
			for _, target := range targets {
				if err := ctx.Err(); err != nil {
					return
				}
				e.applyTarget(ctx, store, target)
			}
		}
	}
	// Reconcile CPU independently so a transient failure in the egress query
	// cannot suppress repair for another runtime-policy component.
	e.reconcilePendingCPULimits(ctx)
}

func (e *EgressDriftSubscriber) cpuPolicyStore() (state.AppCPUPolicyConvergenceStore, bool) {
	store, ok := e.engine.store.(state.AppCPUPolicyConvergenceStore)
	return store, ok
}

type appCPULimitPolicyRouter interface {
	UpdateAppCPULimit(context.Context, string, string, int64, int) error
}

func (e *EgressDriftSubscriber) reconcilePendingCPULimits(ctx context.Context) {
	store, ok := e.cpuPolicyStore()
	if !ok {
		return
	}
	targets, err := store.ListPendingAppCPUPolicyTargets(ctx, "", state.AppCPUPolicyObservationFreshness/2, egressDriftBatchLimit)
	if err != nil {
		e.log.Warn("schedd: list pending app CPU policies failed", "err", err)
		return
	}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return
		}
		e.applyCPULimitTarget(ctx, store, target)
	}
}

func (e *EgressDriftSubscriber) reconcileAppCPULimit(ctx context.Context, appID string) {
	store, ok := e.cpuPolicyStore()
	if !ok {
		return
	}
	targets, err := store.ListPendingAppCPUPolicyTargets(ctx, appID, state.AppCPUPolicyObservationFreshness/2, egressDriftBatchLimit)
	if err != nil {
		e.log.Warn("schedd: list app CPU policy targets failed", "app", appID, "err", err)
		return
	}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return
		}
		e.applyCPULimitTarget(ctx, store, target)
	}
}

func (e *EgressDriftSubscriber) applyCPULimitTarget(ctx context.Context, store state.AppCPUPolicyConvergenceStore, target state.AppCPUPolicyApplyTarget) {
	var applyErr error
	if router, ok := e.router.(appCPULimitPolicyRouter); ok {
		applyErr = router.UpdateAppCPULimit(ctx, target.NodeID, target.AppID, target.Revision, target.CPUMillicores)
	} else {
		applyErr = errors.New("app CPU policy update is unsupported by routed VMM")
	}
	if err := store.RecordAppCPUPolicyApply(ctx, target.AppID, target.NodeID, target.Revision, applyErr); err != nil {
		e.log.Warn("schedd: record app CPU policy apply failed", "app", target.AppID, "node", target.NodeID,
			"revision", target.Revision, "apply_err", applyErr, "err", err)
		return
	}
	if applyErr != nil {
		e.log.Warn("schedd: app CPU policy vmmd update failed", "app", target.AppID, "slug", target.Slug,
			"node", target.NodeID, "revision", target.Revision, "err", applyErr)
		return
	}
	e.log.Debug("schedd: app CPU policy applied", "app", target.AppID, "slug", target.Slug,
		"node", target.NodeID, "revision", target.Revision, "cpu_millicores", target.CPUMillicores)
}

func (e *EgressDriftSubscriber) reconcileApp(ctx context.Context, appID string) {
	store, ok := e.convergenceStore()
	if !ok {
		app, err := e.engine.store.AppByID(ctx, appID)
		if err != nil {
			e.log.Warn("schedd: egress drift app read failed", "app", appID, "err", err)
			return
		}
		e.fanOut(ctx, app.ID, app.Slug)
		return
	}
	targets, err := store.ListPendingAppEgressPolicyTargets(ctx, appID, state.AppEgressPolicyObservationFreshness/2, egressDriftBatchLimit)
	if err != nil {
		e.log.Warn("schedd: list app egress policy targets failed", "app", appID, "err", err)
		return
	}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return
		}
		e.applyTarget(ctx, store, target)
	}
}

func (e *EgressDriftSubscriber) applyTarget(ctx context.Context, store state.AppEgressPolicyConvergenceStore, target state.AppEgressPolicyApplyTarget) {
	applyErr := e.router.UpdateEgressAllowlist(ctx, target.NodeID, target.AppID, target.Allowlist)
	if err := store.RecordAppEgressPolicyApply(ctx, target.AppID, target.NodeID, target.Revision, applyErr); err != nil {
		e.log.Warn("schedd: record app egress policy apply failed",
			"app", target.AppID, "slug", target.Slug, "node", target.NodeID,
			"revision", target.Revision, "apply_err", applyErr, "err", err)
		return
	}
	if applyErr != nil {
		e.log.Warn("schedd: egress drift vmmd update failed",
			"app", target.AppID, "slug", target.Slug, "node", target.NodeID,
			"revision", target.Revision, "err", applyErr)
		return
	}
	e.log.Debug("schedd: app egress policy applied",
		"app", target.AppID, "slug", target.Slug, "node", target.NodeID,
		"revision", target.Revision, "allowlist_len", len(target.Allowlist))
}

// handle is the per-message work unit. Parse, filter, walk,
// fan-out. Each step logs on failure but never propagates —
// the loop must outlive a transient bad event (the same "never
// return" policy as DeletionSubscriber.handle).
func (e *EgressDriftSubscriber) handle(ctx context.Context, n db.Notification) {
	if n.Channel != db.NotifyAppChanged {
		// Defensive: callers generally Subscribe to a single
		// channel, but a wider-list caller could route
		// unrelated traffic here. Ignore to avoid re-fanning
		// on a misrouted payload.
		return
	}
	payload, err := db.ParseAppChangedPayload(n.Payload)
	if err != nil {
		e.engine.ops.ObserveNotificationPayloadRejected(db.NotifyAppChanged, "egress_drift")
		e.log.Warn("schedd: egress drift bad payload",
			"channel", n.Channel, "err", err, "payload_first_64", first64(n.Payload))
		return
	}
	// kind filter: "updated" carries an egress_allowlist
	// mutation; "static_egress_ip" carries an ADR-119
	// pin mutation. "deleted", "renamed", "parked",
	// "woken" are ignored — those are domain events the
	// deletion / route flush / watchdog consumers
	// handle.
	//
	// Empty AppID is the first guard: payloads without an
	// app_id cannot be routed to a vmmd, so the kind switch
	// is a no-op for them. Logging is at Warn (the upstream
	// payload is malformed; a future PR may surface this as
	// a counter).
	if payload.AppID == "" {
		e.log.Warn("schedd: egress drift empty app_id in payload",
			"channel", n.Channel, "payload", n.Payload)
		return
	}
	switch payload.Kind {
	case "updated":
		if _, ok := e.convergenceStore(); ok {
			e.reconcileApp(ctx, payload.AppID)
		} else {
			e.fanOut(ctx, payload.AppID, payload.Slug)
		}
	case "static_egress_ip":
		e.fanOutStaticEgressIP(ctx, payload.AppID, payload.Slug)
	default:
		return
	}
}

// fanOut reads the current EgressAllowlist, enumerates every
// live instance, and pushes the allowlist to each owning
// vmmd. Each per-node call is independent — a failure on one
// node logs and continues (the next reconcile picks up the
// missing node).
func (e *EgressDriftSubscriber) fanOut(ctx context.Context, appID, slug string) {
	// Re-read the column on every event so the patch reflects
	// the post-commit state, not the in-flight snapshot the
	// apid handler held when it emitted the notification.
	// Cheap — AppByID is one indexed lookup; even with 100k
	// apps this is sub-millisecond.
	app, err := e.engine.store.AppByID(ctx, appID)
	if err != nil {
		e.log.Warn("schedd: egress drift app read failed",
			"app", appID, "slug", slug, "err", err)
		return
	}
	allowlist := app.EgressAllowlist

	// ListInstancesForApp returns every row the app owns,
	// ordered by started_at desc. We filter to live states
	// (RUNNING / WAKING / COLD_BOOTING) and dedupe by node
	// so a vmmd that hosts 3 instances of the app receives
	// one call, not three — the per-app fan-out on vmmd's
	// side is the responsibility of pkg/fcvm.Manager.
	rows, err := e.engine.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		e.log.Warn("schedd: egress drift list instances failed",
			"app", appID, "slug", slug, "err", err)
		return
	}
	// Track the set of nodes we've already pushed to this
	// tick — the in-place patch is per-app, not per-instance.
	seen := make(map[string]struct{}, len(rows))
	pushed := 0
	for _, ins := range rows {
		if !state.IsLive(ins.State) {
			continue
		}
		if ins.NodeID == "" {
			// Pre-#97 fixture or test row. Skip — without a
			// nodeID we can't route; the next reconcile
			// (or a Park + ColdBoot) will re-anchor the
			// row to a real node.
			continue
		}
		if _, ok := seen[ins.NodeID]; ok {
			continue
		}
		seen[ins.NodeID] = struct{}{}
		if err := e.router.UpdateEgressAllowlist(ctx, ins.NodeID, appID, allowlist); err != nil {
			// Log and continue. The next reconcile
			// (next PATCH, or the watchdog's Park + ColdBoot
			// on a stuck instance) re-anchors the vmmd's
			// live-instance map.
			e.log.Warn("schedd: egress drift vmmd update failed",
				"app", appID, "slug", slug,
				"node", ins.NodeID, "err", err)
			continue
		}
		pushed++
	}
	if pushed == 0 {
		e.log.Debug("schedd: egress drift observed with no live instances",
			"app", appID, "slug", slug, "allowlist_len", len(allowlist))
		return
	}
	e.log.Info("schedd: egress drift fanned out",
		"app", appID, "slug", slug,
		"allowlist_len", len(allowlist),
		"live_instances", len(rows),
		"nodes_pushed", pushed)
}

// fanOutStaticEgressIP (ADR-119 redesign) is the per-app
// static IP counterpart of fanOut. It walks the live-instance
// map of the app, dedupes by node, and pushes the new IP (or
// clear) to each owning vmmd via
// RoutedVMM.UpdateStaticEgressIP.
//
// The source of truth is the apps.static_egress_ip column —
// re-read on every event so the patch reflects the
// post-commit state, not the in-flight snapshot the
// notification payload held when it was emitted. mirrors
// fanOut's read-on-event pattern. The wire payload carries
// `ip` only for log correlation. The vmmd side has its own
// short-circuit (Manager.UpdateStaticEgressIP's id-IP
// equality check) so a redelivered identical IP is a no-op.
func (e *EgressDriftSubscriber) fanOutStaticEgressIP(ctx context.Context, appID, slug string) {
	// Re-read the column. Cheap — AppByID is one indexed
	// lookup. The column is signed text (nullable); an empty
	// string is the "clear" pin.
	app, err := e.engine.store.AppByID(ctx, appID)
	if err != nil {
		e.log.Warn("schedd: static egress IP drift app read failed",
			"app", appID, "slug", slug, "err", err)
		return
	}
	ip := app.StaticEgressIP
	// Re-shape the typed column to the wire string. The vmmd
	// re-parses + re-validates against the canonical deny-set
	// (pkg/api.ValidateStaticEgressIP) so this is a faithful
	// round-trip. NULL → "" is the "clear" pin.
	ipStr := ""
	if ip != nil {
		ipStr = ip.String()
	}

	rows, err := e.engine.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		e.log.Warn("schedd: static egress IP drift list instances failed",
			"app", appID, "slug", slug, "err", err)
		return
	}
	seen := make(map[string]struct{}, len(rows))
	pushed := 0
	for _, ins := range rows {
		if !state.IsLive(ins.State) {
			continue
		}
		if ins.NodeID == "" {
			continue
		}
		if _, ok := seen[ins.NodeID]; ok {
			continue
		}
		seen[ins.NodeID] = struct{}{}
		if err := e.router.UpdateStaticEgressIP(ctx, ins.NodeID, app.AccountID, appID, ipStr); err != nil {
			e.log.Warn("schedd: static egress IP drift vmmd update failed",
				"app", appID, "slug", slug,
				"node", ins.NodeID, "err", err)
			continue
		}
		pushed++
	}
	if pushed == 0 {
		e.log.Debug("schedd: static egress IP drift observed with no live instances",
			"app", appID, "slug", slug, "ip", ipStr)
		return
	}
	e.log.Info("schedd: static egress IP drift fanned out",
		"app", appID, "slug", slug,
		"ip", ipStr,
		"live_instances", len(rows),
		"nodes_pushed", pushed)
}
