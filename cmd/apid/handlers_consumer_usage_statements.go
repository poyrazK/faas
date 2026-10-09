package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func apiConsumerUsageStatementResponse(statement state.APIConsumerUsageStatement) api.APIConsumerUsageStatementResponse {
	out := api.APIConsumerUsageStatementResponse{
		ID: statement.ID, ConsumerID: statement.ConsumerID,
		PeriodStart: statement.PeriodStart.UTC(), PeriodEnd: statement.PeriodEnd.UTC(),
		Revision: statement.Revision, Status: string(statement.Status), Currency: statement.Currency,
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
			PriceMillicentsPerUnit: bucket.PriceMillicentsPerUnit, ChargedUnits: bucket.Charged(), AmountMillicents: bucket.AmountMillicents,
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

// createAPIConsumerUsageStatement plans the period against its existing
// revisions (ADR-843). Unchanged usage replays the latest revision. A changed
// draft is superseded by a new draft, and usage that arrives after
// finalization becomes a new revision holding only the uncovered units.
func (s *server) createAPIConsumerUsageStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, store, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	periodStart, periodEnd, ok := decodeAPIConsumerUsageStatementPeriod(w, r)
	if !ok {
		return
	}
	revisions, err := store.ListAPIConsumerUsageStatementRevisions(r.Context(), acct.ID, app.ID, consumer.ID, periodStart, periodEnd)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load API consumer usage statements"))
		return
	}
	input, unchanged, err := s.planAPIConsumerUsageStatement(r, acct.ID, app.ID, consumer.ID, periodStart, periodEnd, revisions)
	if errors.Is(err, billing.ErrAPIConsumerUsageRegressed) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage coverage conflict", "current usage is below an earlier finalized statement"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not calculate API consumer usage quote"))
		return
	}
	if unchanged {
		writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(revisions[len(revisions)-1]))
		return
	}
	s.persistAPIConsumerUsageStatement(w, r, acct, store, input)
}

func decodeAPIConsumerUsageStatementPeriod(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	var req api.CreateAPIConsumerUsageStatementRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return time.Time{}, time.Time{}, false
	}
	invalid := func(detail string) (time.Time, time.Time, bool) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid statement period", detail))
		return time.Time{}, time.Time{}, false
	}
	if req.PeriodStart == nil || req.PeriodEnd == nil {
		return invalid("period_start and period_end are required")
	}
	periodStart, periodEnd := req.PeriodStart.UTC(), req.PeriodEnd.UTC()
	if !periodStart.Equal(periodStart.Truncate(time.Minute)) || !periodEnd.Equal(periodEnd.Truncate(time.Minute)) {
		return invalid("period_start and period_end must be UTC minutes")
	}
	if !periodEnd.After(periodStart) {
		return invalid("period_end must be after period_start")
	}
	if periodEnd.Sub(periodStart) > time.Duration(usageMaxWindowDays)*24*time.Hour {
		return invalid("statement periods cannot exceed 90 days")
	}
	return periodStart, periodEnd, true
}

// planAPIConsumerUsageStatement prices the usage not yet covered by a
// finalized revision. unchanged is true when there is no such usage and the
// period already has a revision to replay.
func (s *server) planAPIConsumerUsageStatement(r *http.Request, accountID, appID, consumerID string, start, end time.Time,
	revisions []state.APIConsumerUsageStatement) (state.APIConsumerUsageStatementInput, bool, error) {
	usageStore, ok := s.store.(state.ConsumerUsageStore)
	if !ok {
		return state.APIConsumerUsageStatementInput{}, false, errors.New("consumer usage ledger is unavailable")
	}
	cardsStore, ok := s.store.(state.APIConsumerRateCardStore)
	if !ok {
		return state.APIConsumerUsageStatementInput{}, false, errors.New("API consumer pricing is unavailable")
	}
	// Monthly allowances count from the start of start's month (ADR-844).
	usage, err := usageStore.ListAPIConsumerUsage(r.Context(), accountID, appID, consumerID, billing.MonthStart(start), end)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(r.Context(), accountID, appID)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	current, err := billing.QuoteAPIConsumerUsageFrom(cards, usage, start)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	quote, err := billing.APIConsumerStatementDelta(current, revisions)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	if len(quote.Buckets) == 0 && len(revisions) > 0 {
		return state.APIConsumerUsageStatementInput{}, true, nil
	}
	input := state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID,
		PeriodStart: start, PeriodEnd: end, Revision: len(revisions) + 1, Currency: quote.Currency,
		BillableUnits: quote.BillableUnits, UnpricedUnits: quote.UnpricedUnits,
		AmountMillicents: quote.AmountMillicents, Priced: quote.Priced,
		Buckets: make([]state.APIConsumerUsageStatementBucket, 0, len(quote.Buckets)), AsOf: time.Now().UTC(),
	}
	if len(revisions) > 0 {
		input.PriorStatus = revisions[len(revisions)-1].Status
	}
	for _, bucket := range quote.Buckets {
		input.Buckets = append(input.Buckets, statementBucketFromCharge(bucket))
	}
	return input, false, nil
}

