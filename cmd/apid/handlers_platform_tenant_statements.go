package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func platformTenantStatementResponse(s state.PlatformTenantStatement) api.PlatformTenantStatementResponse {
	out := api.PlatformTenantStatementResponse{ID: s.ID, TenantID: s.TenantID, PeriodStart: s.PeriodStart,
		PeriodEnd: s.PeriodEnd, Revision: s.Revision, Status: string(s.Status), Currency: s.Currency,
		BillableUnits: s.BillableUnits, UnpricedUnits: s.UnpricedUnits, AmountMillicents: s.AmountMillicents,
		AsOf: s.AsOf, CreatedAt: s.CreatedAt, FinalizedAt: s.FinalizedAt,
		Lines: make([]api.PlatformTenantStatementLineResponse, 0, len(s.Lines))}
	for _, line := range s.Lines {
		out.Lines = append(out.Lines, api.PlatformTenantStatementLineResponse{
			AppID: line.AppID, ConsumerID: line.ConsumerID, SurfaceID: line.SurfaceID, JWTAuthorizationRuleID: line.JWTAuthorizationRuleID, WindowStart: line.WindowStart, WindowEnd: line.WindowEnd,
			BillableUnits: line.BillableUnits, RateCardID: line.RateCardID,
			PlatformTenantRateCardID: line.PlatformTenantRateCardID, Currency: line.Currency,
			PriceMillicentsPerUnit: line.PriceMillicentsPerUnit, AmountMillicents: line.AmountMillicents,
		})
	}
	return out
}

func platformTenantHandoffResponse(h state.PlatformTenantStatementHandoff) api.PlatformTenantStatementHandoffResponse {
	return api.PlatformTenantStatementHandoffResponse{ID: h.ID, StatementID: h.StatementID,
		ExternalInvoiceID: h.ExternalInvoiceID, Currency: h.Currency,
		AmountMillicents: h.AmountMillicents, CreatedAt: h.CreatedAt}
}

func (s *server) tenantStatementStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantStatementStore, bool) {
	if statementID := r.PathValue("statement_id"); statementID != "" {
		if _, err := uuid.Parse(statementID); err != nil {
			s.notFound(w, "no such platform tenant statement")
			return state.PlatformTenant{}, nil, false
		}
	}
	tenants, ok := s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenants)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	store, ok := s.store.(state.PlatformTenantStatementStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant statements are unavailable"))
	}
	return tenant, store, ok
}

func validateTenantStatementPeriod(start, end time.Time) bool {
	return !start.IsZero() && !end.IsZero() && start.Equal(start.UTC().Truncate(time.Minute)) &&
		end.Equal(end.UTC().Truncate(time.Minute)) && end.After(start) &&
		end.Sub(start) <= time.Duration(usageMaxWindowDays)*24*time.Hour
}

func tenantStatementPeriodProblem(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
		"Invalid statement period", "period_start and period_end must be UTC minutes, ordered, and at most 90 days apart"))
}

func (s *server) createPlatformTenantStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.tenantStatementStore(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAPIConsumerUsageStatementRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.PeriodStart == nil || req.PeriodEnd == nil || !validateTenantStatementPeriod(*req.PeriodStart, *req.PeriodEnd) {
		tenantStatementPeriodProblem(w)
		return
	}
	start, end := req.PeriodStart.UTC(), req.PeriodEnd.UTC()
	plan, err := store.PlanPlatformTenantStatement(r.Context(), acct.ID, tenant.ID, start, end)
	if err != nil {
		writeTenantStatementQuoteError(w, err)
		return
	}
	if len(plan.UsageDelta) == 0 && plan.HasLatest {
		writeJSON(w, http.StatusOK, platformTenantStatementResponse(plan.Latest))
		return
	}
	revision, priorStatus := 1, state.APIConsumerUsageStatementStatus("")
	if plan.HasLatest {
		revision, priorStatus = plan.Latest.Revision+1, plan.Latest.Status
	}
	input, err := s.quotePlatformTenantStatement(r, acct.ID, tenant.ID, start, end, revision, priorStatus, plan.UsageDelta)
	if err != nil {
		writeTenantStatementQuoteError(w, err)
		return
	}
	s.persistPlatformTenantStatement(w, r, acct, tenant, store, input)
}

func (s *server) persistPlatformTenantStatement(w http.ResponseWriter, r *http.Request, acct state.Account,
	tenant state.PlatformTenant, store state.PlatformTenantStatementStore, input state.PlatformTenantStatementInput) {
	statement, created, err := store.CreatePlatformTenantStatement(r.Context(), input)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Statement revision conflict", "statement state changed; reload the period and retry"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create platform tenant statement"))
		return
	}
	if created {
		s.audit.Emit(r.Context(), "platform_tenant_statement.created", &acct.ID, map[string]any{"tenant_id": tenant.ID, "statement_id": statement.ID, "revision": statement.Revision})
		writeJSON(w, http.StatusCreated, platformTenantStatementResponse(statement))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantStatementResponse(statement))
}

