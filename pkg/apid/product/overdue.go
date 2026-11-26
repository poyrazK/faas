package product

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Overdue holds (ADR-20261018212918) mirror ADR-1315 for Product Control: a
// customer whose exported statement is unpaid the app policy's days after its
// due date cannot start new work until the invoice is paid.

func (s *Service) overdue(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductOverdueStore, state.App, bool) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, state.App{}, false
	}
	store, ok := s.Host.Store().(state.ProductOverdueStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Product Control overdue storage is unavailable"))
		return nil, state.App{}, false
	}
	return store, app, true
}

func overduePolicyResponse(p state.ProductOverduePolicy) api.ProductOverduePolicy {
	return api.ProductOverduePolicy{AppID: p.AppID, BlockOverdueAfterDays: p.BlockOverdueAfterDays, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func overdueHoldResponse(h state.ProductOverdueHold) api.ProductOverdueHold {
	return api.ProductOverdueHold{TenantID: h.TenantID, DocumentID: h.DocumentID, ExportID: h.ExportID, DueAt: h.DueAt, Since: h.Since,
		HostedInvoiceURL: api.ProviderHostedURL(h.HostedInvoiceURL)}
}

func (s *Service) GetOverduePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.overdue(w, r, acct)
	if !ok {
		return
	}
	policy, err := store.GetProductOverduePolicy(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the app has no overdue policy; customers are never held for unpaid invoices")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the overdue policy"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, overduePolicyResponse(policy))
}

// PutOverduePolicy sets the app's overdue rule; holds follow at the next
// recomputation.
func (s *Service) PutOverduePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.PutProductOverduePolicyRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	if req.BlockOverdueAfterDays == nil || *req.BlockOverdueAfterDays < 0 || *req.BlockOverdueAfterDays > api.ProductOverdueBlockMaxDays {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid overdue policy",
			fmt.Sprintf("block_overdue_after_days must be 0 to %d", api.ProductOverdueBlockMaxDays)))
		return
	}
	store, app, ok := s.overdue(w, r, acct)
	if !ok {
		return
	}
	policy, err := store.PutProductOverduePolicy(r.Context(), state.ProductOverduePolicy{AccountID: acct.ID, AppID: app.ID, BlockOverdueAfterDays: *req.BlockOverdueAfterDays})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the overdue policy"))
		return
	}
	s.Host.EmitAudit(r.Context(), "product.billing.overdue_policy_updated", &acct.ID, map[string]any{"app_id": app.ID,
		"block_overdue_after_days": policy.BlockOverdueAfterDays})
	httpkit.WriteJSON(w, http.StatusOK, overduePolicyResponse(policy))
}

