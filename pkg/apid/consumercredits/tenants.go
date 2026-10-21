package consumercredits

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Platform tenants get the same prepaid credit and spend caps as consumers,
// across all of their apps (ADR-20261019100331).

func (s *Service) tenantStores(w http.ResponseWriter) (state.PlatformTenantCreditStore, state.PlatformTenantSpendCapStore, bool) {
	credits, okCredits := s.Host.Store().(state.PlatformTenantCreditStore)
	caps, okCaps := s.Host.Store().(state.PlatformTenantSpendCapStore)
	if !okCredits || !okCaps {
		api.WriteProblem(w, api.ErrInternal("platform tenant credits are unavailable"))
		return nil, nil, false
	}
	return credits, caps, true
}

func tenantCreditResponse(g state.PlatformTenantCreditGrant, now time.Time) api.PlatformTenantCreditResponse {
	consumer := creditResponse(state.APIConsumerCreditGrant{RemainingMillicents: g.RemainingMillicents, ExpiresAt: g.ExpiresAt,
		VoidedAt: g.VoidedAt}, now)
	return api.PlatformTenantCreditResponse{ID: g.ID, TenantID: g.TenantID, Currency: g.Currency, AmountMillicents: g.AmountMillicents,
		RemainingMillicents: g.RemainingMillicents, Status: consumer.Status, Note: g.Note, ExpiresAt: g.ExpiresAt, VoidedAt: g.VoidedAt,
		CreatedAt: g.CreatedAt}
}

// CreateTenantCredit grants a platform tenant prepaid credit and rechecks
// its spend cap.
func (s *Service) CreateTenantCredit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	credits, _, ok := s.tenantStores(w)
	if !ok {
		return
	}
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAPIConsumerCreditRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	now := s.now()
	if problem := validateCreditRequest(req, now); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	grant, err := credits.CreatePlatformTenantCreditGrant(r.Context(), state.PlatformTenantCreditGrant{AccountID: acct.ID,
		TenantID: tenant.ID, Currency: req.Currency, AmountMillicents: req.AmountMillicents, Note: req.Note, ExpiresAt: req.ExpiresAt})
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Too many open credits",
			fmt.Sprintf("a platform tenant holds at most %d grants with credit left; void one first", api.ConsumerCreditOpenGrantsMax)).
			WithLimit(int64(api.ConsumerCreditOpenGrantsMax), int64(api.ConsumerCreditOpenGrantsMax)).
			WithDocs("https://gregale.dev/docs/api-monetization#prepaid-credit"))
		return
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, validation("Invalid credit", err.Error()))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not grant credit"))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant_credit.granted", acct.ID, map[string]any{"tenant_id": tenant.ID,
		"credit_id": grant.ID, "currency": grant.Currency, "amount_millicents": grant.AmountMillicents})
	s.recheckTenant(r.Context(), acct.ID, tenant.ID)
	httpkit.WriteJSON(w, http.StatusCreated, tenantCreditResponse(grant, now))
}

// ListTenantCredits lists a tenant's grants with its usable balance per
// currency.
func (s *Service) ListTenantCredits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	credits, _, ok := s.tenantStores(w)
	if !ok {
		return
	}
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	grants, err := credits.ListPlatformTenantCreditGrants(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list credits"))
		return
	}
	now := s.now()
	out := api.PlatformTenantCreditListResponse{Credits: make([]api.PlatformTenantCreditResponse, 0, len(grants))}
	balances := make([]state.APIConsumerCreditGrant, 0, len(grants))
	for _, g := range grants {
		out.Credits = append(out.Credits, tenantCreditResponse(g, now))
		balances = append(balances, state.APIConsumerCreditGrant{Currency: g.Currency, RemainingMillicents: g.RemainingMillicents,
			ExpiresAt: g.ExpiresAt, VoidedAt: g.VoidedAt})
	}
	out.Balances = Balances(balances, now)
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// VoidTenantCredit voids what is left of a tenant grant.
func (s *Service) VoidTenantCredit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	credits, _, ok := s.tenantStores(w)
	if !ok {
		return
	}
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	grant, err := credits.VoidPlatformTenantCreditGrant(r.Context(), acct.ID, tenant.ID, r.PathValue("credit_id"))
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such credit")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not void the credit"))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant_credit.voided", acct.ID, map[string]any{"tenant_id": tenant.ID, "credit_id": grant.ID})
	s.recheckTenant(r.Context(), acct.ID, tenant.ID)
	httpkit.WriteJSON(w, http.StatusOK, tenantCreditResponse(grant, s.now()))
}

func tenantSpendCapResponse(c state.PlatformTenantSpendCap) api.PlatformTenantSpendCapResponse {
	out := api.PlatformTenantSpendCapResponse{TenantID: c.TenantID, Currency: c.Currency, MonthlyCapMillicents: c.MonthlyCapMillicents,
		AmountDueMillicents: c.AmountDueMillicents, CheckedAt: c.CheckedAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if c.MonthStart != nil {
		out.Month = c.MonthStart.Format("2006-01")
		out.Capped = c.CappedIn(*c.MonthStart)
	}
	return out
}

// PutTenantSpendCap sets a tenant's monthly spend cap and checks it at once.
func (s *Service) PutTenantSpendCap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, caps, ok := s.tenantStores(w)
	if !ok {
		return
	}
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	var req api.PutAPIConsumerSpendCapRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if problem := validateSpendCapRequest(req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	stored, err := caps.PutPlatformTenantSpendCap(r.Context(), state.PlatformTenantSpendCap{TenantID: tenant.ID, AccountID: acct.ID,
		Currency: req.Currency, MonthlyCapMillicents: *req.MonthlyCapMillicents})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not store the spend cap"))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant.spend_cap_set", acct.ID, map[string]any{"tenant_id": tenant.ID,
		"currency": stored.Currency, "monthly_cap_millicents": stored.MonthlyCapMillicents})
	if checked, err := s.checkTenant(r.Context(), stored, true); err == nil {
		stored = checked
	}
	httpkit.WriteJSON(w, http.StatusOK, tenantSpendCapResponse(stored))
}

