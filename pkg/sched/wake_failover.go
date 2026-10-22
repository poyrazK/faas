// adr: 20261022160000 — re-place a wake a node refused before the VM started.
package sched

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// production-us 2026-10-22: a node rolled back mid-release kept answering
// heartbeats while its vmmd refused every wake before starting a VM, and
// placement kept choosing it. A wake now runs as a sequence of attempts: when
// vmmd refuses one for a node-local reason, the attempt is released like any
// boot error and the next attempt is placed on another node, inside the same
// caller deadline.

type wakeFailoverKey struct{}

// wakeFailover is one logical wake's state across its attempts.
type wakeFailover struct {
	// excluded holds the nodes that refused this wake.
	excluded []string
	// alternate is set from the second attempt on.
	alternate bool
	// placedOn is the node the current attempt was placed on, empty while
	// the attempt has not been placed.
	placedOn string
	// refusal is set when the current attempt's node refused it.
	refusal nodeRefusal
}

type nodeRefusal struct {
	nodeID, reason string
}

func wakeFailoverFrom(ctx context.Context) *wakeFailover {
	failover, _ := ctx.Value(wakeFailoverKey{}).(*wakeFailover)
	return failover
}

// excludedWakeNodes lists the nodes placement must skip for this wake.
func excludedWakeNodes(ctx context.Context) []string {
	if failover := wakeFailoverFrom(ctx); failover != nil {
		return slices.Clone(failover.excluded)
	}
	return nil
}

// isWakeFailoverAlternate reports whether this admission re-places a refused
// wake. The first attempt already passed, or armed, the wake-rate limiter,
// the scale-out cooldown and the snapshot-miss backoff for this wake.
func isWakeFailoverAlternate(ctx context.Context) bool {
	failover := wakeFailoverFrom(ctx)
	return failover != nil && failover.alternate
}

func noteWakePlacement(ctx context.Context, nodeID string) {
	if failover := wakeFailoverFrom(ctx); failover != nil {
		failover.placedOn = nodeID
	}
}

// noteWakeBootError records a vmmd boot error that the node, not the app,
// caused before the VM started.
func noteWakeBootError(ctx context.Context, nodeID string, err error) {
	failover := wakeFailoverFrom(ctx)
	if failover == nil {
		return
	}
	if reason, ok := nodeRefusalReason(err); ok {
		failover.refusal = nodeRefusal{nodeID: nodeID, reason: reason}
	}
}

// nodeRefusalReason classifies a vmmd boot error. Only refusals that vmmd
// gives before any guest code runs, and that depend on the node, qualify. A
// fixable CRITICAL finding is decided from the canonical sidecar and refuses
// on every node; customer startup failures ran customer code; internal and
// transport errors may have left a VM behind.
func nodeRefusalReason(err error) (string, bool) {
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeScanCritical {
		return "", false
	}
	// A peer that predates grpcerr's separate title field sends
	// "title: detail" as the title.
	switch title := problem.Title; {
	case strings.HasPrefix(title, api.ScanCriticalTitleGenerationUnavailable),
		strings.HasPrefix(title, api.ScanCriticalTitleGenerationMismatch):
		return wire.WakeFailoverReasonBaseGeneration, true
	case strings.HasPrefix(title, api.ScanCriticalTitleSidecarMissing):
		return wire.WakeFailoverReasonScanSidecarMissing, true
	}
	return "", false
}

// admitWithWakeFailover runs one admission and re-places it on another node
// while its node refuses it, up to api.WakeFailoverMaxAlternates times. Each
// refused attempt has released its reservation before the next one admits,
// and every attempt passes NodeLedger.Admit, so §6.2 invariants 1 and 2 hold.
func (e *Engine) admitWithWakeFailover(ctx context.Context, appID, mode string, attempt func(context.Context) (WakeResult, error)) (WakeResult, error) {
	if mode == string(state.InstanceModeMirror) {
		// Mirror admissions are best-effort shadows under their own deadline.
		return attempt(ctx)
	}
	failover := &wakeFailover{}
	ctx = context.WithValue(ctx, wakeFailoverKey{}, failover)
	result, err := attempt(ctx)
	for err != nil && failover.refusal.nodeID != "" {
		refusal, refusalErr := failover.refusal, err
		failover.excluded = append(failover.excluded, refusal.nodeID)
		e.log.Warn("wake: node refused the wake before starting a VM",
			"app", appID, "node", refusal.nodeID, "reason", refusal.reason,
			"attempt", len(failover.excluded), "err", err)
		if outcome, stop := wakeFailoverStop(ctx, len(failover.excluded)); stop {
			e.ops.RecordWakeFailover(refusal.reason, outcome)
			return result, refusalErr
		}
		failover.alternate, failover.placedOn, failover.refusal = true, "", nodeRefusal{}
		result, err = attempt(ctx)
		switch {
		case err == nil && result.InstanceID != "":
			e.ops.RecordWakeFailover(refusal.reason, wire.WakeFailoverOutcomeSucceeded)
			e.log.Info("wake: re-placed a refused wake", "app", appID,
				"refused_by", failover.excluded, "node", result.NodeID, "instance", result.InstanceID)
		case err == nil:
			// At capacity: another wake took the app's last slot meanwhile.
			e.ops.RecordWakeFailover(refusal.reason, wire.WakeFailoverOutcomeNoAlternate)
		case failover.refusal.nodeID != "":
			e.ops.RecordWakeFailover(refusal.reason, wire.WakeFailoverOutcomeRefusedAgain)
		case failover.placedOn == "":
			// No other node took the attempt. The refusal explains the
			// failed wake better than the fleet's lack of headroom.
			e.ops.RecordWakeFailover(refusal.reason, wire.WakeFailoverOutcomeNoAlternate)
			return WakeResult{}, refusalErr
		default:
			e.ops.RecordWakeFailover(refusal.reason, wire.WakeFailoverOutcomeFailed)
		}
	}
	return result, err
}

// wakeFailoverStop reports whether a refused wake must end without another
// attempt, and the outcome to record.
func wakeFailoverStop(ctx context.Context, refused int) (string, bool) {
	if refused > api.WakeFailoverMaxAlternates {
		return wire.WakeFailoverOutcomeNoAlternate, true
	}
	if ctx.Err() != nil {
		return wire.WakeFailoverOutcomeOutOfTime, true
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < api.WakeFailoverMinRemainingMs*time.Millisecond {
		return wire.WakeFailoverOutcomeOutOfTime, true
	}
	return "", false
}
