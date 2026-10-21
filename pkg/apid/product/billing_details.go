package product

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Customer billing details (ADR-20261020120000) live in the owner's Paddle,
// on the Paddle customer that the workspace's platform tenant names as its
// external reference. Gregale keeps only the audit event.

// billingDetailsCustomer is the workspace's provider customer ID.
func (s *Service) billingDetailsCustomer(w http.ResponseWriter, r *http.Request, acct state.Account) (authmw.ProductCustomerIdentity, state.App, string, bool) {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || !id.Access.Permissions.ManageBilling {
		customerPermissionAllowed(w, api.ProductPermissions{}, api.ProductPermissionManageBilling)
		return id, state.App{}, "", false
	}
	if s.BillingDetails == nil || s.PutBillingDetails == nil {
		api.WriteProblem(w, api.ErrCapacity("billing details are unavailable"))
		return id, state.App{}, "", false
	}
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return id, state.App{}, "", false
	}
	tenants, ok := s.Host.Store().(state.PlatformTenantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("platform tenants are unavailable"))
		return id, state.App{}, "", false
	}
	tenant, err := tenants.GetPlatformTenant(r.Context(), acct.ID, id.TenantID)
	if err != nil {
		respond(w, nil, err)
		return id, state.App{}, "", false
	}
	return id, app, tenant.ExternalRef, true
}

func (s *Service) CustomerBillingDetails(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, _, customer, ok := s.billingDetailsCustomer(w, r, acct)
	if !ok {
		return
	}
	value, problem := s.BillingDetails(r.Context(), acct, customer)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, value)
}

// PutCustomerBillingDetails sets the workspace's billing address and, when
// given, its business. The audit names the changed parts, never their values.
func (s *Service) PutCustomerBillingDetails(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, app, customer, ok := s.billingDetailsCustomer(w, r, acct)
	if !ok {
		return
	}
	var req api.PutProductBillingDetailsRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	value, changed, problem := s.PutBillingDetails(r.Context(), acct, customer, req)
	if len(changed) > 0 {
		s.Host.EmitAudit(r.Context(), "product.customer.billing_details_updated", &acct.ID, map[string]any{"app_id": app.ID, "tenant_id": id.TenantID,
			"provider": value.Provider, "changed": changed, "role": id.Access.Role})
	}
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, value)
}
