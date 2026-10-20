package product

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) billing(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, period string) (state.ProductBillingStore, state.ProductBillingScope, bool) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, state.ProductBillingScope{}, false
	}
	store, ok := s.Host.Store().(state.ProductBillingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Product Control billing storage is unavailable"))
		return nil, state.ProductBillingScope{}, false
	}
	return store, state.ProductBillingScope{AccountID: acct.ID, AppID: app.ID, TenantID: tenant, Period: period}, true
}

func billingRespond(w http.ResponseWriter, value any, err error) {
	if problem := billingConflict(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	respond(w, value, err)
}

// BillingProblem is the problem for an error of FinalizeStatement.
func BillingProblem(err error) *api.Problem {
	switch problem := billingConflict(err); {
	case problem != nil:
		return problem
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Product unavailable", "no product catalog or customer plan is available")
	case errors.Is(err, state.ErrInvalidArgument):
		return api.ErrValidation("invalid product billing scope")
	default:
		return api.ErrInternal("product billing failed")
	}
}

// billingConflict is the 409 for a billing rule, nil for any other error.
func billingConflict(err error) *api.Problem {
	code, detail := "", ""
	switch {
	case errors.Is(err, state.ErrProductBillingReviewChanged):
		code, detail = "product_billing_review_changed", "Review the period again and confirm its current review hash."
	case errors.Is(err, state.ErrProductBillingUnresolved):
		code, detail = "product_billing_unresolved", "Reconcile unresolved operations or explicitly waive their charges before finalizing."
	case errors.Is(err, state.ErrProductBillingOpenPeriod):
		code, detail = "product_billing_period_open", "Only a closed UTC month can be finalized."
	case errors.Is(err, errDocumentExported):
		code, detail = "product_billing_exported", "This statement was exported to your billing provider, which records its invoice as the handoff. Read it with statements export-status."
	case errors.Is(err, state.ErrConflict):
		code, detail = "product_billing_conflict", "This change conflicts with retained billing evidence. Finalize charges before crediting; preserve credit IDs and invoice references on retries."
	}
	if code == "" {
		return nil
	}
	return api.NewProblem(http.StatusConflict, code, "Billing change unavailable", detail)
}

// FinalizeStatement freezes a closed, resolved month with its reviewed
// hash. The endpoint and billing runs (ADR-20261018212918) share it, so both
// apply the same rules and audit event.
func (s *Service) FinalizeStatement(ctx context.Context, acct state.Account, scope state.ProductBillingScope, expectedHash string) (api.ProductBillingDocument, error) {
	store, ok := s.Host.Store().(state.ProductBillingStore)
	if !ok {
		return api.ProductBillingDocument{}, errors.New("product control billing storage is unavailable")
	}
	value, err := store.FinalizeProductBilling(ctx, scope, expectedHash)
	if err == nil {
		s.Host.EmitAudit(ctx, "product.billing.finalized", &acct.ID, map[string]any{"app_id": scope.AppID, "tenant_id": scope.TenantID,
			"period": scope.Period, "document_id": value.ID, "amount_millicents": value.AmountMillicents})
	}
	return value, err
}

// ExportStatement invoices a finalized statement in the account's provider;
// the endpoint and billing runs share it.
func (s *Service) ExportStatement(ctx context.Context, acct state.Account, scope state.ProductBillingScope, document, provider,
	customer string) (api.StatementInvoiceExportResponse, *api.Problem) {
	if s.ExportInvoice == nil {
		return api.StatementInvoiceExportResponse{}, api.ErrCapacity("statement invoice exports are unavailable")
	}
	value, problem := s.ExportInvoice(ctx, acct, scope, document, provider, customer)
	if problem == nil {
		s.Host.EmitAudit(ctx, "product.billing.invoice_exported", &acct.ID, map[string]any{"app_id": scope.AppID, "tenant_id": scope.TenantID,
			"document_id": value.StatementID, "provider": value.Provider, "invoice_id": value.InvoiceID, "livemode": value.Livemode})
	}
	return value, problem
}

func (s *Service) BillingReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.billing(w, r, acct, r.URL.Query().Get("tenant_id"), requestPeriod(r))
	if !ok {
		return
	}
	value, err := store.ReviewProductBilling(r.Context(), scope)
	billingRespond(w, value, err)
}

func (s *Service) BillingReceipts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.billing(w, r, acct, r.URL.Query().Get("tenant_id"), requestPeriod(r))
	if !ok {
		return
	}
	value, err := store.ListProductBillingReceipts(r.Context(), scope, r.URL.Query().Get("cursor"))
	billingRespond(w, value, err)
}

