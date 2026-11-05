package sched

// Applying a restarted vmmd's adoption report (vmmd restart adoption ADR §3).
//
// When a node's ownership epoch moves, schedd fetches the new vmmd's report
// and fails every RUNNING row on the node that vmmd neither adopted nor
// leased a slot to since startup: its guest was refused, or never recorded,
// and is gone. A frozen row parks instead, as under ADR-419, and so does a
// frozen row whose freeze or thaw the restart cut short (the guest was
// resumed and reported thawed). Rows in
// transition (waking, cold-booting, snapshotting, migrating, draining) stay
// with the controllers driving them; ADR-419 remains their backstop.
//
// The report is a complete inventory, so this needs neither ADR-191's
// two-sweep wait nor ADR-419's confirmations and grace. Rows are listed
// before the report is fetched: a guest the successor created by then was
// leased before the list, so the report names it. The reread under the app
// lock requires the same wake and start, so a row woken again in between
// is left alone.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// AdoptionRefusedReason is the audit and frozen-park reason for rows a
// restarted vmmd did not take over.
const AdoptionRefusedReason = "vmmd_adoption_refused"

// AdoptionThawedReason parks a FROZEN row whose guest the restarted vmmd
// resumed because a freeze or thaw was cut short. Publishing it RUNNING
// needs the thaw path's fences; parking is safe for a frozen app
// (ADR-1151), and the next request restores it.
const AdoptionThawedReason = "vmmd_adoption_thawed"

type adoptionReportRouter interface {
	AdoptionReport(ctx context.Context, nodeID string) (AdoptionReport, error)
}

// AdoptionReportReconciler applies each node's adoption report once per
// ownership epoch. Construct with NewAdoptionReportReconciler.
type AdoptionReportReconciler struct {
	engine *Engine
	log    DeadNodeReconcilerLogger

	mu      sync.Mutex
	applied map[string]uint64 // node -> last epoch applied
}

func NewAdoptionReportReconciler(engine *Engine, log DeadNodeReconcilerLogger) *AdoptionReportReconciler {
	return &AdoptionReportReconciler{engine: engine, log: log, applied: map[string]uint64{}}
}

// Reconcile checks every node this schedd schedules onto and applies any
// report under a new epoch. It returns how many rows it moved. A node whose
// report could not be applied in full is retried on the next tick; applying
// a report twice is harmless.
func (r *AdoptionReportReconciler) Reconcile(ctx context.Context) (int, error) {
	if r == nil || r.engine == nil {
		return 0, nil
	}
	epochs, ok := r.engine.Store().(state.ComputeNodeVMMDEpochStore)
	listed, lists := r.engine.Store().(state.AdoptionReportInstanceStore)
	router, routed := r.engine.vmm.(adoptionReportRouter)
	if !ok || !lists || !routed {
		return 0, nil
	}
	nodes, err := r.nodes(ctx)
	if err != nil {
		return 0, err
	}
	moved := 0
	var errs []error
	for _, node := range nodes {
		n, err := r.reconcileNode(ctx, epochs, listed, router, node)
		moved += n
		if err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", node, err))
		}
	}
	return moved, errors.Join(errs...)
}

