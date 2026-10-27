// adr: 20261027173012
package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

// settleAtCapacity decides a request schedd answered at capacity. Schedd
// counts instances this gateway may not route to yet: a deployment check's
// instance, a wake started by another producer, or one being stopped. Only
// when this gateway itself sees effectiveMax instances is the answer the
// customer's limit (429 app_concurrency_reached with a true count). Otherwise
// it re-reads the app's live targets and waits briefly for one to become
// routable; pick reports one when it does. Past the wait it is
// ErrInstanceSlotsSettling (#5709: "max_concurrency is 1; 0 already live").
func (h *Handler) settleAtCapacity(ctx context.Context, app App, limits api.Limits, effectiveMax int, pick func() PickResult) (PickResult, error) {
	if observed := backendCapacityCount(h.backend, app.ID); observed >= effectiveMax {
		return PickResult{}, api.ErrAppConcurrencyReachedAt(limits, effectiveMax, observed)
	}
	if reconciler, ok := h.backend.(liveTargetReconciler); ok {
		if err := reconciler.ReconcileLiveTargets(ctx, app.ID); err != nil && h.log != nil {
			h.log.Warn("gateway: live target reconciliation after an at-capacity admission failed", "app_id", app.ID, "err", err)
		}
	}
	wait := time.NewTimer(atCapacitySettleWait)
	defer wait.Stop()
	poll := time.NewTicker(atCapacitySettlePoll)
	defer poll.Stop()
	for {
		if picked := pick(); picked.OK {
			return picked, nil
		}
		select {
		case <-ctx.Done():
			return PickResult{}, ctx.Err()
		case <-wait.C:
			return PickResult{}, ErrInstanceSlotsSettling
		case <-poll.C:
		}
	}
}
