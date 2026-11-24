package apidserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
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
		FinalizedAt:             statement.FinalizedAt,
		Buckets:                 make([]api.APIConsumerUsageStatementBucketResponse, 0, len(statement.Buckets)),
		CreditAppliedMillicents: statement.CreditAppliedMillicents, AmountDueMillicents: statement.AmountDueMillicents(),
	}
	for _, bucket := range statement.Buckets {
		out.Buckets = append(out.Buckets, api.APIConsumerUsageStatementBucketResponse{
			WaivedUnits: bucket.WaivedUnits,
			WindowStart: bucket.WindowStart.UTC(), BillableUnits: bucket.BillableUnits,
			RateCardID: bucket.RateCardID, Currency: bucket.Currency,
			PriceMillicentsPerUnit: bucket.PriceMillicentsPerUnit, ChargedUnits: bucket.Charged(),
			TierUnits: bucket.TierUnits, AmountMillicents: bucket.AmountMillicents, Trial: bucket.Trial, Unit: bucket.Unit, Model: bucket.Model,
		})
	}
	if w := statement.Waiver; w != nil {
		out.Waiver = &api.APIConsumerUsageWaiverResponse{
			APIConsumerUsageWaiverRequest: api.APIConsumerUsageWaiverRequest{
				SourceStatementID: w.SourceStatementID, Unit: w.Unit, Model: w.Model, Since: w.Since, Until: w.Until,
				Units: w.Units, Reason: w.Reason,
			}, ApprovedBy: w.ApprovedBy, ApprovedAt: w.ApprovedAt,
		}
	}
	for _, fee := range statement.Fees {
		out.Fees = append(out.Fees, api.APIConsumerStatementFeeResponse{Kind: fee.Kind, RateCardID: fee.RateCardID, AmountMillicents: fee.AmountMillicents})
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

// createAPIConsumerUsageStatement drafts the period's statement (ADR-949).
func (s *server) createAPIConsumerUsageStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, consumer, store, ok := s.apiConsumerUsageStatementStore(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAPIConsumerUsageStatementRequest
	// Authorize waiver requests before checking for a cached response. Keep
	// the original body when the idempotency middleware needs its fingerprint.
	var body bytes.Buffer
	if r.Header.Get("Idempotency-Key") != "" && r.Body != nil {
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.TeeReader(r.Body, &body), r.Body}
	}
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if body.Len() > 0 {
		r.Body = io.NopCloser(&body)
	}
	periodStart, periodEnd, ok := validateAPIConsumerUsageStatementPeriod(w, req)
	if !ok {
		return
	}
	if req.Waiver != nil {
		s.requireScope(api.ScopesAdminOnly...)(s.idempotent(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
			s.approveConsumerUsageWaiver(w, r, acct, app.ID, consumer.ID, store, periodStart, periodEnd, *req.Waiver)
		}))(w, r, acct)
		return
	}
	s.idempotent(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		statement, created, err := s.draftAPIConsumerStatement(r.Context(), acct, app.ID, consumer.ID, store, periodStart, periodEnd)
		if err != nil {
			api.WriteProblem(w, apiConsumerStatementDraftProblem(err))
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, apiConsumerUsageStatementResponse(statement))
	})(w, r, acct)
}

// draftAPIConsumerStatement plans the period against its existing revisions
// (ADR-949). Unchanged usage replays the latest revision (created is false).
// A changed draft is superseded by a new draft, and usage that arrives after
// finalization becomes a new revision holding only the uncovered units. The
// handler and billing runs (ADR-1503) share it.
func (s *server) draftAPIConsumerStatement(ctx context.Context, acct state.Account, appID, consumerID string,
	store state.APIConsumerUsageStatementStore, start, end time.Time) (state.APIConsumerUsageStatement, bool, error) {
	revisions, err := store.ListAPIConsumerUsageStatementRevisions(ctx, acct.ID, appID, consumerID, start, end)
	if err != nil {
		return state.APIConsumerUsageStatement{}, false, fmt.Errorf("list statement revisions: %w", err)
	}
	input, unchanged, err := s.planAPIConsumerUsageStatement(ctx, acct.ID, appID, consumerID, start, end, revisions)
	if err != nil {
		return state.APIConsumerUsageStatement{}, false, err
	}
	if unchanged {
		return revisions[len(revisions)-1], false, nil
	}
	statement, created, err := store.CreateAPIConsumerUsageStatement(ctx, input)
	if err != nil || !created {
		return statement, false, err
	}
	s.audit.Emit(ctx, "api_consumer_usage_statement.created", &acct.ID, map[string]any{
		"app_id": input.AppID, "consumer_id": input.ConsumerID, "statement_id": statement.ID,
		"revision":     statement.Revision,
		"period_start": input.PeriodStart.Format(time.RFC3339), "period_end": input.PeriodEnd.Format(time.RFC3339),
		"amount_millicents": statement.AmountMillicents, "unpriced_units": statement.UnpricedUnits,
	})
	return statement, true, nil
}