// statementBucketFromCharge persists a priced minute; charged_units is
// recorded only for priced buckets, where it determines the amount.
func statementBucketFromCharge(bucket billing.APIConsumerUsageChargeBucket) state.APIConsumerUsageStatementBucket {
	out := state.APIConsumerUsageStatementBucket{
		WindowStart: bucket.WindowStart, BillableUnits: bucket.BillableUnits,
		RateCardID: bucket.RateCardID, Currency: bucket.Currency,
		PriceMillicentsPerUnit: bucket.PriceMillicentsPerUnit, AmountMillicents: bucket.AmountMillicents,
	}
	if bucket.RateCardID != "" {
		charged := bucket.ChargedUnits
		out.ChargedUnits = &charged
	}
	return out
}

func (s *server) persistAPIConsumerUsageStatement(w http.ResponseWriter, r *http.Request, acct state.Account,
	store state.APIConsumerUsageStatementStore, input state.APIConsumerUsageStatementInput) {
	statement, created, err := store.CreateAPIConsumerUsageStatement(r.Context(), input)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Statement revision conflict", "statement state changed; reload the period and retry"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create API consumer usage statement"))
		return
	}
	if !created {
		writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(statement))
		return
	}
	s.audit.Emit(r.Context(), "api_consumer_usage_statement.created", &acct.ID, map[string]any{
		"app_id": input.AppID, "consumer_id": input.ConsumerID, "statement_id": statement.ID,
		"revision":     statement.Revision,
		"period_start": input.PeriodStart.Format(time.RFC3339), "period_end": input.PeriodEnd.Format(time.RFC3339),
		"amount_millicents": statement.AmountMillicents, "unpriced_units": statement.UnpricedUnits,
	})
	writeJSON(w, http.StatusCreated, apiConsumerUsageStatementResponse(statement))
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
		if outbox, ok := s.store.(state.AppWebhookEventOutboxStore); ok {
			if _, err := outbox.RelayAppWebhookEventOutboxSource(r.Context(), state.AppWebhookEventUsageStatementFinalized, statement.ID); err != nil {
				s.log.WarnContext(r.Context(), "relay usage statement finalized webhook",
					"app_id", app.ID, "statement_id", statement.ID, "err", err.Error())
			}
		}
	}
	writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(statement))
}

func apiConsumerUsageStatementHandoffResponse(handoff state.APIConsumerUsageStatementHandoff) api.APIConsumerUsageStatementHandoffResponse {
	return api.APIConsumerUsageStatementHandoffResponse{
		ID: handoff.ID, StatementID: handoff.StatementID,
		ExternalInvoiceID: handoff.ExternalInvoiceID, Currency: handoff.Currency,
		AmountMillicents: handoff.AmountMillicents, CreatedAt: handoff.CreatedAt.UTC(),
	}
}

// claimAPIConsumerUsageStatement records the customer's external invoice
// reference for a finalized statement. The operation is provider-neutral:
// Gregale snapshots the amount and currency but never charges the customer.
func (s *server) claimAPIConsumerUsageStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, statements, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	handoffs, ok := s.store.(state.APIConsumerUsageStatementHandoffStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("API consumer usage statement handoffs are unavailable"))
		return
	}
	statement, err := statements.GetAPIConsumerUsageStatement(r.Context(), acct.ID, app.ID, consumer.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such usage statement")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load API consumer usage statement"))
		return
	}
	if statement.Status != state.APIConsumerUsageStatementFinalized {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage statement cannot be handed off", "only finalized usage statements can be handed off to a customer billing system"))
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
	handoff, created, err := handoffs.CreateAPIConsumerUsageStatementHandoff(r.Context(), state.APIConsumerUsageStatementHandoffInput{
		AccountID: acct.ID, AppID: app.ID, ConsumerID: consumer.ID,
		StatementID: statement.ID, ExternalInvoiceID: req.ExternalInvoiceID,
	})
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such usage statement")
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage statement handoff conflict", "the statement was already handed off or the external invoice ID is already in use"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not record API consumer usage statement handoff"))
		return
	}
	if created {
		s.audit.Emit(r.Context(), "api_consumer_usage_statement.handed_off", &acct.ID, map[string]any{
			"app_id": app.ID, "consumer_id": consumer.ID, "statement_id": statement.ID,
			"handoff_id": handoff.ID, "external_invoice_id": handoff.ExternalInvoiceID,
		})
		writeJSON(w, http.StatusCreated, apiConsumerUsageStatementHandoffResponse(handoff))
		return
	}
	writeJSON(w, http.StatusOK, apiConsumerUsageStatementHandoffResponse(handoff))
}

func (s *server) getAPIConsumerUsageStatementHandoff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, _, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	handoffs, ok := s.store.(state.APIConsumerUsageStatementHandoffStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("API consumer usage statement handoffs are unavailable"))
		return
	}
	handoff, err := handoffs.GetAPIConsumerUsageStatementHandoff(r.Context(), acct.ID, app.ID, consumer.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no usage statement handoff")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load API consumer usage statement handoff"))
		return
	}
	writeJSON(w, http.StatusOK, apiConsumerUsageStatementHandoffResponse(handoff))
}