// DeleteOverduePolicy clears the rule and lifts its holds at once.
func (s *Service) DeleteOverduePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.overdue(w, r, acct)
	if !ok {
		return
	}
	lifted, err := store.DeleteProductOverduePolicy(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the app has no overdue policy")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not remove the overdue policy"))
		return
	}
	s.Host.EmitAudit(r.Context(), "product.billing.overdue_policy_deleted", &acct.ID, map[string]any{"app_id": app.ID})
	s.auditOverdue(r.Context(), nil, lifted)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) OverdueHolds(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.overdue(w, r, acct)
	if !ok {
		return
	}
	holds, err := store.ListProductOverdueHolds(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list overdue holds"))
		return
	}
	out := api.ProductOverdueHolds{Holds: make([]api.ProductOverdueHold, 0, len(holds))}
	for _, h := range holds {
		out.Holds = append(out.Holds, overdueHoldResponse(h))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// customerOverdueHold is what a held customer's portal explains, nil when
// the customer is not held.
func customerOverdueHold(ctx context.Context, products state.ProductControlStore, accountID, appID, tenantID string) (*api.ProductCustomerOverdueHold, error) {
	store, ok := products.(state.ProductOverdueStore)
	if !ok {
		return nil, nil
	}
	hold, err := store.GetProductOverdueHold(ctx, accountID, appID, tenantID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	view := &api.ProductCustomerOverdueHold{DueAt: hold.DueAt, HostedInvoiceURL: api.ProviderHostedURL(hold.HostedInvoiceURL)}
	if identity, ok := authmw.ProductCustomerFromContext(ctx); ok && identity.TenantID == tenantID && identity.Access.Permissions.ViewBilling {
		view.PaymentCheckID = hold.ExportID
	}
	return view, nil
}

// CustomerCheckOverduePayment checks the hold presented to the signed-in
// workspace. A stale request can reload the hold, including its successor.
func (s *Service) CustomerCheckOverduePayment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	identity, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || !identity.Access.Permissions.ViewBilling {
		customerPermissionAllowed(w, api.ProductPermissions{}, api.ProductPermissionViewBilling)
		return
	}
	var req struct {
		HoldID string `json:"hold_id"`
	}
	if r.URL.RawQuery != "" {
		api.WriteProblem(w, api.ErrValidation("payment check takes no query parameters"))
		return
	}
	if !httpkit.DecodeJSONLimit(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	if !ownerUUID(req.HoldID) {
		api.WriteProblem(w, api.ErrValidation("the current payment hold reference is required"))
		return
	}
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	unavailable := api.NewProblem(http.StatusServiceUnavailable, api.CodeBillingProviderUnavailable, "Payment provider unavailable",
		"Could not confirm payment right now. Try again shortly or contact your product team.")
	if s.RefreshHeldInvoicePayment == nil {
		api.WriteProblem(w, unavailable)
		return
	}
	ctx := r.Context()
	if problem := s.RefreshHeldInvoicePayment(ctx, acct, app.ID, identity.TenantID, req.HoldID); problem != nil {
		if problem.Code == api.CodeStatementInvoiceExportInProgress {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, problem.Code, "Payment check in progress",
				"A check is already in progress. Try again shortly."))
			return
		}
		api.WriteProblem(w, unavailable)
		return
	}
	hold, err := customerOverdueHold(ctx, store, acct.ID, app.ID, identity.TenantID)
	respond(w, struct {
		OverdueHold *api.ProductCustomerOverdueHold `json:"overdue_hold"`
	}{hold}, err)
}

// StartOverdueHolds recomputes holds every api.ProductOverdueCheckInterval.
func (s *Service) StartOverdueHolds(ctx context.Context, log *slog.Logger) {
	if _, ok := s.Host.Store().(state.ProductOverdueStore); !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.ProductOverdueCheckInterval)
		defer ticker.Stop()
		for {
			if err := s.SyncOverdueOnce(ctx); err != nil && ctx.Err() == nil {
				log.Warn("product overdue check failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// SyncOverdueOnce places and lifts holds from exported statements' payment
// status (ADR-1504) and audits each change. It does nothing while Product
// Control is disabled, when no new starts are admitted anyway.
func (s *Service) SyncOverdueOnce(ctx context.Context) error {
	store, ok := s.Host.Store().(state.ProductOverdueStore)
	if !ok || !s.Enabled {
		return nil
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	placed, lifted, err := store.SyncProductOverdueHolds(ctx, now)
	if err != nil {
		return fmt.Errorf("sync product overdue holds: %w", err)
	}
	s.auditOverdue(ctx, placed, lifted)
	return nil
}

func (s *Service) auditOverdue(ctx context.Context, placed, lifted []state.ProductOverdueHold) {
	for _, h := range placed {
		s.Host.EmitAudit(ctx, "product.customer.overdue_blocked", &h.AccountID, map[string]any{"app_id": h.AppID, "tenant_id": h.TenantID,
			"document_id": h.DocumentID, "export_id": h.ExportID, "due_at": h.DueAt})
	}
	for _, h := range lifted {
		s.Host.EmitAudit(ctx, "product.customer.overdue_unblocked", &h.AccountID, map[string]any{"app_id": h.AppID, "tenant_id": h.TenantID,
			"document_id": h.DocumentID, "export_id": h.ExportID})
	}
}
