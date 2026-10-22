package apidserver

import (
	"context"
	"errors"
	"fmt"
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
		CreditAppliedMillicents: s.CreditAppliedMillicents, AmountDueMillicents: s.AmountDueMillicents(),
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
	for _, fee := range s.Fees {
		out.Fees = append(out.Fees, api.APIConsumerStatementFeeResponse{Kind: fee.Kind, RateCardID: fee.RateCardID, AmountMillicents: fee.AmountMillicents})
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
	statement, created, err := s.draftPlatformTenantStatement(r.Context(), acct, tenant.ID, store, req.PeriodStart.UTC(), req.PeriodEnd.UTC())
	if err != nil {
		api.WriteProblem(w, tenantStatementProblem(err))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, platformTenantStatementResponse(statement))
}

// draftPlatformTenantStatement snapshots the period's tenant-attributed
// usage (ADR-238). Unchanged usage replays the latest revision (created is
// false). The handler and billing runs (ADR-1503) share it.
func (s *server) draftPlatformTenantStatement(ctx context.Context, acct state.Account, tenantID string,
	store state.PlatformTenantStatementStore, start, end time.Time) (state.PlatformTenantStatement, bool, error) {
	plan, err := store.PlanPlatformTenantStatement(ctx, acct.ID, tenantID, start, end)
	if err != nil {
		return state.PlatformTenantStatement{}, false, err
	}
	if len(plan.UsageDelta) == 0 && plan.HasLatest {
		return plan.Latest, false, nil
	}
	revision, priorStatus := 1, state.APIConsumerUsageStatementStatus("")
	if plan.HasLatest {
		revision, priorStatus = plan.Latest.Revision+1, plan.Latest.Status
	}
	input, err := s.quotePlatformTenantStatement(ctx, acct.ID, tenantID, start, end, revision, priorStatus, plan.UsageDelta)
	if err != nil {
		return state.PlatformTenantStatement{}, false, err
	}
	statement, created, err := store.CreatePlatformTenantStatement(ctx, input)
	if err != nil || !created {
		return statement, false, err
	}
	s.audit.Emit(ctx, "platform_tenant_statement.created", &acct.ID, map[string]any{"tenant_id": tenantID, "statement_id": statement.ID, "revision": statement.Revision})
	return statement, true, nil
}

func (s *server) quotePlatformTenantStatement(ctx context.Context, accountID, tenantID string, start, end time.Time,
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
	tenantCards, err := tenantCardsStore.ListPlatformTenantRateCards(ctx, accountID, tenantID)
	if err != nil {
		return state.PlatformTenantStatementInput{}, err
	}
	opts, err := s.tenantQuoteOptions(ctx, accountID, tenantID)
	if err != nil {
		return state.PlatformTenantStatementInput{}, err
	}
	if billing.TenantMonthlyPricing(tenantCards, start, end) {
		return s.quotePlatformTenantMonth(ctx, accountID, tenantID, start, end, revision, priorStatus, usageDelta, tenantCards, opts)
	}
	for _, bucket := range usageDelta {
		if _, loaded := cards[bucket.AppID]; loaded {
			continue
		}
		appCards, err := cardsStore.ListAPIConsumerRateCardsForApp(ctx, accountID, bucket.AppID)
		if err != nil {
			return state.PlatformTenantStatementInput{}, err
		}
		cards[bucket.AppID] = appCards
	}
	return billing.BuildPlatformTenantStatementFromDelta(accountID, tenantID, start, end, time.Now().UTC(), revision,
		priorStatus, usageDelta, cards, tenantCards, opts)
}

// tenantQuoteOptions are how a tenant's usage is priced beyond its cards:
// its trial (ADR-1322).
func (s *server) tenantQuoteOptions(ctx context.Context, accountID, tenantID string) (billing.QuoteOptions, error) {
	trials, ok := s.store.(state.PlatformTenantTrialStore)
	if !ok {
		return billing.QuoteOptions{}, nil
	}
	trial, err := trials.GetPlatformTenantTrial(ctx, accountID, tenantID)
	if errors.Is(err, state.ErrNotFound) {
		return billing.QuoteOptions{}, nil
	}
	if err != nil {
		return billing.QuoteOptions{}, fmt.Errorf("load tenant trial: %w", err)
	}
	return billing.QuoteOptions{TrialStart: trial.StartsAt, TrialEnd: trial.EndsAt}, nil
}

// tenantMeterUsage loads the month's app-reported units for each meter a
// tenant card prices (ADR-1319).
func (s *server) tenantMeterUsage(ctx context.Context, accountID, tenantID string, tenantCards []state.PlatformTenantRateCard,
	start, end time.Time) (map[string][]state.PlatformTenantMeterUsageBucket, error) {
	meters := map[string][]state.PlatformTenantMeterUsageBucket{}
	store, ok := s.store.(state.PlatformTenantMeterUsageStore)
	for _, card := range tenantCards {
		meter, isMeter := state.AppMeterOfUnit(card.Unit)
		if _, loaded := meters[meter]; !isMeter || loaded {
			continue
		}
		if !ok {
			return nil, errors.New("platform tenant meter usage is unavailable")
		}
		rows, err := store.ListPlatformTenantMeterUsage(ctx, accountID, tenantID, meter, start, end)
		if err != nil {
			return nil, fmt.Errorf("list tenant meter usage: %w", err)
		}
		meters[meter] = rows
	}
	return meters, nil
}

// quotePlatformTenantMonth re-prices a whole month for tenant allowances,
// tiers and unit streams (ADR-975, ADR-1319) and bills the difference from
// finalized revisions, plus monthly fees (ADR-1322).
func (s *server) quotePlatformTenantMonth(ctx context.Context, accountID, tenantID string, start, end time.Time,
	revision int, priorStatus state.APIConsumerUsageStatementStatus, usageDelta []state.APIConsumerUsageBucket,
	tenantCards []state.PlatformTenantRateCard, opts billing.QuoteOptions) (state.PlatformTenantStatementInput, error) {
	statements, okStatements := s.store.(state.PlatformTenantStatementStore)
	cardsStore, okCards := s.store.(state.APIConsumerRateCardStore)
	if !okStatements || !okCards {
		return state.PlatformTenantStatementInput{}, state.ErrNotFound
	}
	if !billing.IsCalendarMonth(start, end) {
		return state.PlatformTenantStatementInput{}, billing.ErrTenantMonthPeriodRequired
	}
	monthUsage, err := statements.ListPlatformTenantUsageMinutes(ctx, accountID, tenantID, start, end)
	if err != nil {
		return state.PlatformTenantStatementInput{}, err
	}
	previous, err := statements.ListPlatformTenantStatements(ctx, accountID, tenantID, start, end)
	if err != nil {
		return state.PlatformTenantStatementInput{}, err
	}
	cards := map[string][]state.APIConsumerRateCard{}
	for _, bucket := range monthUsage {
		if _, loaded := cards[bucket.AppID]; loaded {
			continue
		}
		if cards[bucket.AppID], err = cardsStore.ListAPIConsumerRateCardsForApp(ctx, accountID, bucket.AppID); err != nil {
			return state.PlatformTenantStatementInput{}, err
		}
	}
	routes, err := s.tenantRouteUsage(ctx, accountID, tenantID, tenantCards, monthUsage, start, end)
	if err != nil {
		return state.PlatformTenantStatementInput{}, err
	}
	meters, err := s.tenantMeterUsage(ctx, accountID, tenantID, tenantCards, start, end)
	if err != nil {
		return state.PlatformTenantStatementInput{}, err
	}
	tenants, ok := s.store.(state.PlatformTenantStore)
	if !ok {
		return state.PlatformTenantStatementInput{}, state.ErrNotFound
	}
	tenant, err := tenants.GetPlatformTenant(ctx, accountID, tenantID)
	if err != nil {
		return state.PlatformTenantStatementInput{}, err
	}
	month := billing.TenantMonth{AccountID: accountID, TenantID: tenantID, Start: start, End: end, AsOf: time.Now().UTC(),
		Revision: revision, PriorStatus: priorStatus, UsageDelta: usageDelta, MonthUsage: monthUsage, CardsByApp: cards,
		TenantCards: tenantCards, Previous: previous, Inputs: billing.TenantUsageInputs{Routes: routes, Meters: meters},
		TenantCreated: tenant.CreatedAt, Options: opts}
	in, err := billing.BuildTenantMonthStatement(month)
	if !errors.Is(err, billing.ErrTenantChargeDecreased) {
		return in, err
	}
	// Late usage moved a volume month into a cheaper step: keep the step
	// finalized revisions billed (ADR-1312).
	locked := billing.LockedTenantVolumeSteps(tenantCards, previous, start, end)
	if len(locked) == 0 {
		return in, err
	}
	month.Options.VolumeSteps = locked
	return billing.BuildTenantMonthStatement(month)
}

// tenantRouteUsage loads each subject's route counts when a tenant card
// weights routes: consumers' (ADR-1307), and surfaces' and JWT rules'
// (ADR-1310).
func (s *server) tenantRouteUsage(ctx context.Context, accountID, tenantID string, tenantCards []state.PlatformTenantRateCard,
	monthUsage []state.APIConsumerUsageBucket, start, end time.Time) (billing.TenantRouteUsage, error) {
	weighted := false
	for _, card := range tenantCards {
		weighted = weighted || len(card.RouteWeights) > 0
	}
	routeStore, ok := s.store.(state.ConsumerRouteUsageStore)
	if !weighted || !ok {
		return nil, nil
	}
	routes := billing.TenantRouteUsage{}
	sources := false
	for _, bucket := range monthUsage {
		sources = sources || bucket.SurfaceID != "" || bucket.JWTAuthorizationRuleID != ""
		key := billing.TenantRouteKey(bucket.AppID, bucket.ConsumerKey)
		if _, loaded := routes[key]; loaded || bucket.ConsumerKey == "" {
			continue
		}
		rows, err := routeStore.ListAPIConsumerRouteUsage(ctx, accountID, bucket.AppID, bucket.ConsumerKey, start, end)
		if err != nil {
			return nil, fmt.Errorf("list tenant route usage: %w", err)
		}
		routes[key] = rows
	}
	sourceStore, ok := s.store.(state.PlatformTenantSourceRouteUsageStore)
	if !sources || !ok {
		return routes, nil
	}
	rows, err := sourceStore.ListPlatformTenantSourceRouteUsage(ctx, accountID, tenantID, start, end)
	if err != nil {
		return nil, fmt.Errorf("list tenant source route usage: %w", err)
	}
	for _, row := range rows {
		key := billing.TenantSourceRouteKey(row.AppID, row.SourceKind, row.SourceID)
		routes[key] = append(routes[key], state.APIConsumerRouteUsageBucket{WindowStart: row.WindowStart, Route: row.Route, BillableUnits: row.BillableUnits})
	}
	return routes, nil
}

// tenantStatementProblem explains why a tenant statement could not be
// drafted. Billing runs record its detail as the item's reason.
func tenantStatementProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, billing.ErrMixedTenantCurrency):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Mixed statement currencies", "all priced app usage in one tenant statement must use the same currency")
	case errors.Is(err, billing.ErrNoNewTenantUsage):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"No tenant usage", "the requested period has no billable tenant-attributed usage and no monthly fee to bill")
	case errors.Is(err, billing.ErrAPIConsumerAllowanceInTenantStatement):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"App pricing needs an app statement", "an app rate card with included units, tiers, route weights, or a plan cannot price minutes no platform tenant rate card covers; add a tenant rate card in force for those minutes or bill through app consumer statements")
	case errors.Is(err, billing.ErrTenantMonthPeriodRequired):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Calendar month required", "a platform tenant rate card with a monthly allowance, tiers, route weights, monthly fees or a unit other than requests prices this period, so the statement must cover exactly one UTC calendar month")
	case errors.Is(err, billing.ErrTenantMonthOverlap):
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Month already partly billed", "a finalized statement for another period covers minutes of this month; bill this tenant by calendar month once a monthly allowance or tiers apply")
	case errors.Is(err, billing.ErrTenantChargeDecreased):
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Charge would decrease", "re-pricing the month would bill less than its finalized revisions; Gregale never issues credits")
	case errors.Is(err, billing.ErrTenantUsageRegressed):
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage coverage conflict", "current usage is below an earlier immutable statement snapshot")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Statement revision conflict", "statement state changed; reload the period and retry")
	default:
		return api.ErrInternal("could not price platform tenant usage")
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
	statement, err := s.finalizeTenantStatement(r.Context(), acct, tenant.ID, store, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant statement")
		return
	}
	if err != nil {
		api.WriteProblem(w, tenantStatementFinalizeProblem(err))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantStatementResponse(statement))
}

// finalizeTenantStatement freezes a tenant statement and audits the first
// finalization. The handler and billing runs (ADR-1503) share it.
func (s *server) finalizeTenantStatement(ctx context.Context, acct state.Account, tenantID string,
	store state.PlatformTenantStatementStore, statementID string) (state.PlatformTenantStatement, error) {
	statement, changed, err := store.FinalizePlatformTenantStatement(ctx, acct.ID, tenantID, statementID)
	if err == nil && changed {
		s.audit.Emit(ctx, "platform_tenant_statement.finalized", &acct.ID, map[string]any{"tenant_id": tenantID, "statement_id": statement.ID, "revision": statement.Revision})
	}
	return statement, err
}

func tenantStatementFinalizeProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrConflict) {
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Statement cannot be finalized", "all usage must be priced in one currency before finalization")
	}
	return api.ErrInternal("could not finalize platform tenant statement")
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