func (s *server) quotePlatformTenantStatement(r *http.Request, accountID, tenantID string, start, end time.Time,
	revision int, priorStatus state.APIConsumerUsageStatementStatus,
	usageDelta []state.APIConsumerUsageBucket) (state.PlatformTenantStatementInput, error) {
	cardsStore, ok := s.store.(state.APIConsumerRateCardStore)
	if !ok {
		return state.PlatformTenantStatementInput{}, state.ErrNotFound
	}
	cards := map[string][]state.APIConsumerRateCard{}
	tenantCardsStore, ok := s.store.(state.PlatformTenantRateCardStore)
	if !ok {
		return state.PlatformTenantStatementInput{}, state.ErrNotFound
	}
	tenantCards, err := tenantCardsStore.ListPlatformTenantRateCards(r.Context(), accountID, tenantID)
	if err != nil {
		return state.PlatformTenantStatementInput{}, err
	}
	for _, bucket := range usageDelta {
		if _, loaded := cards[bucket.AppID]; loaded {
			continue
		}
		appCards, err := cardsStore.ListAPIConsumerRateCardsForApp(r.Context(), accountID, bucket.AppID)
		if err != nil {
			return state.PlatformTenantStatementInput{}, err
		}
		cards[bucket.AppID] = appCards
	}
	return billing.BuildPlatformTenantStatementFromDelta(accountID, tenantID, start, end, time.Now().UTC(), revision,
		priorStatus, usageDelta, cards, tenantCards)
}

func writeTenantStatementQuoteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrMixedTenantCurrency):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Mixed statement currencies", "all priced app usage in one tenant statement must use the same currency"))
	case errors.Is(err, billing.ErrNoNewTenantUsage):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"No tenant usage", "the requested period has no billable tenant-attributed usage"))
	case errors.Is(err, billing.ErrAPIConsumerAllowanceInTenantStatement):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Included units need an app statement", "an app rate card with included_units_per_month cannot price a cross-app statement; add a platform tenant rate card or bill through app consumer statements"))
	case errors.Is(err, billing.ErrTenantUsageRegressed):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage coverage conflict", "current usage is below an earlier immutable statement snapshot"))
	default:
		api.WriteProblem(w, api.ErrInternal("could not price platform tenant usage"))
	}
}

func (s *server) listPlatformTenantStatements(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.tenantStatementStore(w, r, acct)
	if !ok {
		return
	}
	start, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("period_start"))
	end, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("period_end"))
	if err1 != nil || err2 != nil || !validateTenantStatementPeriod(start, end) {
		tenantStatementPeriodProblem(w)
		return
	}
	statements, err := store.ListPlatformTenantStatementHeaders(r.Context(), acct.ID, tenant.ID, start.UTC(), end.UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant statements"))
		return
	}
	out := api.PlatformTenantStatementListResponse{Statements: make([]api.PlatformTenantStatementResponse, 0, len(statements))}
	for _, statement := range statements {
		out.Statements = append(out.Statements, platformTenantStatementResponse(statement))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getPlatformTenantStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.tenantStatementStore(w, r, acct)
	if !ok {
		return
	}
	statement, err := store.GetPlatformTenantStatementHeader(r.Context(), acct.ID, tenant.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant statement")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant statement"))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantStatementResponse(statement))
}

func (s *server) finalizePlatformTenantStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.tenantStatementStore(w, r, acct)
	if !ok {
		return
	}
	statement, changed, err := store.FinalizePlatformTenantStatement(r.Context(), acct.ID, tenant.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant statement")
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Statement cannot be finalized", "all usage must be priced in one currency before finalization"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not finalize platform tenant statement"))
		return
	}
	if changed {
		s.audit.Emit(r.Context(), "platform_tenant_statement.finalized", &acct.ID, map[string]any{"tenant_id": tenant.ID, "statement_id": statement.ID, "revision": statement.Revision})
	}
	writeJSON(w, http.StatusOK, platformTenantStatementResponse(statement))
}

func (s *server) claimPlatformTenantStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.tenantStatementStore(w, r, acct)
	if !ok {
		return
	}
	var req api.ClaimAPIConsumerUsageStatementRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req.ExternalInvoiceID = strings.TrimSpace(req.ExternalInvoiceID)
	if req.ExternalInvoiceID == "" || len(req.ExternalInvoiceID) > 255 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid external invoice ID", "external_invoice_id is required and must be at most 255 bytes"))
		return
	}
	handoff, created, err := store.CreatePlatformTenantStatementHandoff(r.Context(), state.PlatformTenantStatementHandoffInput{
		AccountID: acct.ID, TenantID: tenant.ID, StatementID: r.PathValue("statement_id"), ExternalInvoiceID: req.ExternalInvoiceID})
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant statement")
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Statement handoff conflict", "statement must be finalized and cannot overlap usage already handed off through another statement"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not hand off platform tenant statement"))
		return
	}
	if created {
		s.audit.Emit(r.Context(), "platform_tenant_statement.handed_off", &acct.ID, map[string]any{"tenant_id": tenant.ID, "statement_id": handoff.StatementID, "external_invoice_id": handoff.ExternalInvoiceID})
		writeJSON(w, http.StatusCreated, platformTenantHandoffResponse(handoff))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantHandoffResponse(handoff))
}

func (s *server) getPlatformTenantStatementHandoff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.tenantStatementStore(w, r, acct)
	if !ok {
		return
	}
	handoff, err := store.GetPlatformTenantStatementHandoff(r.Context(), acct.ID, tenant.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no platform tenant statement handoff")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant statement handoff"))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantHandoffResponse(handoff))
}
