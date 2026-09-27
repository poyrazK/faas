package gateway

import (
	"context"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// TenantRequestBudgetStore admits one request against a customer-wide,
// cross-replica counter. It must never fall back to a local allowance on error.
type TenantRequestBudgetStore interface {
	AdmitTenantRequest(context.Context, string, string) (TenantRequestBudgetDecision, error)
}

type TenantRequestBudgetDecision struct {
	Allowed           bool
	Scope             string
	Limit             int64
	Observed          int64
	RetryAfterSeconds int64
}

type suppressFinancialUsageKey struct{}

func suppressFinancialUsage(r *http.Request) {
	*r = *r.WithContext(context.WithValue(r.Context(), suppressFinancialUsageKey{}, true))
}

func (h *Handler) WithTenantRequestBudgetStore(store TenantRequestBudgetStore) *Handler {
	h.tenantRequestBudgetStore = store
	h.tenantRequestBudgetEnabled = true
	return h
}

func (h *Handler) enforceTenantRequestBudget(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App, deploymentSmoke bool) bool {
	tenantID := authenticatedFrom(r.Context()).PlatformTenantID
	if deploymentSmoke || !h.tenantRequestBudgetEnabled || tenantID == "" {
		return true
	}
	if h.tenantRequestBudgetStore == nil {
		h.denyTenantBudgetUnavailable(w, r, rec, app)
		return false
	}
	decision, err := h.tenantRequestBudgetStore.AdmitTenantRequest(r.Context(), app.AccountID, tenantID)
	if err != nil {
		if h.log != nil {
			h.log.Error("platform tenant request admission unavailable", "err", err, "tenant_id", tenantID)
		}
		h.denyTenantBudgetUnavailable(w, r, rec, app)
		return false
	}
	if decision.Allowed {
		return true
	}
	suppressFinancialUsage(r)
	if decision.RetryAfterSeconds < 1 {
		decision.RetryAfterSeconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(decision.RetryAfterSeconds, 10))
	w.Header().Set("x-faas-rate-limit-scope", "platform-tenant")
	api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "tenant_request_budget_exceeded",
		"Customer request budget exceeded", "the platform customer has exhausted its request budget"))
	h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
	return false
}

func (h *Handler) denyTenantBudgetUnavailable(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App) {
	suppressFinancialUsage(r)
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "tenant_request_budget_unavailable",
		"Customer request budget unavailable", "request admission could not be verified"))
	h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
}
