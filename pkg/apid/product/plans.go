package product

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) customerPlans(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductSelfServiceStore, state.ProductPlanScope, bool) {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || !id.Access.Permissions.ManagePlan {
		customerPermissionAllowed(w, api.ProductPermissions{}, api.ProductPermissionManagePlan)
		return nil, state.ProductPlanScope{}, false
	}
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, state.ProductPlanScope{}, false
	}
	store, ok := s.Host.Store().(state.ProductSelfServiceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Product Control plan storage is unavailable"))
		return nil, state.ProductPlanScope{}, false
	}
	return store, state.ProductPlanScope{AccountID: acct.ID, AppID: app.ID, TenantID: id.TenantID, AuthApp: id.AuthApp, AuthAppID: id.AuthAppID, OrganizationID: id.OrganizationID, Role: id.Access.Role}, true
}

func (s *Service) CustomerPlans(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.customerPlans(w, r, acct)
	if !ok {
		return
	}
	value, err := store.GetProductPlanOptions(r.Context(), scope)
	planRespond(w, value, err)
}

func (s *Service) CustomerPlanPreview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.customerPlans(w, r, acct)
	if !ok {
		return
	}
	var selection api.ProductPlanSelection
	if !operation.DecodeOperationBody(w, r, &selection, api.ProductControlAuthBodyBytes) {
		return
	}
	value, err := store.PreviewProductPlanChange(r.Context(), scope, selection)
	planRespond(w, value, err)
}

func (s *Service) CustomerPlanConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.customerPlans(w, r, acct)
	if !ok {
		return
	}
	var confirmation api.ProductPlanChangeConfirmation
	if !operation.DecodeOperationBody(w, r, &confirmation, api.ProductControlAuthBodyBytes) {
		return
	}
	value, err := store.ApplyProductPlanChange(r.Context(), scope, confirmation)
	if err == nil {
		s.Host.EmitAudit(r.Context(), "product.customer.plan_changed", &acct.ID, map[string]any{"app_id": scope.AppID, "tenant_id": scope.TenantID, "change_id": value.ID, "plan": value.Preview.AfterCustomer.Plan, "revision": value.Preview.AfterCustomer.Revision, "role": value.ActorRole})
	}
	planRespond(w, value, err)
}

func (s *Service) CustomerPlanChange(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.customerPlans(w, r, acct)
	if !ok {
		return
	}
	value, err := store.GetProductPlanChange(r.Context(), scope, r.PathValue("change"))
	planRespond(w, value, err)
}

func (s *Service) CustomerPlanHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.customerPlans(w, r, acct)
	if !ok {
		return
	}
	value, err := store.ListProductPlanChanges(r.Context(), scope, r.URL.Query().Get("cursor"))
	planRespond(w, value, err)
}

func planRespond(w http.ResponseWriter, value any, err error) {
	switch {
	case errors.Is(err, state.ErrProductPlanChangeStale):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_plan_review_changed", "Plan review changed", "your plan, offered choices, prices, month or usage changed; review the current terms before confirming again"))
	case errors.Is(err, state.ErrProductPlanNotOffered):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_plan_not_offered", "Plan change unavailable", "this plan change is not currently offered for your workspace; contact your product team"))
	default:
		respond(w, value, err)
	}
}