// nodes is the owner node, or every active node in the single-scheduler
// posture.
func (r *AdoptionReportReconciler) nodes(ctx context.Context) ([]string, error) {
	if owner := r.engine.OwnerNodeID(); owner != "" {
		return []string{owner}, nil
	}
	rows, err := r.engine.Store().ListComputeNodes(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, n := range rows {
		out = append(out, n.ID)
	}
	return out, nil
}

func (r *AdoptionReportReconciler) reconcileNode(ctx context.Context, epochs state.ComputeNodeVMMDEpochStore, listed state.AdoptionReportInstanceStore, router adoptionReportRouter, node string) (int, error) {
	cur, err := epochs.ComputeNodeVMMDEpoch(ctx, node)
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	done := r.applied[node]
	r.mu.Unlock()
	if cur.Epoch == 0 || cur.Epoch <= done {
		return 0, nil
	}
	listCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	rows, err := listed.ListAdoptionReportInstancesOnNode(listCtx, node, nodeInventoryRunningLimit)
	cancel()
	if err != nil {
		return 0, err
	}
	rep, err := router.AdoptionReport(ctx, node)
	if errors.Is(err, ErrAdoptionReportOff) {
		r.markApplied(node, cur.Epoch)
		return 0, nil
	}
	if err != nil {
		return 0, err // vmmd still starting or unreachable: next tick.
	}
	if rep.Epoch != cur.Epoch || rep.Incarnation != cur.Incarnation {
		// Another start claimed the node since the read; its report is next.
		r.logf("info", "sched: adoption report: epoch moved", "node_id", node, "row_epoch", cur.Epoch, "report_epoch", rep.Epoch)
		return 0, nil
	}
	// Admissions first: one the successor cannot be re-bound to leaves the
	// adopted set, so its row is repaired like any other refused guest.
	rebound, rebindRetry := r.engine.rebindAdoptedAdmissions(ctx, &rep)
	if !rep.Complete {
		if !rebindRetry {
			r.markApplied(node, cur.Epoch)
		}
		r.logf("warn", "sched: adoption report incomplete; leaving lost guests to the inventory reconciler", "node_id", node, "epoch", rep.Epoch, "rebound", rebound)
		return 0, nil
	}
	moved, retry := r.engine.applyAdoptionReport(ctx, rep, rows)
	retry = retry || rebindRetry
	if !retry {
		r.markApplied(node, cur.Epoch)
	}
	r.logf("info", "sched: adoption report applied", "node_id", node, "epoch", rep.Epoch,
		"adopted", len(rep.Adopted), "refused", len(rep.Refused), "rebound", rebound, "moved", moved, "fenced", rep.Fenced, "retry", retry)
	return moved, nil
}

func (r *AdoptionReportReconciler) markApplied(node string, epoch uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied[node] = epoch
}

func (r *AdoptionReportReconciler) logf(level, msg string, args ...any) {
	if r.log == nil {
		return
	}
	if level == "warn" {
		r.log.Warn(msg, args...)
		return
	}
	r.log.Info(msg, args...)
}

// AdoptionRebindRefusedReason is the refusal detail for an adopted guest
// whose admission could not be re-bound to the successor.
const AdoptionRebindRefusedReason = "admission_rebind_refused"

// rebindAdoptedAdmissions re-binds each adopted guest's ADR-595 admission to
// the report's vmmd (docs/adr/20261018-runtime-admission-rebind.md): the hop
// extends the admission's current holder, walked from the receipt's issuer
// through the instance's re-binds, to the successor under the report's
// epoch. A hop the store refuses (the residency no longer matches the
// receipt, or the row no longer serves) moves the instance from adopted to
// refused, so applyAdoptionReport repairs its row. Recording is idempotent,
// so a retried pass is harmless; retry reports a transient failure.
func (e *Engine) rebindAdoptedAdmissions(ctx context.Context, rep *AdoptionReport) (int, bool) {
	if len(rep.Admissions) == 0 {
		return 0, false
	}
	store, ok := e.store.(state.ApplicationStandardRebindStore)
	if !ok {
		return 0, false
	}
	rebound, retry := 0, false
	for instance, adm := range rep.Admissions {
		if _, adopted := rep.Adopted[instance]; !adopted {
			continue
		}
		hops, err := store.ListApplicationStandardRebinds(ctx, instance)
		if err != nil {
			retry = true
			continue
		}
		holder := admissionHolder(hops, adm.IssuingIncarnation)
		if holder == rep.Incarnation {
			continue // Re-bound by an earlier pass.
		}
		hop := state.ApplicationStandardRebind{
			InstanceID: instance, NodeID: rep.NodeID, FromIncarnation: holder, ToIncarnation: rep.Incarnation, VMMDEpoch: rep.Epoch,
			Observed: state.ApplicationStandardRebindObserved{Token: adm.Token, CompletedAtUnixNano: adm.CompletedAtUnixNano,
				Netns: adm.Netns, HostIP: adm.HostIP, LeaseUID: adm.LeaseUID, Paused: adm.Paused},
		}
		switch err := store.RecordApplicationStandardRebind(ctx, hop); {
		case err == nil:
			rebound++
		case errors.Is(err, state.ErrApplicationStandardRuntimeStale), errors.Is(err, state.ErrInvalidArgument):
			app := rep.Adopted[instance]
			delete(rep.Adopted, instance)
			rep.Refused[instance] = AdoptionRefusal{AppID: app, Reason: AdoptionRebindRefusedReason, Detail: err.Error()}
			e.log.Warn("sched: adopted guest's admission not re-bound; repairing its row", "instance_id", instance, "node_id", rep.NodeID, "epoch", rep.Epoch, "err", err)
		default:
			retry = true
		}
	}
	return rebound, retry
}

// admissionHolder follows an admission's re-binds from its issuer to the
// incarnation that holds it now, as application_standard_effective_incarnation does.
func admissionHolder(hops []state.ApplicationStandardRebind, issuer string) string {
	held := issuer
	for range hops {
		next := ""
		for _, h := range hops {
			if h.FromIncarnation == held {
				next = h.ToIncarnation
				break
			}
		}
		if next == "" {
			break
		}
		held = next
	}
	return held
}

// applyAdoptionReport fails or parks each listed row the report's vmmd does
// not hold. It returns how many rows moved and whether a row needs another
// pass.
func (e *Engine) applyAdoptionReport(ctx context.Context, rep AdoptionReport, rows []state.Instance) (int, bool) {
	reconciler, ok := e.store.(staleInstanceStore)
	if !ok {
		return 0, false
	}
	terminalAt := e.now().UTC()
	moved, attempted, retry := 0, 0, false
	for _, ins := range rows {
		if ins.NodeID != rep.NodeID {
			continue
		}
		if rep.Holds(ins.ID) {
			if ins.State == string(state.StateFrozen) && rep.Resolved[ins.ID] == AdoptionResolvedThawed {
				if err := e.ParkFrozen(ctx, ins.ID, AdoptionThawedReason); err != nil {
					retry = true
					e.log.Warn("sched: park frozen row whose guest the restarted vmmd resumed", "instance_id", ins.ID, "node_id", rep.NodeID, "err", err)
					continue
				}
				moved++
			}
			continue
		}
		switch ins.State {
		case string(state.StateFrozen):
			if err := e.ParkFrozen(ctx, ins.ID, AdoptionRefusedReason); err != nil {
				retry = true
				e.log.Warn("sched: park frozen instance vmmd did not adopt", "instance_id", ins.ID, "node_id", rep.NodeID, "err", err)
				continue
			}
			moved++
		case string(state.StateRunning):
			if attempted >= api.InstanceDivergenceTickLimit {
				return moved, true
			}
			attempted++
			listed := ins
			failed, again := e.failMissingRunning(ctx, reconciler, ins, rep.NodeID, AdoptionRefusedReason, terminalAt,
				func(fresh state.Instance, _ time.Time) bool {
					return fresh.WakeID == listed.WakeID && fresh.StartedAt.Equal(listed.StartedAt)
				})
			if failed {
				moved++
				refusal := rep.Refused[ins.ID]
				e.log.Warn("sched: instance not adopted by restarted vmmd", "instance_id", ins.ID, "app_id", ins.AppID,
					"node_id", rep.NodeID, "epoch", rep.Epoch, "reason", refusal.Reason, "detail", refusal.Detail, "resolved", refusal.Resolved)
			}
			retry = retry || again
		}
	}
	return moved, retry
}
