// Package consumercredits grants API consumers prepaid credit and caps
// their monthly spend (ADR-1306). Finalized statements draw credit down in
// the state store; this package serves the grants and caps, and its worker
// flags consumers whose month to date passed their cap so the gateway
// refuses them. Like the other apid domains (ADR-982), it reaches apid only
// through Host; apidserver keeps the route table.
package consumercredits

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what credits and spend caps need from apid.
type Host interface {
	Store() state.Store
	// ConsumerRoute resolves the app and consumer of a consumer route; on
	// false it has written the problem.
	ConsumerRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumer, bool)
	// QuoteConsumerUsage prices usage in [since, until) like a statement.
	QuoteConsumerUsage(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) (billing.APIConsumerUsageQuote, error)
	// TenantRoute resolves the platform tenant of a tenant route; on false
	// it has written the problem.
	TenantRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, bool)
	// QuoteTenantMonthUsage prices a tenant's usage for the month to date,
	// fees excluded (ADR-20261019100331).
	QuoteTenantMonthUsage(ctx context.Context, accountID, tenantID string, now time.Time) (currency string, usageMillicents int64, err error)
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
}

// Service serves credits and spend caps and runs the spend check worker.
// Now is the clock; nil uses time.Now.
type Service struct {
	Host Host
	Now  func() time.Time
	// Enabled pauses the background worker while it reports false: API
	// monetization is released by a runtime setting (ADR-20261018090210).
	// Nil runs it.
	Enabled func() bool
}

// tick runs one background tick unless Enabled pauses the worker; paused,
// spend caps are not rechecked.
func (s *Service) tick(ctx context.Context) error {
	if s.Enabled != nil && !s.Enabled() {
		return nil
	}
	return s.CheckOnce(ctx)
}

var currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) stores(w http.ResponseWriter) (state.APIConsumerCreditStore, state.APIConsumerSpendCapStore, bool) {
	credits, okCredits := s.Host.Store().(state.APIConsumerCreditStore)
	caps, okCaps := s.Host.Store().(state.APIConsumerSpendCapStore)
	if !okCredits || !okCaps {
		api.WriteProblem(w, api.ErrInternal("consumer credits are unavailable"))
		return nil, nil, false
	}
	return credits, caps, true
}

func validation(title, detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, title, detail)
}

func creditResponse(g state.APIConsumerCreditGrant, now time.Time) api.APIConsumerCreditResponse {
	status := api.ConsumerCreditActive
	switch {
	case g.VoidedAt != nil:
		status = api.ConsumerCreditVoided
	case g.RemainingMillicents == 0:
		status = api.ConsumerCreditUsed
	case g.ExpiresAt != nil && !g.ExpiresAt.After(now):
		status = api.ConsumerCreditExpired
	}
	return api.APIConsumerCreditResponse{ID: g.ID, ConsumerID: g.ConsumerID, Currency: g.Currency, AmountMillicents: g.AmountMillicents,
		RemainingMillicents: g.RemainingMillicents, Status: status, Note: g.Note, ExpiresAt: g.ExpiresAt, VoidedAt: g.VoidedAt,
		CreatedAt: g.CreatedAt}
}

func validateCreditRequest(req api.CreateAPIConsumerCreditRequest, now time.Time) *api.Problem {
	switch {
	case !currencyRE.MatchString(req.Currency):
		return validation("Invalid currency", "currency must be an ISO-4217 code such as EUR")
	case req.AmountMillicents <= 0 || req.AmountMillicents > api.ConsumerCreditGrantMaxMillicents:
		return validation("Invalid amount", fmt.Sprintf("amount_millicents must be 1 to %d", api.ConsumerCreditGrantMaxMillicents)).
			WithLimit(api.ConsumerCreditGrantMaxMillicents, req.AmountMillicents)
	case len(req.Note) > api.ConsumerCreditNoteMaxBytes:
		return validation("Note too long", fmt.Sprintf("note is at most %d bytes", api.ConsumerCreditNoteMaxBytes)).
			WithLimit(int64(api.ConsumerCreditNoteMaxBytes), int64(len(req.Note)))
	case req.ExpiresAt != nil && !req.ExpiresAt.After(now):
		return validation("Invalid expiry", "expires_at must be in the future")
	}
	return nil
}

