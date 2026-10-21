package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/tenantplans"
	"github.com/onebox-faas/faas/pkg/state"
)

// tenantPlansHost adapts *server to tenantplans.Host (ADR-982, ADR-1307).
type tenantPlansHost struct{ s *server }

var _ tenantplans.Host = tenantPlansHost{}

func (h tenantPlansHost) Store() state.Store { return h.s.store }

func (h tenantPlansHost) TenantsAllowed(w http.ResponseWriter, acct state.Account) bool {
	_, ok := h.s.platformTenantStore(w, acct)
	return ok
}

func (h tenantPlansHost) TenantByPath(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, bool) {
	tenants, ok := h.s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, false
	}
	return h.s.platformTenantByPath(w, r, acct, tenants)
}

func (h tenantPlansHost) CreateTenantRateCard(ctx context.Context, acct state.Account, tenantID string,
	req api.CreatePlatformTenantRateCardRequest) (state.PlatformTenantRateCard, *api.Problem) {
	store, ok := h.s.store.(state.PlatformTenantRateCardStore)
	if !ok {
		return state.PlatformTenantRateCard{}, api.ErrInternal("platform tenant pricing is unavailable")
	}
	return h.s.createTenantRateCard(ctx, acct, tenantID, store, req)
}

func (h tenantPlansHost) SetTenantRequestBudget(ctx context.Context, acct state.Account, tenantID string, perMinute, perDay int64) error {
	store, ok := h.s.store.(state.PlatformTenantRequestBudgetStore)
	if !ok {
		return errors.New("platform tenant request budgets are unavailable")
	}
	budget, err := store.SetPlatformTenantRequestBudget(ctx, acct.ID, tenantID, perMinute, perDay)
	if err != nil {
		return err
	}
	h.s.audit.Emit(ctx, "platform_tenant.request_budget_updated", &acct.ID, map[string]any{
		"tenant_id": tenantID, "max_requests_per_minute": budget.MaxRequestsPerMinute,
		"max_requests_per_day": budget.MaxRequestsPerDay, "source": "plan"})
	return nil
}

func (h tenantPlansHost) Audit(ctx context.Context, event, accountID string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, &accountID, fields)
}
