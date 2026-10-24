package product

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// A workspace billed through the owner's Stripe keeps a card on file there
// through Stripe's hosted Checkout (ADR-20261022100735). Gregale reads only
// the card's brand, last digits and expiry; Stripe keeps the card.

// paymentMethodCustomer is the workspace's Stripe customer, for roles that
// manage billing.
func (s *Service) paymentMethodCustomer(w http.ResponseWriter, r *http.Request, acct state.Account) (authmw.ProductCustomerIdentity, state.App, string, bool) {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || !id.Access.Permissions.ManageBilling {
		customerPermissionAllowed(w, api.ProductPermissions{}, api.ProductPermissionManageBilling)
		return id, state.App{}, "", false
	}
	if s.PaymentMethod == nil || s.PaymentMethodCheckout == nil || s.CompletePaymentMethod == nil {
		api.WriteProblem(w, api.ErrCapacity("payment methods are unavailable"))
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

// CustomerPaymentMethod shows the workspace's default invoice card and
// records whether one is on file (ADR-20261022180921).
func (s *Service) CustomerPaymentMethod(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, app, customer, ok := s.paymentMethodCustomer(w, r, acct)
	if !ok {
		return
	}
	card, problem := s.PaymentMethod(r.Context(), acct, customer)
	if problem != nil && problem.Code == api.CodeProductPaymentMethodUnavailable {
		httpkit.WriteJSON(w, http.StatusOK, api.ProductPaymentMethodView{})
		return
	}
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if err := s.recordPaymentMethod(r.Context(), acct, app, id.TenantID, card != nil); err != nil {
		respond(w, nil, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ProductPaymentMethodView{Available: true, PaymentMethod: card})
}

// CustomerPaymentMethodCheckout opens Stripe's hosted page that saves a card
// for future invoices, after the customer's explicit consent.
func (s *Service) CustomerPaymentMethodCheckout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductPaymentMethodCheckoutRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	id, app, customer, ok := s.paymentMethodCustomer(w, r, acct)
	if !ok {
		return
	}
	if !req.Consent {
		api.WriteProblem(w, api.ErrValidation("consent to future charges by the product team is required to save a card"))
		return
	}
	store, ok := s.Host.Store().(state.ProductControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Product Control is unavailable"))
		return
	}
	catalog, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, "")
	if err != nil {
		respond(w, nil, err)
		return
	}
	if s.EmailOrigin == "" {
		api.WriteProblem(w, api.ErrCapacity("payment methods are unavailable"))
		return
	}
	back := s.portalURL(app.Slug) + "&workspace=" + url.QueryEscape(id.OrganizationID)
	url, problem := s.PaymentMethodCheckout(r.Context(), acct, customer, catalog.Spec.Currency, "", back+"&checkout={CHECKOUT_SESSION_ID}", back+"&checkout=cancelled")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.Host.EmitAudit(r.Context(), "product.customer.payment_method_consented", &acct.ID, map[string]any{"app_id": app.ID, "tenant_id": id.TenantID,
		"organization_id": id.OrganizationID, "provider": state.BillingProviderStripe})
	httpkit.WriteJSON(w, http.StatusOK, api.ProductPaymentMethodCheckout{URL: url})
}

// CustomerPaymentMethodComplete makes the card a returned Checkout saved the
// workspace's default for invoices, and records it on file.
func (s *Service) CustomerPaymentMethodComplete(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductPaymentMethodCompleteRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	if len(req.SessionID) < 8 || len(req.SessionID) > 255 || !validCookieToken(req.SessionID) {
		api.WriteProblem(w, api.ErrValidation("session_id must be the Checkout Session the provider returned"))
		return
	}
	id, app, customer, ok := s.paymentMethodCustomer(w, r, acct)
	if !ok {
		return
	}
	card, problem := s.CompletePaymentMethod(r.Context(), acct, customer, req.SessionID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if err := s.recordPaymentMethod(r.Context(), acct, app, id.TenantID, card != nil); err != nil {
		respond(w, nil, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "product.customer.payment_method_saved", &acct.ID, map[string]any{"app_id": app.ID, "tenant_id": id.TenantID,
		"organization_id": id.OrganizationID, "provider": state.BillingProviderStripe})
	httpkit.WriteJSON(w, http.StatusOK, api.ProductPaymentMethodView{Available: true, PaymentMethod: card})
}