// apiConsumerStatementDraftProblem explains why a statement could not be
// drafted. Billing runs record its detail as the item's reason.
func apiConsumerStatementDraftProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, billing.ErrAPIConsumerUsageRegressed):
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage coverage conflict", "current usage is below an earlier finalized statement")
	case errors.Is(err, billing.ErrAPIConsumerChargeDecreased):
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Charge would decrease", "re-rating this period's tiers would lower charges below finalized statements; Gregale does not issue credits")
	case errors.Is(err, errTieredStatementPeriod):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid statement period", "a tiered rate card prices this period, so it must be exactly one UTC calendar month")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Statement revision conflict", "statement state changed; reload the period and retry")
	default:
		return api.ErrInternal("could not create API consumer usage statement")
	}
}

func validateAPIConsumerUsageStatementPeriod(w http.ResponseWriter, req api.CreateAPIConsumerUsageStatementRequest) (time.Time, time.Time, bool) {
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
func (s *server) planAPIConsumerUsageStatement(ctx context.Context, accountID, appID, consumerID string, start, end time.Time,
	revisions []state.APIConsumerUsageStatement) (state.APIConsumerUsageStatementInput, bool, error) {
	usageStore, ok := s.store.(state.ConsumerUsageStore)
	if !ok {
		return state.APIConsumerUsageStatementInput{}, false, errors.New("consumer usage ledger is unavailable")
	}
	cardsStore, ok := s.store.(state.APIConsumerRateCardStore)
	if !ok {
		return state.APIConsumerUsageStatementInput{}, false, errors.New("API consumer pricing is unavailable")
	}
	// Monthly allowances count from the start of start's month (ADR-950).
	usage, err := usageStore.ListAPIConsumerUsage(ctx, accountID, appID, consumerID, billing.MonthStart(start), end)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(ctx, accountID, appID)
	if err == nil {
		cards, err = s.consumerPriceHistory(ctx, cards, accountID, appID, consumerID)
	}
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	if (billing.TieredCardEffectiveIn(cards, start, end) || billing.MonthlyFeeCardEffectiveIn(cards, start, end)) && !billing.IsCalendarMonth(start, end) {
		return state.APIConsumerUsageStatementInput{}, false, errTieredStatementPeriod
	}
	inputs, err := s.consumerUsageInputs(ctx, cards, accountID, appID, consumerID, billing.MonthStart(start), end, false)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	opts, err := s.consumerQuoteOptions(ctx, accountID, appID, consumerID)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	if billing.ConsumerModelRatesBefore(cards, end) {
		opts.ModelWaivers, err = s.consumerUsageWaivers(ctx, accountID, appID, consumerID, start, end)
		if err != nil {
			return state.APIConsumerUsageStatementInput{}, false, err
		}
	}
	quote, err := consumerStatementDelta(cards, usage, inputs, start, revisions, opts)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	consumer, err := s.store.GetAPIConsumerByID(ctx, accountID, consumerID)
	if err != nil {
		return state.APIConsumerUsageStatementInput{}, false, err
	}
	var fees []state.APIConsumerStatementFee
	if consumer.RevokedAt == nil || !consumer.RevokedAt.Before(start) {
		if consumer.RevokedAt != nil {
			// Prorating cards stop at the revocation (ADR-20261019100332).
			opts.EndedAt = consumer.RevokedAt.UTC()
		}
		fees = billing.APIConsumerStatementFees(cards, start, end, consumer.CreatedAt, opts, revisions, quote.Buckets)
	}
	if len(quote.Buckets) == 0 && len(fees) == 0 && len(revisions) > 0 {
		return state.APIConsumerUsageStatementInput{}, true, nil
	}
	input := state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID,
		PeriodStart: start, PeriodEnd: end, Revision: len(revisions) + 1, Currency: quote.Currency,
		BillableUnits: quote.BillableUnits, UnpricedUnits: quote.UnpricedUnits,
		AmountMillicents: quote.AmountMillicents, Priced: quote.Priced || (len(fees) > 0 && quote.UnpricedUnits == 0),
		Buckets: make([]state.APIConsumerUsageStatementBucket, 0, len(quote.Buckets)), Fees: fees, AsOf: time.Now().UTC(),
	}
	for _, fee := range fees {
		input.AmountMillicents += fee.AmountMillicents
	}
	if len(revisions) > 0 {
		input.PriorStatus = revisions[len(revisions)-1].Status
	}
	for _, bucket := range quote.Buckets {
		input.Buckets = append(input.Buckets, statementBucketFromCharge(bucket))
	}
	return input, false, nil
}