// CreateCredit grants a consumer prepaid credit and rechecks its spend cap.
func (s *Service) CreateCredit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	credits, _, ok := s.stores(w)
	if !ok {
		return
	}
	app, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
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
	grant, err := credits.CreateAPIConsumerCreditGrant(r.Context(), state.APIConsumerCreditGrant{AccountID: acct.ID, AppID: app.ID,
		ConsumerID: consumer.ID, Currency: req.Currency, AmountMillicents: req.AmountMillicents, Note: req.Note, ExpiresAt: req.ExpiresAt})
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Too many open credits",
			fmt.Sprintf("a consumer holds at most %d grants with credit left; void one first", api.ConsumerCreditOpenGrantsMax)).
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
	s.Host.Audit(r.Context(), "api_consumer_credit.granted", acct.ID, map[string]any{"app_id": app.ID, "consumer_id": consumer.ID,
		"credit_id": grant.ID, "currency": grant.Currency, "amount_millicents": grant.AmountMillicents})
	s.recheck(r.Context(), acct.ID, consumer.ID)
	httpkit.WriteJSON(w, http.StatusCreated, creditResponse(grant, now))
}

// ListCredits lists a consumer's grants with its usable balance per
// currency.
func (s *Service) ListCredits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	credits, _, ok := s.stores(w)
	if !ok {
		return
	}
	app, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
	if !ok {
		return
	}
	grants, err := credits.ListAPIConsumerCreditGrants(r.Context(), acct.ID, app.ID, consumer.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list credits"))
		return
	}
	now := s.now()
	httpkit.WriteJSON(w, http.StatusOK, api.APIConsumerCreditListResponse{Credits: creditResponses(grants, now), Balances: Balances(grants, now)})
}

func creditResponses(grants []state.APIConsumerCreditGrant, now time.Time) []api.APIConsumerCreditResponse {
	out := make([]api.APIConsumerCreditResponse, 0, len(grants))
	for _, g := range grants {
		out = append(out, creditResponse(g, now))
	}
	return out
}

// Balances sums the credit each currency can still pay at now.
func Balances(grants []state.APIConsumerCreditGrant, now time.Time) []api.APIConsumerCreditBalance {
	sums := map[string]int64{}
	for _, g := range grants {
		if g.Usable(now) {
			sums[g.Currency] += g.RemainingMillicents
		}
	}
	out := make([]api.APIConsumerCreditBalance, 0, len(sums))
	for currency, available := range sums {
		out = append(out, api.APIConsumerCreditBalance{Currency: currency, AvailableMillicents: available})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Currency < out[j].Currency })
	return out
}

// VoidCredit voids what is left of a grant.
func (s *Service) VoidCredit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	credits, _, ok := s.stores(w)
	if !ok {
		return
	}
	app, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
	if !ok {
		return
	}
	grant, err := credits.VoidAPIConsumerCreditGrant(r.Context(), acct.ID, app.ID, consumer.ID, r.PathValue("credit_id"))
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such credit")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not void the credit"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer_credit.voided", acct.ID, map[string]any{"app_id": app.ID, "consumer_id": consumer.ID,
		"credit_id": grant.ID})
	s.recheck(r.Context(), acct.ID, consumer.ID)
	httpkit.WriteJSON(w, http.StatusOK, creditResponse(grant, s.now()))
}

func spendCapResponse(c state.APIConsumerSpendCap) api.APIConsumerSpendCapResponse {
	out := api.APIConsumerSpendCapResponse{ConsumerID: c.ConsumerID, Currency: c.Currency, MonthlyCapMillicents: c.MonthlyCapMillicents,
		AmountDueMillicents: c.AmountDueMillicents, CheckedAt: c.CheckedAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if c.MonthStart != nil {
		out.Month = c.MonthStart.Format("2006-01")
		out.Capped = c.CappedIn(*c.MonthStart)
	}
	return out
}

func validateSpendCapRequest(req api.PutAPIConsumerSpendCapRequest) *api.Problem {
	if !currencyRE.MatchString(req.Currency) {
		return validation("Invalid currency", "currency must be an ISO-4217 code such as EUR")
	}
	if req.MonthlyCapMillicents == nil || *req.MonthlyCapMillicents < 0 || *req.MonthlyCapMillicents > api.ConsumerSpendCapMaxMillicents {
		return validation("Invalid cap", fmt.Sprintf("monthly_cap_millicents must be 0 to %d", api.ConsumerSpendCapMaxMillicents))
	}
	return nil
}

// PutSpendCap sets a consumer's monthly spend cap and checks it at once.
func (s *Service) PutSpendCap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, caps, ok := s.stores(w)
	if !ok {
		return
	}
	app, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
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
	stored, err := caps.PutAPIConsumerSpendCap(r.Context(), state.APIConsumerSpendCap{ConsumerID: consumer.ID, AccountID: acct.ID,
		AppID: app.ID, Currency: req.Currency, MonthlyCapMillicents: *req.MonthlyCapMillicents})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not store the spend cap"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer.spend_cap_set", acct.ID, map[string]any{"app_id": app.ID, "consumer_id": consumer.ID,
		"currency": stored.Currency, "monthly_cap_millicents": stored.MonthlyCapMillicents})
	if checked, err := s.check(r.Context(), stored); err == nil {
		stored = checked
	}
	httpkit.WriteJSON(w, http.StatusOK, spendCapResponse(stored))
}

