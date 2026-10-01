// adr: 375
package gateway

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

type rateAdmissionKey struct{}

func withRateAdmissionEvidence(ctx context.Context) context.Context {
	return context.WithValue(ctx, rateAdmissionKey{}, new(atomic.Bool))
}

func markRateAdmissionUnavailable(ctx context.Context) {
	if unavailable, ok := ctx.Value(rateAdmissionKey{}).(*atomic.Bool); ok {
		unavailable.Store(true)
	}
}

func writeRateAdmissionUnavailable(w http.ResponseWriter, r *http.Request) bool {
	if requestBudgetExpired(r.Context()) {
		writeRequestBudgetExceededForRequest(w, r)
		return true
	}
	if unavailable, ok := r.Context().Value(rateAdmissionKey{}).(*atomic.Bool); ok && unavailable.Load() {
		recordTrafficRefusal(r.Context(), "rate_limit_unavailable")
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable,
			"rate_limit_unavailable", "Request admission unavailable", "the shared rate allowance could not be verified"))
		return true
	}
	return false
}

// Account/app and tenant budgets charge authenticated ordinary public requests
// once before cache lookup. Retries use their separate amplification counter.
// Platform health, CORS preflight and earlier fixed edge responses retain their
// own controls; deployment verification does not consume customer allowance.
func (h *Handler) enforceTrafficRates(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App, smoke bool) bool {
	markTrafficPhase(r.Context(), trafficRates)
	if smoke {
		return true
	}
	if app.AccountID == "" {
		h.warnEmptyAccountOnce()
	} else if !h.accountLimiter.AllowAccount(r.Context(), app.AccountID, app.Plan) {
		h.rejectTrafficRate(w, r, app, "account")
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	if !h.limiter.AllowAppWithLimits(r.Context(), app.ID, app.Plan, app.RequestRateLimitRPS, app.RequestRateLimitBurst) {
		h.rejectTrafficRate(w, r, app, "app")
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	h.writeAppRateLimitHeaders(w, app.ID, app.Plan)
	return h.enforceTenantRequestBudget(w, r, rec, app, false)
}

func (h *Handler) rejectTrafficRate(w http.ResponseWriter, r *http.Request, app App, scope string) {
	recordTrafficLimiter(r.Context(), scope)
	recordTrafficRefusal(r.Context(), "rate_limited")
	w.Header().Set("x-faas-rate-limit-scope", scope)
	if writeRateAdmissionUnavailable(w, r) {
		return
	}
	w.Header().Set("Retry-After", "1")
	if scope == "account" {
		h.writeAccountRateLimitHeaders(w, app.AccountID, app.Plan)
		if h.metrics != nil {
			h.metrics.ObserveAccountRateLimit(app.AccountID, string(app.Plan))
		}
	} else {
		h.writeAppRateLimitHeaders(w, app.ID, app.Plan)
		if h.metrics != nil {
			h.metrics.ObserveRateLimit(app.ID, string(app.Plan))
		}
	}
	api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited", "Rate limit exceeded", "slow down and retry"))
}