// consumerStatementDelta prices a period and bills what finalized revisions
// have not. If exact re-rating would lower what they charged, because late
// usage moved a volume month into a cheaper step, the volume cards keep the
// step those revisions billed (ADR-1312).
func consumerStatementDelta(cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket, inputs billing.ConsumerUsageInputs,
	start time.Time, revisions []state.APIConsumerUsageStatement, opts billing.QuoteOptions) (billing.APIConsumerUsageQuote, error) {
	current, err := billing.QuoteConsumerUsage(cards, usage, inputs, start, opts)
	if err != nil {
		return billing.APIConsumerUsageQuote{}, err
	}
	quote, err := billing.APIConsumerStatementDelta(current, revisions)
	if !errors.Is(err, billing.ErrAPIConsumerChargeDecreased) {
		return quote, err
	}
	locked := billing.LockedVolumeSteps(cards, revisions)
	if len(locked) == 0 {
		return quote, err
	}
	opts.VolumeSteps = locked
	if current, err = billing.QuoteConsumerUsage(cards, usage, inputs, start, opts); err != nil {
		return billing.APIConsumerUsageQuote{}, err
	}
	return billing.APIConsumerStatementDelta(current, revisions)
}

// consumerQuoteOptions are how a consumer's usage is priced beyond its
// cards: its trial (ADR-1314).
func (s *server) consumerQuoteOptions(ctx context.Context, accountID, appID, consumerID string) (billing.QuoteOptions, error) {
	trials, ok := s.store.(state.APIConsumerTrialStore)
	if !ok {
		return billing.QuoteOptions{}, nil
	}
	trial, err := trials.GetAPIConsumerTrial(ctx, accountID, appID, consumerID)
	if errors.Is(err, state.ErrNotFound) {
		return billing.QuoteOptions{}, nil
	}
	if err != nil {
		return billing.QuoteOptions{}, fmt.Errorf("load consumer trial: %w", err)
	}
	return billing.QuoteOptions{TrialStart: trial.StartsAt, TrialEnd: trial.EndsAt}, nil
}

// statementBucketFromCharge persists a priced minute; charged_units is
// recorded only for priced buckets, where it determines the amount.
func statementBucketFromCharge(bucket billing.APIConsumerUsageChargeBucket) state.APIConsumerUsageStatementBucket {
	out := state.APIConsumerUsageStatementBucket{
		WaivedUnits: bucket.WaivedUnits,
		WindowStart: bucket.WindowStart, BillableUnits: bucket.BillableUnits,
		RateCardID: bucket.RateCardID, Currency: bucket.Currency,
		PriceMillicentsPerUnit: bucket.PriceMillicentsPerUnit, AmountMillicents: bucket.AmountMillicents,
		TierUnits: bucket.TierUnits, Trial: bucket.Trial, Unit: bucket.Unit, Model: bucket.Model,
	}
	if bucket.RateCardID != "" {
		charged := bucket.ChargedUnits
		out.ChargedUnits = &charged
	}
	return out
}