func (s *Service) GetTenantSpendCap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, caps, ok := s.tenantStores(w)
	if !ok {
		return
	}
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	c, err := caps.GetPlatformTenantSpendCap(r.Context(), acct.ID, tenant.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the platform tenant has no spend cap")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the spend cap"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, tenantSpendCapResponse(c))
}

func (s *Service) DeleteTenantSpendCap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, caps, ok := s.tenantStores(w)
	if !ok {
		return
	}
	tenant, ok := s.Host.TenantRoute(w, r, acct)
	if !ok {
		return
	}
	err := caps.DeletePlatformTenantSpendCap(r.Context(), acct.ID, tenant.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the platform tenant has no spend cap")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not remove the spend cap"))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant.spend_cap_removed", acct.ID, map[string]any{"tenant_id": tenant.ID})
	w.WriteHeader(http.StatusNoContent)
}

// recheckTenant checks a tenant's cap after its credit changed.
func (s *Service) recheckTenant(ctx context.Context, accountID, tenantID string) {
	caps, ok := s.Host.Store().(state.PlatformTenantSpendCapStore)
	if !ok {
		return
	}
	if c, err := caps.GetPlatformTenantSpendCap(ctx, accountID, tenantID); err == nil {
		_, _ = s.checkTenant(ctx, c, true)
	}
}

// checkTenantsOnce checks one batch of due tenant spend caps.
func (s *Service) checkTenantsOnce(ctx context.Context) error {
	caps, ok := s.Host.Store().(state.PlatformTenantSpendCapStore)
	if !ok {
		return nil
	}
	due, err := caps.ClaimPlatformTenantSpendCapChecks(ctx, s.now(), api.ConsumerSpendCapCheckLease, api.ConsumerSpendCapBatchSize)
	if err != nil {
		return fmt.Errorf("claim tenant spend cap checks: %w", err)
	}
	var firstErr error
	for _, c := range due {
		if _, err := s.checkTenant(ctx, c, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// checkTenant prices the tenant's month to date, subtracts the credit that
// pays it, and records whether the rest passes the cap. Like a consumer's,
// a quote in another currency never caps, and an unforced check reuses the
// last price while it holds.
func (s *Service) checkTenant(ctx context.Context, c state.PlatformTenantSpendCap, force bool) (state.PlatformTenantSpendCap, error) {
	credits, okCredits := s.Host.Store().(state.PlatformTenantCreditStore)
	caps, okCaps := s.Host.Store().(state.PlatformTenantSpendCapStore)
	if !okCredits || !okCaps {
		return c, errors.New("platform tenant credits are unavailable")
	}
	now := s.now()
	monthStart := billing.MonthStart(now)
	changes, okChanges := s.Host.Store().(state.SpendCapUsageChangeStore)
	if !force && okChanges && lastPriceHolds(c.CheckedAt, c.MonthStart, c.AmountDueMillicents, c.UpdatedAt, monthStart, now,
		func(windowFrom, since time.Time) (bool, error) {
			return changes.PlatformTenantUsageChangedSince(ctx, c.AccountID, c.TenantID, windowFrom, since)
		}) {
		return s.recordTenantCheck(ctx, caps, c, monthStart, *c.AmountDueMillicents, *c.CheckedAt, now)
	}
	currency, usage, err := s.Host.QuoteTenantMonthUsage(ctx, c.AccountID, c.TenantID, now)
	if err != nil {
		return c, fmt.Errorf("quote tenant %s: %w", c.TenantID, err)
	}
	var due int64
	if currency == c.Currency {
		pos, err := credits.PlatformTenantCreditPosition(ctx, c.AccountID, c.TenantID, c.Currency, now)
		if err != nil {
			return c, fmt.Errorf("credit position %s: %w", c.TenantID, err)
		}
		due = max(0, usage-pos.AvailableMillicents-pos.AppliedToUsageInMonthMillicents)
	}
	return s.recordTenantCheck(ctx, caps, c, monthStart, due, now, now)
}

// recordTenantCheck records a tenant's amount due as priced at checkedAt,
// capped against the cap as it is now, and audits a change.
func (s *Service) recordTenantCheck(ctx context.Context, caps state.PlatformTenantSpendCapStore, c state.PlatformTenantSpendCap,
	monthStart time.Time, due int64, checkedAt, now time.Time) (state.PlatformTenantSpendCap, error) {
	capped := due > c.MonthlyCapMillicents
	was, err := caps.RecordPlatformTenantSpendCheck(ctx, c.TenantID, monthStart, due, capped, checkedAt, now.Add(api.ConsumerSpendCapCheckInterval))
	if err != nil {
		return c, fmt.Errorf("record tenant spend check %s: %w", c.TenantID, err)
	}
	if was != capped {
		event := "platform_tenant.spend_cap_reached"
		if !capped {
			event = "platform_tenant.spend_cap_cleared"
		}
		s.Host.Audit(ctx, event, c.AccountID, map[string]any{"tenant_id": c.TenantID, "currency": c.Currency,
			"monthly_cap_millicents": c.MonthlyCapMillicents, "amount_due_millicents": due})
	}
	month, at := monthStart, checkedAt
	c.Capped, c.MonthStart, c.AmountDueMillicents, c.CheckedAt = capped, &month, &due, &at
	return c, nil
}
