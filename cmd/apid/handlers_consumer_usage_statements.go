package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func apiConsumerUsageStatementResponse(statement state.APIConsumerUsageStatement) api.APIConsumerUsageStatementResponse {
	out := api.APIConsumerUsageStatementResponse{
		ID: statement.ID, ConsumerID: statement.ConsumerID,
		PeriodStart: statement.PeriodStart.UTC(), PeriodEnd: statement.PeriodEnd.UTC(),
		Status: string(statement.Status), Currency: statement.Currency,
		BillableUnits: statement.BillableUnits, UnpricedUnits: statement.UnpricedUnits,
		AmountMillicents: statement.AmountMillicents, Priced: statement.Priced,
		AsOf: statement.AsOf.UTC().Format(time.RFC3339Nano), CreatedAt: statement.CreatedAt.UTC(),
		FinalizedAt: statement.FinalizedAt,
		Buckets:     make([]api.APIConsumerUsageStatementBucketResponse, 0, len(statement.Buckets)),
	}
	for _, bucket := range statement.Buckets {
		out.Buckets = append(out.Buckets, api.APIConsumerUsageStatementBucketResponse{
			WindowStart: bucket.WindowStart.UTC(), BillableUnits: bucket.BillableUnits,
			RateCardID: bucket.RateCardID, Currency: bucket.Currency,
			PriceMillicentsPerUnit: bucket.PriceMillicentsPerUnit, AmountMillicents: bucket.AmountMillicents,
		})
	}
	return out
}

func (s *server) apiConsumerUsageStatementStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumer, state.APIConsumerUsageStatementStore, bool) {
	if !s.consumerFeatureAllowed(w, acct) {
		return state.App{}, state.APIConsumer{}, nil, false
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return state.App{}, state.APIConsumer{}, nil, false
	}
	consumer, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || consumer.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return state.App{}, state.APIConsumer{}, nil, false
	}
	store, ok := s.store.(state.APIConsumerUsageStatementStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("API consumer usage statements are unavailable"))
		return state.App{}, state.APIConsumer{}, nil, false
	}
	return app, consumer, store, true
}

func (s *server) listAPIConsumerUsageStatements(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, store, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	statements, err := store.ListAPIConsumerUsageStatements(r.Context(), acct.ID, app.ID, consumer.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list API consumer usage statements"))
		return
	}
	out := api.APIConsumerUsageStatementListResponse{Statements: make([]api.APIConsumerUsageStatementResponse, 0, len(statements))}
	for _, statement := range statements {
		out.Statements = append(out.Statements, apiConsumerUsageStatementResponse(statement))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createAPIConsumerUsageStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, store, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAPIConsumerUsageStatementRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.PeriodStart == nil || req.PeriodEnd == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid statement period", "period_start and period_end are required"))
		return
	}
	periodStart, periodEnd := req.PeriodStart.UTC(), req.PeriodEnd.UTC()
	if !periodStart.Equal(periodStart.Truncate(time.Minute)) || !periodEnd.Equal(periodEnd.Truncate(time.Minute)) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid statement period", "period_start and period_end must be UTC minutes"))
		return
	}
	if !periodEnd.After(periodStart) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid statement period", "period_end must be after period_start"))
		return
	}
	if periodEnd.Sub(periodStart) > time.Duration(usageMaxWindowDays)*24*time.Hour {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid statement period", "statement periods cannot exceed 90 days"))
		return
	}
	usageStore, ok := s.store.(state.ConsumerUsageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer usage ledger is unavailable"))
		return
	}
	cardsStore, ok := s.store.(state.APIConsumerRateCardStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("API consumer pricing is unavailable"))
		return
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load API consumer rate cards"))
		return
	}
	usage, err := usageStore.ListAPIConsumerUsage(r.Context(), acct.ID, app.ID, consumer.ID, periodStart, periodEnd)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load API consumer usage"))
		return
	}
	quote, err := billing.QuoteAPIConsumerUsage(cards, usage)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not calculate API consumer usage quote"))
		return
	}
	buckets := make([]state.APIConsumerUsageStatementBucket, 0, len(quote.Buckets))
	for _, bucket := range quote.Buckets {
		buckets = append(buckets, state.APIConsumerUsageStatementBucket{
			WindowStart: bucket.WindowStart, BillableUnits: bucket.BillableUnits,
			RateCardID: bucket.RateCardID, Currency: bucket.Currency,
			PriceMillicentsPerUnit: bucket.PriceMillicentsPerUnit, AmountMillicents: bucket.AmountMillicents,
		})
	}
	statement, created, err := store.CreateAPIConsumerUsageStatement(r.Context(), state.APIConsumerUsageStatementInput{
		AccountID: acct.ID, AppID: app.ID, ConsumerID: consumer.ID,
		PeriodStart: periodStart, PeriodEnd: periodEnd, Currency: quote.Currency,
		BillableUnits: quote.BillableUnits, UnpricedUnits: quote.UnpricedUnits,
		AmountMillicents: quote.AmountMillicents, Priced: quote.Priced, Buckets: buckets,
		AsOf: time.Now().UTC(),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create API consumer usage statement"))
		return
	}
	if created {
		s.audit.Emit(r.Context(), "api_consumer_usage_statement.created", &acct.ID, map[string]any{
			"app_id": app.ID, "consumer_id": consumer.ID, "statement_id": statement.ID,
			"period_start": periodStart.Format(time.RFC3339), "period_end": periodEnd.Format(time.RFC3339),
			"amount_millicents": statement.AmountMillicents, "unpriced_units": statement.UnpricedUnits,
		})
		writeJSON(w, http.StatusCreated, apiConsumerUsageStatementResponse(statement))
		return
	}
	writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(statement))
}

func (s *server) getAPIConsumerUsageStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, store, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	statement, err := store.GetAPIConsumerUsageStatement(r.Context(), acct.ID, app.ID, consumer.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such usage statement")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load API consumer usage statement"))
		return
	}
	writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(statement))
}

func (s *server) finalizeAPIConsumerUsageStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, store, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	statement, changed, err := store.FinalizeAPIConsumerUsageStatement(r.Context(), acct.ID, app.ID, consumer.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such usage statement")
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage statement cannot be finalized", "all usage must have an effective rate card before finalization"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not finalize API consumer usage statement"))
		return
	}
	if changed {
		s.audit.Emit(r.Context(), "api_consumer_usage_statement.finalized", &acct.ID, map[string]any{
			"app_id": app.ID, "consumer_id": consumer.ID, "statement_id": statement.ID,
			"finalized_at": statement.FinalizedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(statement))
}