func (s *Service) BillingDocuments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.billing(w, r, acct, r.URL.Query().Get("tenant_id"), requestPeriod(r))
	if !ok {
		return
	}
	value, err := store.ListProductBillingDocuments(r.Context(), scope, r.URL.Query().Get("cursor"))
	if err == nil {
		err = s.withInvoices(r.Context(), acct.ID, &value, true)
	}
	billingRespond(w, value, err)
}

func (s *Service) BillingFinalize(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductFinalizeRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	_, scope, ok := s.billing(w, r, acct, req.TenantID, req.Period)
	if !ok {
		return
	}
	value, err := s.FinalizeStatement(r.Context(), acct, scope, req.ExpectedHash)
	billingRespond(w, value, err)
}

func (s *Service) BillingWaive(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductWaiverRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	store, scope, ok := s.billing(w, r, acct, req.TenantID, requestPeriod(r))
	if !ok {
		return
	}
	value, err := store.WaiveProductReceipt(r.Context(), scope, r.PathValue("operation"), req.Reason)
	if err == nil {
		s.Host.EmitAudit(r.Context(), "product.billing.waived", &acct.ID, map[string]any{"app_id": scope.AppID, "tenant_id": scope.TenantID, "operation_id": value.OperationID})
	}
	billingRespond(w, value, err)
}

func (s *Service) BillingCredit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductCreditRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	store, scope, ok := s.billing(w, r, acct, req.TenantID, requestPeriod(r))
	if !ok {
		return
	}
	value, err := store.CreditProductReceipt(r.Context(), scope, req)
	if err == nil {
		s.Host.EmitAudit(r.Context(), "product.billing.credited", &acct.ID, map[string]any{"app_id": scope.AppID, "tenant_id": scope.TenantID, "document_id": value.ID, "operation_id": value.OperationID, "amount_millicents": value.AmountMillicents})
	}
	billingRespond(w, value, err)
}

func (s *Service) BillingHandoff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductHandoffRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	store, scope, ok := s.billing(w, r, acct, req.TenantID, requestPeriod(r))
	if !ok {
		return
	}
	value, err := s.handoffManually(r.Context(), store, acct.ID, scope, r.PathValue("document"), req.ExternalInvoiceID)
	if err == nil {
		s.Host.EmitAudit(r.Context(), "product.billing.invoice_handoff", &acct.ID, map[string]any{"app_id": scope.AppID, "tenant_id": scope.TenantID, "document_id": value.DocumentID})
	}
	billingRespond(w, value, err)
}

func (s *Service) BillingGetHandoff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.billing(w, r, acct, r.URL.Query().Get("tenant_id"), requestPeriod(r))
	if !ok {
		return
	}
	value, err := store.GetProductBillingHandoff(r.Context(), scope, r.PathValue("document"))
	billingRespond(w, value, err)
}

// BillingExport exports a finalized statement to the account's own Stripe,
// Paddle or Lago; the provider invoice becomes its handoff
// (ADR-20261018212918). A retry resumes the same provider invoice.
func (s *Service) BillingExport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductInvoiceExportRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	_, scope, ok := s.billing(w, r, acct, req.TenantID, requestPeriod(r))
	if !ok {
		return
	}
	value, problem := s.ExportStatement(r.Context(), acct, scope, r.PathValue("document"), req.Provider, req.Customer)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, value)
}

// BillingGetExport reads a statement's export and its provider payment
// status (ADR-1504).
func (s *Service) BillingGetExport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, scope, ok := s.billing(w, r, acct, r.URL.Query().Get("tenant_id"), requestPeriod(r))
	if !ok {
		return
	}
	if s.InvoiceExport == nil {
		api.WriteProblem(w, api.ErrCapacity("statement invoice exports are unavailable"))
		return
	}
	value, problem := s.InvoiceExport(r.Context(), acct, scope, r.PathValue("document"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, value)
}

func (s *Service) CustomerBilling(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return
	}
	if r.PathValue("slug") == "" {
		r.SetPathValue("slug", r.URL.Query().Get("app"))
	}
	store, scope, ok := s.billing(w, r, acct, tenant, requestPeriod(r))
	if !ok {
		return
	}
	products, ok := s.Host.Store().(state.ProductControlStore)
	if !ok {
		respond(w, nil, state.ErrNotFound)
		return
	}
	if _, err := products.GetProductCustomer(r.Context(), acct.ID, scope.AppID, tenant); err != nil {
		respond(w, nil, err)
		return
	}
	review, err := store.ReviewProductBilling(r.Context(), scope)
	if err != nil {
		billingRespond(w, nil, err)
		return
	}
	docs, err := store.ListProductBillingDocuments(r.Context(), scope, r.URL.Query().Get("cursor"))
	if err == nil {
		err = s.withInvoices(r.Context(), acct.ID, &docs, false)
	}
	billingRespond(w, api.ProductCustomerBilling{Review: review, Documents: docs}, err)
}