func (s *Service) GetSpendCap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, caps, ok := s.stores(w)
	if !ok {
		return
	}
	_, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
	if !ok {
		return
	}
	c, err := caps.GetAPIConsumerSpendCap(r.Context(), acct.ID, consumer.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the consumer has no spend cap")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the spend cap"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, spendCapResponse(c))
}

func (s *Service) DeleteSpendCap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, caps, ok := s.stores(w)
	if !ok {
		return
	}
	app, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
	if !ok {
		return
	}
	err := caps.DeleteAPIConsumerSpendCap(r.Context(), acct.ID, consumer.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the consumer has no spend cap")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not remove the spend cap"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer.spend_cap_removed", acct.ID, map[string]any{"app_id": app.ID, "consumer_id": consumer.ID})
	w.WriteHeader(http.StatusNoContent)
}

// recheck checks a consumer's cap after its credit changed, so new credit
// lifts a cap without waiting for the worker. Failures leave the worker to
// catch up.
func (s *Service) recheck(ctx context.Context, accountID, consumerID string) {
	caps, ok := s.Host.Store().(state.APIConsumerSpendCapStore)
	if !ok {
		return
	}
	if c, err := caps.GetAPIConsumerSpendCap(ctx, accountID, consumerID); err == nil {
		_, _ = s.check(ctx, c)
	}
}

// Start runs the spend check worker until ctx ends.
func (s *Service) Start(ctx context.Context, log *slog.Logger) {
	if _, ok := s.Host.Store().(state.APIConsumerSpendCapStore); !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.ConsumerSpendCapCheckInterval)
		defer ticker.Stop()
		for {
			if err := s.tick(ctx); err != nil && ctx.Err() == nil {
				log.Warn("consumer spend cap check failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// CheckOnce checks one batch of due spend caps. A cap whose check fails is
// retried when its lease ends.
func (s *Service) CheckOnce(ctx context.Context) error {
	caps, ok := s.Host.Store().(state.APIConsumerSpendCapStore)
	if !ok {
		return nil
	}
	due, err := caps.ClaimAPIConsumerSpendCapChecks(ctx, s.now(), api.ConsumerSpendCapCheckLease, api.ConsumerSpendCapBatchSize)
	if err != nil {
		return fmt.Errorf("claim spend cap checks: %w", err)
	}
	var firstErr error
	for _, c := range due {
		if _, err := s.check(ctx, c); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := s.checkTenantsOnce(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// check prices the consumer's month to date, subtracts the credit that
// pays it, and records whether the rest passes the cap. A quote in another
// currency cannot be compared and never caps.
func (s *Service) check(ctx context.Context, c state.APIConsumerSpendCap) (state.APIConsumerSpendCap, error) {
	credits, okCredits := s.Host.Store().(state.APIConsumerCreditStore)
	caps, okCaps := s.Host.Store().(state.APIConsumerSpendCapStore)
	if !okCredits || !okCaps {
		return c, errors.New("consumer credits are unavailable")
	}
	now := s.now()
	monthStart := billing.MonthStart(now)
	quote, err := s.Host.QuoteConsumerUsage(ctx, c.AccountID, c.AppID, c.ConsumerID, monthStart, now.Truncate(time.Minute).Add(time.Minute))
	if err != nil {
		return c, fmt.Errorf("quote consumer %s: %w", c.ConsumerID, err)
	}
	var due int64
	if quote.Currency == c.Currency {
		pos, err := credits.APIConsumerCreditPosition(ctx, c.AccountID, c.AppID, c.ConsumerID, c.Currency, now)
		if err != nil {
			return c, fmt.Errorf("credit position %s: %w", c.ConsumerID, err)
		}
		due = max(0, quote.AmountMillicents-pos.AvailableMillicents-pos.AppliedInMonthMillicents)
	}
	capped := due > c.MonthlyCapMillicents
	was, err := caps.RecordAPIConsumerSpendCheck(ctx, c.ConsumerID, monthStart, due, capped, now, now.Add(api.ConsumerSpendCapCheckInterval))
	if err != nil {
		return c, fmt.Errorf("record spend check %s: %w", c.ConsumerID, err)
	}
	if was != capped {
		event := "api_consumer.spend_cap_reached"
		if !capped {
			event = "api_consumer.spend_cap_cleared"
		}
		s.Host.Audit(ctx, event, c.AccountID, map[string]any{"app_id": c.AppID, "consumer_id": c.ConsumerID,
			"currency": c.Currency, "monthly_cap_millicents": c.MonthlyCapMillicents, "amount_due_millicents": due})
	}
	month, at := monthStart, now
	c.Capped, c.MonthStart, c.AmountDueMillicents, c.CheckedAt = capped, &month, &due, &at
	return c, nil
}