// errTieredStatementPeriod rejects a non-month period priced by a tiered
// card, where re-rating across statements of one month would need credits
// (ADR-951), or by a card with monthly fees, billed once per month
// (ADR-1316).
var errTieredStatementPeriod = errors.New("tiered rate cards and monthly fees require calendar-month statement periods")

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
	statement, err := s.finalizeAPIConsumerStatement(r.Context(), acct, app.ID, consumer.ID, store, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such usage statement")
		return
	}
	if err != nil {
		api.WriteProblem(w, apiConsumerStatementFinalizeProblem(err))
		return
	}
	writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(statement))
}

// finalizeAPIConsumerStatement freezes a statement. The first finalization
// is audited and relays the usage_statement.finalized webhook; finalizing
// again is a no-op. The handler and billing runs (ADR-1503) share it.
func (s *server) finalizeAPIConsumerStatement(ctx context.Context, acct state.Account, appID, consumerID string,
	store state.APIConsumerUsageStatementStore, statementID string) (state.APIConsumerUsageStatement, error) {
	draft, err := store.GetAPIConsumerUsageStatement(ctx, acct.ID, appID, consumerID, statementID)
	if err != nil {
		return draft, err
	}
	if draft.Status == state.APIConsumerUsageStatementDraft {
		for _, bucket := range draft.Buckets {
			if bucket.Model != "" && bucket.RateCardID == "" && bucket.BillableUnits > bucket.WaivedUnits {
				return draft, &unpricedConsumerModelError{unit: bucket.Unit, model: bucket.Model, minute: bucket.WindowStart}
			}
		}
		if draft.UnpricedUnits == 0 {
			if err := s.checkConsumerModelRates(ctx, draft); err != nil {
				return draft, err
			}
		}
	}
	statement, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, acct.ID, appID, consumerID, statementID)
	if err != nil || !changed {
		return statement, err
	}
	s.audit.Emit(ctx, "api_consumer_usage_statement.finalized", &acct.ID, map[string]any{
		"app_id": appID, "consumer_id": consumerID, "statement_id": statement.ID,
		"finalized_at": statement.FinalizedAt.UTC().Format(time.RFC3339Nano),
	})
	if outbox, ok := s.store.(state.AppWebhookEventOutboxStore); ok {
		if _, err := outbox.RelayAppWebhookEventOutboxSource(ctx, state.AppWebhookEventUsageStatementFinalized, statement.ID); err != nil {
			s.log.WarnContext(ctx, "relay usage statement finalized webhook",
				"app_id", appID, "statement_id", statement.ID, "err", err.Error())
		}
	}
	return statement, nil
}

type unpricedConsumerModelError struct {
	unit, model string
	minute      time.Time
}

func (e *unpricedConsumerModelError) Error() string {
	return fmt.Sprintf("meter %s has no rate for model %s at %s; review the usage quote before finalization",
		e.unit, e.model, e.minute.UTC().Format(time.RFC3339))
}

