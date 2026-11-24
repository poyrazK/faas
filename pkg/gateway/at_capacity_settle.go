// adr: 20261027173012
package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// ErrInstanceSlotsSettling marks an admission schedd answered at capacity
// while this gateway could not route to any instance holding the app's
// slots: they are still starting, being verified, or stopping. It is a
// retryable platform condition, not the customer's concurrency limit.
var ErrInstanceSlotsSettling = errors.New("gateway: the app's instance slots are held by instances that are not routable yet")

// atCapacitySettleWait bounds how long a request answered at capacity waits
// for the instance holding the slot to become routable; atCapacitySettlePoll
// is how often it looks.
const (
	atCapacitySettleWait = 2 * time.Second
	atCapacitySettlePoll = 50 * time.Millisecond
)

// settleAtCapacity decides a request schedd answered at capacity. An
// at-capacity answer already rehydrated the app's targets, so a replica pick
// now finds serves the request. Schedd also counts instances this gateway may
// not route to yet: a deployment check's instance, a wake started by another
// producer, or one being stopped. Only when this gateway itself sees
// effectiveMax instances is the answer the customer's limit (429
// app_concurrency_reached with a true count). Otherwise it re-reads the app's
// live targets and waits briefly for one to become routable. Past the wait it
// is ErrInstanceSlotsSettling (#5709: "max_concurrency is 1; 0 already live").
//
// deploymentID names the requesting lane (empty for production): only that
// lane's instances count against its limit, so a warm stage instance does not
// turn production's requests into 429s (prod 2026-11-19).
func (h *Handler) settleAtCapacity(ctx context.Context, app App, deploymentID string, limits api.Limits, effectiveMax int, pick func() PickResult) (PickResult, error) {
	if picked := pick(); picked.OK {
		return picked, nil
	}
	if observed := backendLaneCapacityCount(h.backend, app.ID, deploymentID); observed >= effectiveMax {
		return PickResult{}, api.ErrAppConcurrencyReachedAt(limits, effectiveMax, observed)
	}
	if reconciler, ok := h.backend.(liveTargetReconciler); ok {
		if err := reconciler.ReconcileLiveTargets(ctx, app.ID); err != nil && h.log != nil {
			h.log.Warn("gateway: live target reconciliation after an at-capacity admission failed", "app_id", app.ID, "err", err)
		}
	}
	start := time.Now()
	settle := start.Add(atCapacitySettleWait)
	// A boot another gateway or producer started holds the slot: wait for it
	// up to the app's cold-wake budget, as a request joining this gateway's
	// own wake would, instead of answering 503 after the flat settle wait.
	join := start.Add(WakeAdmissionPolicyForAppWithWakeLimits(app.Plan, app.ConcurrencyOverflow, app.MaxQueueWaitMS, app.WakeMaxQueueDepth, app.WakeMaxQueueWaitSeconds).MaxWait)
	tracker, _ := h.backend.(bootInProgressReader)
	poll := time.NewTicker(atCapacitySettlePoll)
	defer poll.Stop()
	for {
		if picked := pick(); picked.OK {
			return picked, nil
		}
		now := time.Now()
		if !now.Before(settle) && (tracker == nil || !now.Before(join) || !tracker.BootInProgress(app.ID)) {
			return PickResult{}, ErrInstanceSlotsSettling
		}
		select {
		case <-ctx.Done():
			return PickResult{}, ctx.Err()
		case <-poll.C:
		}
	}
}

// bootInProgressReader is implemented by PGBackend: it reports whether an
// instance of the app was announced waking or cold booting and has not
// reached another state yet.
type bootInProgressReader interface {
	BootInProgress(appID string) bool
}

// skipsAdmitFastPath reports whether an admission must reach schedd even when
// the gateway's cached targets already fill the app's concurrency budget.
//
// A deployment's post-readiness smoke must: the cached targets can belong to
// an older release at 0% traffic, and only schedd can park that idle sibling
// to free the slot (parkZeroTrafficSibling). Answering at capacity locally
// meant schedd never saw the smoke; it was refused until the old instance
// idled out, past the verification window, and the deploy failed as
// deployment_verification_unavailable (prod 2026-11-23: 1 in 4 back-to-back
// redeploys of a max_concurrency=1 MCP app; ADR-20261123120000).
func skipsAdmitFastPath(trigger string) bool {
	return trigger == sched.TriggerDeploymentSmoke
}
