package sched

// stale_instance_hint.go — fail a RUNNING row the gateway proved stale.
//
// The divergence sweep (instance_divergence.go) cannot judge a node that
// reports no VMs at all, and it ships report-only. So when vmmd retires an
// app's only VM while schedd cannot write (the native financial gate's
// database outage: the funded teardown removes the VM, the row stays
// RUNNING), the row keeps the app's only concurrency slot after recovery.
// Every wake then answers at capacity, nothing boots, and requests hang
// until the idle reaper happens to notice (billing review R12-1 acceptance,
// owner_crash, 2026-11-19).
//
// The gateway already holds the proof: its forward to that instance came
// back "target not found on node". Its recovery admission now names the
// instance, and schedd checks it with the owning vmmd before admitting. The
// row fails only when that vmmd answers and does not list the VM; no answer
// is no evidence, and nothing changes.

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type staleInstanceKey struct{}

// staleInstanceHintTimeout bounds the one Stats call the check makes, so a
// wedged vmmd delays the recovery admission by at most this much.
const staleInstanceHintTimeout = 3 * time.Second

// staleInstanceHintGrace is how old a RUNNING row must be before the hint can
// fail it. It is far shorter than the sweep's InstanceDivergenceGraceSeconds:
// the sweep judges sampled telemetry, while the hint carries a failed forward
// to a target that was routable and asks vmmd's live set directly. The grace
// only covers a row committed RUNNING a moment before vmmd lists the VM. The
// 60 s grace missed the gate's second owner_crash: the replacement booted
// seconds before its owner crashed, and its ghost row held the slot again.
const staleInstanceHintGrace = 2 * time.Second

// WithStaleInstanceHint names the instance the gateway just proved stale.
// The schedd client carries it on AdmitInstance.
func WithStaleInstanceHint(ctx context.Context, instanceID string) context.Context {
	if instanceID == "" {
		return ctx
	}
	return context.WithValue(ctx, staleInstanceKey{}, instanceID)
}

// StaleInstanceHintFromContext returns the hint WithStaleInstanceHint
// attached, or "".
func StaleInstanceHintFromContext(ctx context.Context) string {
	id, _ := ctx.Value(staleInstanceKey{}).(string)
	return id
}

// ReconcileStaleInstanceHint fails instanceID's row when it belongs to
// appID, is RUNNING past the divergence grace, and its node's vmmd answers
// without listing it. It reports whether it moved the row.
func (e *Engine) ReconcileStaleInstanceHint(ctx context.Context, appID, instanceID string) bool {
	if e == nil || e.store == nil || e.vmm == nil || appID == "" || instanceID == "" {
		return false
	}
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil || ins.AppID != appID || ins.State != string(state.StateRunning) {
		e.staleHintSkipped(instanceID, "row is not this app's RUNNING instance", err)
		return false
	}
	// A just-started VM may not be in vmmd's live set yet, so wait out the
	// rest of the grace before asking rather than dropping the hint: the
	// gate's owner_crash crashed the owner about a second after the
	// replacement started, and the gateway sends this evidence only once.
	if age := e.clockNow().Sub(ins.StartedAt); !ins.StartedAt.IsZero() && age < staleInstanceHintGrace {
		select {
		case <-ctx.Done():
			e.staleHintSkipped(instanceID, "admission ended inside the start grace", ctx.Err())
			return false
		case <-time.After(staleInstanceHintGrace - age):
		}
		if ins, err = e.store.InstanceByID(ctx, instanceID); err != nil || ins.State != string(state.StateRunning) {
			e.staleHintSkipped(instanceID, "row left RUNNING during the start grace", err)
			return false
		}
	}
	stats, err := e.staleHintStats(ctx, ins.NodeID)
	if err != nil {
		e.staleHintSkipped(instanceID, "vmmd did not answer", err)
		return false
	}
	for _, live := range stats.Instances {
		if live.InstanceID == ins.ID {
			return false
		}
	}
	// Conditional on the state read above: a peer that parked, evicted or
	// migrated the row meanwhile wins.
	err = e.store.UpdateInstanceStateIf(ctx, ins.ID, ins.State, string(state.StateFailed))
	switch {
	case err == nil:
		e.ledger.Release(ins.ID)
		if e.ops != nil {
			e.ops.InstanceDivergence("failed").Inc()
		}
		if e.log != nil {
			e.log.Warn("sched: stale instance hint confirmed; vmmd does not have this VM",
				"instance_id", ins.ID, "app_id", ins.AppID, "node_id", ins.NodeID)
		}
		if e.events != nil {
			e.events.EmitRecovery(ctx, events.InstanceFailedEvent{
				EmitAt: time.Now().UTC(), InstanceID: ins.ID, AppID: ins.AppID, NodeID: ins.NodeID,
				Reason: "vmm_divergence",
			})
		}
		return true
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrNotFound):
		e.ledger.Release(ins.ID)
		return false
	default:
		if e.log != nil {
			e.log.Warn("sched: stale instance hint: transition failed", "instance_id", ins.ID, "err", err)
		}
		return false
	}
}

// staleHintStatsRetry is how often the hint retries a failed Stats call within
// staleInstanceHintTimeout. The recovery admission often reaches a schedd that
// started a moment ago and has not reached the node's vmmd yet (the gate's
// owner_crash: the hint arrived 0.2 s after schedd listened); one failed call
// would drop the only evidence the gateway sends.
const staleHintStatsRetry = 200 * time.Millisecond

// staleHintStats asks the node's vmmd for its live set, retrying failed calls
// until staleInstanceHintTimeout.
func (e *Engine) staleHintStats(ctx context.Context, nodeID string) (*StatsSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, staleInstanceHintTimeout)
	defer cancel()
	for {
		stats, err := e.vmm.Stats(ctx, e.nodeForRoute(nodeID))
		if err == nil && stats != nil {
			return stats, nil
		}
		if err == nil {
			err = errors.New("empty stats snapshot")
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(staleHintStatsRetry):
		}
	}
}

// staleHintSkipped records why a hint left the row alone; without it a hint
// that changed nothing is indistinguishable from one never sent.
func (e *Engine) staleHintSkipped(instanceID, reason string, err error) {
	if e.log != nil {
		e.log.Info("sched: stale instance hint not applied", "instance_id", instanceID, "reason", reason, "err", err)
	}
}