// checkConsumerModelRates rechecks the metering ledger for a priced draft so
// a missing model rate recorded after drafting also holds the statement.
// Usage recorded after this read can be included in the next revision.
func (s *server) checkConsumerModelRates(ctx context.Context, statement state.APIConsumerUsageStatement) error {
	cardsStore, ok := s.store.(state.APIConsumerRateCardStore)
	if !ok {
		return nil
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(ctx, statement.AccountID, statement.AppID)
	if err != nil {
		return err
	}
	if !billing.ConsumerModelRatesBefore(cards, statement.PeriodEnd) {
		return nil
	}
	cards, err = s.consumerPriceHistory(ctx, cards, statement.AccountID, statement.AppID, statement.ConsumerID)
	if err != nil {
		return err
	}
	if !billing.ConsumerModelRatesBefore(cards, statement.PeriodEnd) {
		return nil
	}
	meterStore, ok := s.store.(state.ConsumerMeterUsageStore)
	if !ok {
		return errors.New("API consumer model usage is unavailable")
	}
	meters, err := consumerReportedMeters(ctx, meterStore, statement.AccountID, statement.AppID, statement.ConsumerID,
		statement.PeriodStart, statement.PeriodEnd)
	if err != nil {
		return err
	}
	missing := billing.UnpricedConsumerModelUsage(cards, meters, statement.PeriodStart)
	if len(missing) == 0 {
		return nil
	}
	waivers, err := s.consumerUsageWaivers(ctx, statement.AccountID, statement.AppID, statement.ConsumerID,
		statement.PeriodStart, statement.PeriodEnd)
	if err != nil {
		return err
	}
	billing.ApplyConsumerModelWaivers(missing, waivers)
	for _, line := range missing {
		if line.BillableUnits > line.WaivedUnits {
			return &unpricedConsumerModelError{unit: line.Unit, model: line.Model, minute: line.WindowStart}
		}
	}
	return nil
}

func apiConsumerStatementFinalizeProblem(err error) *api.Problem {
	var missing *unpricedConsumerModelError
	if errors.As(err, &missing) {
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage statement cannot be finalized", missing.Error())
	}
	if errors.Is(err, state.ErrConflict) {
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Usage statement cannot be finalized", "all usage must have an effective rate card before finalization")
	}
	return api.ErrInternal("could not finalize API consumer usage statement")
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

func (s *server) consumerUsageWaivers(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) ([]state.APIConsumerUsageWaiver, error) {
	store, ok := s.store.(state.APIConsumerUsageWaiverStore)
	if !ok {
		return nil, errors.New("API consumer usage waivers are unavailable")
	}
	return store.ListAPIConsumerUsageWaivers(ctx, accountID, appID, consumerID, since, until)
}

func (s *server) approveConsumerUsageWaiver(w http.ResponseWriter, r *http.Request, acct state.Account, appID, consumerID string,
	store state.APIConsumerUsageStatementStore, start, end time.Time, req api.APIConsumerUsageWaiverRequest) {
	operator, key, _, ok := middleware.PrincipalFrom(r)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Authentication required", "authenticate to approve usage waivers"))
		return
	}
	// The account handler uses the app's resolved owner scope. The verified
	// principal supplies the independent approver identity.
	approval := state.APIConsumerUsageWaiver{
		SourceStatementID: req.SourceStatementID, Unit: req.Unit, Model: req.Model,
		Since: req.Since.UTC(), Until: req.Until.UTC(), Units: req.Units, Reason: strings.TrimSpace(req.Reason),
		ApprovedBy: operator.ID, ApprovedAt: time.Now().UTC(),
	}
	if key != nil && key.ID != "" {
		approval.ApprovedWith = "key:" + key.ID
	}
	conflict := func() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Usage waiver conflict",
			"review the latest held draft and confirm its unpriced units for the selected model and minute window"))
	}
	revisions, err := store.ListAPIConsumerUsageStatementRevisions(r.Context(), acct.ID, appID, consumerID, start, end)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load usage statement revisions"))
		return
	}
	for _, revision := range revisions {
		if revision.Waiver != nil && revision.Waiver.SourceStatementID == approval.SourceStatementID {
			if revision.Waiver.SameSelection(approval) {
				writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(revision))
			} else {
				conflict()
			}
			return
		}
	}
	if len(revisions) == 0 {
		conflict()
		return
	}
	input, err := state.PlanAPIConsumerUsageWaiver(revisions[len(revisions)-1], approval)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid usage waiver", err.Error()))
		return
	case err != nil:
		conflict()
		return
	}
	statement, created, err := store.CreateAPIConsumerUsageStatement(r.Context(), input)
	if errors.Is(err, state.ErrConflict) || (err == nil && (statement.Waiver == nil || !statement.Waiver.SameSelection(approval))) {
		conflict()
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not approve the usage waiver"))
		return
	}
	if created {
		s.audit.Emit(r.Context(), "api_consumer_usage_waiver.approved", &acct.ID, map[string]any{
			"app_id": appID, "consumer_id": consumerID, "statement_id": statement.ID,
			"source_statement_id": approval.SourceStatementID, "unit": approval.Unit, "model": approval.Model,
			"since": approval.Since, "until": approval.Until, "units": approval.Units, "reason": approval.Reason,
			"approved_by": approval.ApprovedBy,
		})
		writeJSON(w, http.StatusCreated, apiConsumerUsageStatementResponse(statement))
		return
	}
	writeJSON(w, http.StatusOK, apiConsumerUsageStatementResponse(statement))
}
