// adr: 375
package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

func (h *Handler) publicRoutingWakeMaximum(app App, routing PublicRoutingSnapshot) int {
	maximum := effectiveAppConcurrencyLimit(app, api.MustLimitsFor(app.Plan).MaxConcurrency)
	if routing.SelectionReason != "weighted" && routing.SelectionReason != "version" && routing.SelectionReason != "session" {
		return maximum + api.RolloutConcurrencyGrant
	}
	// Only a second positive cohort coming up beside a routable cohort gets
	// the existing overlap allowance. The scheduler still owns its ledger.
	for _, weight := range routing.Weights {
		if weight.ID != routing.SelectedDeploymentID && pickPublicDeployment(h.backend, app.ID, weight.ID, "").OK {
			return maximum + api.RolloutConcurrencyGrant
		}
	}
	return maximum
}

// Keep one bounded queue and one boot generation per app. A different cohort
// waits for that generation, then starts its own only if still cold. The outer
// allowance covers all generations; joining another cohort never resets it.
func (h *Handler) wakePublicDeployment(ctx context.Context, app App, deployment, scope, trigger string, maximum int) (string, WakeMethod, bool, error) {
	policy := WakeAdmissionPolicyForAppWithWakeLimits(app.Plan, app.ConcurrencyOverflow, app.MaxQueueWaitMS, app.WakeMaxQueueDepth, app.WakeMaxQueueWaitSeconds)
	waitCtx, cancel := context.WithTimeout(ctx, policy.MaxWait)
	defer cancel()
	waitCtx = context.WithValue(waitCtx, wakeTargetKey{}, deployment)
	acceptedAt := time.Now()
	if start, ok := StartTimeFromContext(ctx); ok {
		acceptedAt = start
	}
	h.beginWakePageCycle(app.ID, acceptedAt)
	var wakeID string
	var method WakeMethod
	for {
		err := h.gate.WaitWithPolicy(waitCtx, app.ID, app.AccountID, policy,
			func() bool { return !pickPublicDeployment(h.backend, app.ID, deployment, "").OK },
			func(wakeCtx context.Context) error {
				if reconciler, ok := h.backend.(liveTargetReconciler); ok {
					if err := reconciler.ReconcileLiveTargets(wakeCtx, app.ID); err == nil && pickPublicDeployment(h.backend, app.ID, deployment, "").OK {
						h.finishWakePageCycle(wakeCtx, app.ID, "")
						return nil
					}
				}
				var atCapacity bool
				var err error
				admit := func(admitCtx context.Context) error {
					wakeID, method, atCapacity, err = h.backend.Admit(admitCtx, app.ID, deployment, scope, trigger, maximum)
					return err
				}
				if h.admissionQueue == nil {
					err = admit(wakeCtx)
				} else {
					var queued bool
					var wait time.Duration
					queued, wait, err = h.admissionQueue.Do(wakeCtx, app.ID, string(app.Plan), policy, admit)
					if h.metrics != nil {
						h.metrics.ObserveWakeAdmission(string(app.Plan), err, queued, wait)
					}
				}
				if err != nil {
					h.finishWakePageCycle(wakeCtx, app.ID, "")
					return err
				}
				if atCapacity {
					h.finishWakePageCycle(wakeCtx, app.ID, "")
					return api.ErrAppConcurrencyReachedAt(api.MustLimitsFor(app.Plan), effectiveAppConcurrencyLimit(app, api.MustLimitsFor(app.Plan).MaxConcurrency), backendCapacityCount(h.backend, app.ID))
				}
				if method == WakeMethodSnapshotRestore && h.burstPressure != nil {
					h.burstPressure.state(app.ID).settlingUntil.Store(time.Now().Add(burstInitialRestoreSettlingWindow).UnixNano())
				}
				h.finishWakePageCycle(wakeCtx, app.ID, wakeID)
				return nil
			}, nil, nil)
		if errors.Is(err, errWakeTargetChanged) {
			continue
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				err = &WakeQueueWaitTimeoutError{RetryAfter: policy.MaxWait}
			}
			return "", WakeMethodUnspecified, false, err
		}
		return wakeID, method, false, nil
	}
}

func (h *Handler) writePublicRoutingWakeError(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder, wakeWaitExpired bool, err error) {
	showPage := acceptsWakePage(r)
	if showPage && r.Context().Err() == nil && wakeWaitExpired && errors.Is(err, context.DeadlineExceeded) && h.gate.WakeInProgress(app.ID) {
		h.noteWakePageServed(r.Context(), app.ID, app.AccountID, requestIDFrom(r), time.Now())
		w.Header().Set(wire.WakeHeader, wire.ColdWakeValue)
		writeWakePage(w, r.Header.Get("x-faas-wake-id"))
	} else if trafficRevocationCause(r.Context()) != nil {
		writeTrafficRevocationError(w, r, trafficRevocationCause(r.Context()))
	} else if requestBudgetExpired(r.Context()) {
		writeRequestBudgetExceededForRequest(w, r)
	} else if !showPage && errors.Is(err, ErrWakeQueueWaitTimeout) && h.gate.WakeInProgress(app.ID) {
		writeWakeInProgress(w, requestIDFrom(r))
	} else {
		if h.metrics != nil {
			if errors.Is(err, ErrQueueFull) {
				h.metrics.ObserveWakeAdmission(string(app.Plan), err, false, 0)
			}
			if isWakeConcurrencyDrop(err) {
				h.metrics.ObserveConcurrencyThrottled(app.ID, api.ConcurrencyOverflowDrop)
			} else if errors.Is(err, ErrQueueFull) || errors.Is(err, ErrWakeQueueWaitTimeout) {
				h.metrics.ObserveConcurrencyThrottled(app.ID, api.ConcurrencyOverflowQueue)
			}
		}
		h.markHealthFailure(app.ID, err)
		if served, _ := h.tryServeStaleOnWakeError(w, r, app, rec); served {
			return
		}
		writeWakeError(w, err)
	}
	h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
}
