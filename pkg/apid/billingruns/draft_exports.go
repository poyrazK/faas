package billingruns

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

type preparedDraftExport struct {
	row       api.BillingRunDraftExportRow
	consumer  state.APIConsumer
	statement state.APIConsumerUsageStatement
}

func draftReviewConflict(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Draft export needs review", detail)
}

func (s *Service) completedDraftRun(w http.ResponseWriter, r *http.Request, acct state.Account) (state.BillingRun, state.BillingRunStore, bool) {
	if !s.Host.ConsumerFeatureAllowed(w, acct) {
		return state.BillingRun{}, nil, false
	}
	store, ok := s.runStore(w)
	if !ok {
		return state.BillingRun{}, nil, false
	}
	runID, ok := pathRunID(w, r)
	if !ok {
		return state.BillingRun{}, nil, false
	}
	run, err := store.GetBillingRun(r.Context(), acct.ID, runID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no such billing run")
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not load the billing run"))
	case run.Status != state.BillingRunCompleted || run.Finalize || run.Export != state.BillingRunExportNone:
		api.WriteProblem(w, draftReviewConflict("review exports from a completed draft-only billing run"))
	default:
		return run, store, true
	}
	return state.BillingRun{}, nil, false
}

func (s *Service) readDraftExportConnection(ctx context.Context, accountID string) (state.BillingConnection, *api.Problem) {
	store, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return state.BillingConnection{}, api.ErrInternal("billing connections are unavailable")
	}
	conn, err := store.GetBillingConnection(ctx, accountID, state.BillingProviderStripe)
	if errors.Is(err, state.ErrNotFound) {
		return state.BillingConnection{}, nil
	}
	if err != nil {
		return state.BillingConnection{}, api.ErrInternal("could not load the Stripe connection")
	}
	return conn, nil
}

// ReviewDraftExports pages the current eligibility and projected amount due for
// the original statements in a completed run. It performs no provider calls.
func (s *Service) ReviewDraftExports(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, store, ok := s.completedDraftRun(w, r, acct)
	if !ok {
		return
	}
	ctx := r.Context()
	conn, problem := s.readDraftExportConnection(ctx, acct.ID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	const size = api.BillingRunDraftExportPageSize
	items, err := store.ListBillingRunItems(ctx, acct.ID, run.ID, "", r.URL.Query().Get("cursor"), size+1)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list the billing run items"))
		return
	}
	out := api.BillingRunDraftExportReview{Items: make([]api.BillingRunDraftExportRow, 0, min(size, len(items)))}
	switch {
	case conn.Provider == "":
		out.ConnectionProblem = "Connect Stripe to export drafts."
	case conn.Collection != "none":
		out.ConnectionProblem = "Set Stripe collection to Draft only to export drafts."
	default:
		version := conn.UpdatedAt.UTC()
		out.Livemode, out.ConnectionUpdatedAt = &conn.Livemode, &version
	}
	if len(items) > size {
		items = items[:size]
		out.NextCursor = items[len(items)-1].Key()
	}
	for _, item := range items {
		draft, problem := s.prepareDraftExport(ctx, acct, run, item, conn)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		out.Items = append(out.Items, draft.row)
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func blockDraftExport(draft preparedDraftExport, reason string) (preparedDraftExport, *api.Problem) {
	draft.row.Status, draft.row.Reason = "blocked", reason
	return draft, nil
}

func (s *Service) prepareDraftExport(ctx context.Context, acct state.Account, run state.BillingRun,
	item state.BillingRunItem, conn state.BillingConnection) (preparedDraftExport, *api.Problem) {
	draft := preparedDraftExport{row: api.BillingRunDraftExportRow{
		ID: item.Key(), CustomerName: item.ExternalRef, StatementID: item.StatementID, Revision: item.Revision,
	}}
	if item.SourceKind != state.StatementSourceAppConsumer {
		return blockDraftExport(draft, "This review supports Stripe-linked app customers.")
	}
	draft.row.ConsumerID = item.SourceID
	if item.Outcome != state.BillingRunItemDrafted || item.StatementID == "" {
		reason := item.Reason
		if reason == "" {
			reason = "This run did not produce an eligible draft for the customer."
		}
		return blockDraftExport(draft, reason)
	}
	store, ok := s.Host.Store().(state.APIConsumerUsageStatementStore)
	if !ok {
		return draft, api.ErrInternal("usage statement review is unavailable")
	}
	consumer, err := s.Host.Store().GetAPIConsumerByID(ctx, acct.ID, item.SourceID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && consumer.AppID != item.AppID) {
		return blockDraftExport(draft, "The app customer is unavailable.")
	}
	if err != nil {
		return draft, api.ErrInternal("could not load the app customer")
	}
	draft.consumer = consumer
	if consumer.Name != "" {
		draft.row.CustomerName = consumer.Name
	}
	statement, err := store.GetAPIConsumerUsageStatement(ctx, acct.ID, item.AppID, item.SourceID, item.StatementID)
	if errors.Is(err, state.ErrNotFound) {
		return blockDraftExport(draft, "The statement is unavailable.")
	}
	if err != nil {
		return draft, api.ErrInternal("could not load the usage statement")
	}
	if statement.Revision != item.Revision || !statement.PeriodStart.Equal(run.PeriodStart) ||
		!statement.PeriodEnd.Equal(run.PeriodEnd) ||
		(statement.Status != state.APIConsumerUsageStatementDraft && statement.Status != state.APIConsumerUsageStatementFinalized) {
		return blockDraftExport(draft, "The run's statement revision is no longer current.")
	}
	if statement.UnpricedUnits != 0 || statement.Currency == "" {
		return blockDraftExport(draft, "The statement needs pricing before it can be finalized.")
	}
	if statement.Status == state.APIConsumerUsageStatementDraft {
		credits, ok := s.Host.Store().(state.APIConsumerCreditStore)
		if !ok {
			return draft, api.ErrInternal("usage statement credit review is unavailable")
		}
		pos, err := credits.APIConsumerCreditPosition(ctx, acct.ID, item.AppID, item.SourceID, statement.Currency, time.Now().UTC())
		if err != nil || pos.AvailableMillicents < 0 {
			return draft, api.ErrInternal("could not preview the customer's credits")
		}
		statement.CreditAppliedMillicents = min(pos.AvailableMillicents, statement.AmountMillicents)
	}
	due := statement.AmountDueMillicents()
	draft.statement = statement
	draft.row.Currency, draft.row.AmountDueMillicents = statement.Currency, &due
	if due <= 0 {
		return blockDraftExport(draft, "The statement has no balance due after credits.")
	}
	customer, invoice, problem := s.Host.ReviewConsumerDraftExport(ctx, acct, consumer, statement, conn)
	if problem != nil {
		if problem.Status >= http.StatusInternalServerError {
			return draft, problem
		}
		if problem.Code == api.CodeStripeCustomerMissing {
			draft.row.BlockerCode = problem.Code
		}
		return blockDraftExport(draft, problemDetail(problem))
	}
	draft.row.StripeCustomer = customer
	if invoice != nil {
		draft.row.Status, draft.row.Invoice = "exported", invoice
		return draft, nil
	}
	completeness, err := s.Host.ConsumerCompleteness(ctx, acct, item.AppID, item.SourceID, run.PeriodStart, run.PeriodEnd)
	if err != nil {
		return draft, api.ErrInternal("could not check the customer's usage completeness")
	}
	if completeness.Status == billing.CompletenessGapsDetected {
		return blockDraftExport(draft, "Usage gaps must be reconciled before exporting.")
	}
	if item.UsageRecoveredAt != nil {
		_, missing, problem := s.usageHoldHours(ctx, acct.ID, item)
		if problem != nil {
			return draft, problem
		}
		if missing > 0 {
			return blockDraftExport(draft, "The usage ledger no longer covers the recorded hold.")
		}
	}
	draft.row.Status = "ready"
	return draft, nil
}

func validDraftApproval(req api.ApproveBillingRunDraftExportRequest) bool {
	consumer, consumerErr := uuid.Parse(req.ConsumerID)
	statement, statementErr := uuid.Parse(req.StatementID)
	return consumerErr == nil && consumer.String() == req.ConsumerID &&
		statementErr == nil && statement.String() == req.StatementID &&
		req.Revision >= 1 && req.AmountDueMillicents > 0 && req.Currency != "" &&
		req.StripeCustomer != "" && req.Livemode != nil && !req.ConnectionUpdatedAt.IsZero()
}

// ApproveDraftExport finalizes and exports one reviewed row. Independently
// replayable rows let the console finish the rest of an approved selection
// when a provider call fails, and resume the same invoice on a retry.
func (s *Service) ApproveDraftExport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, store, ok := s.completedDraftRun(w, r, acct)
	if !ok {
		return
	}
	var req api.ApproveBillingRunDraftExportRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil || !validDraftApproval(req) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid draft approval", "supply the reviewed statement, amount, customer and Stripe connection"))
		return
	}
	ctx := r.Context()
	item, err := store.GetBillingRunAppConsumerItem(ctx, acct.ID, run.ID, req.ConsumerID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && item.StatementID != req.StatementID) {
		api.WriteProblem(w, draftReviewConflict("the reviewed statement is not part of this run"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the billing run item"))
		return
	}
	conn, problem := s.readDraftExportConnection(ctx, acct.ID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if conn.Provider == "" || conn.Collection != "none" || conn.Livemode != *req.Livemode ||
		!conn.UpdatedAt.Equal(req.ConnectionUpdatedAt) {
		api.WriteProblem(w, draftReviewConflict("the Stripe connection changed; review the draft again"))
		return
	}
	draft, problem := s.prepareDraftExport(ctx, acct, run, item, conn)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if draft.row.Status == "blocked" {
		api.WriteProblem(w, draftReviewConflict(draft.row.Reason))
		return
	}
	if draft.row.Revision != req.Revision || draft.row.Currency != req.Currency ||
		draft.row.AmountDueMillicents == nil || *draft.row.AmountDueMillicents != req.AmountDueMillicents ||
		draft.row.StripeCustomer != req.StripeCustomer {
		api.WriteProblem(w, draftReviewConflict("the revision, amount after credits or Stripe customer changed; review the draft again"))
		return
	}
	if draft.row.Invoice != nil {
		if draft.row.Invoice.Livemode != *req.Livemode {
			api.WriteProblem(w, draftReviewConflict("the existing draft was exported in another Stripe mode"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, draft.row.Invoice)
		return
	}
	finalized, problem := s.Host.FinalizeReviewedConsumerStatement(ctx, acct, draft.statement, req.AmountDueMillicents)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	invoice, problem := s.Host.ExportReviewedConsumerDraft(ctx, acct, draft.consumer, finalized, req.StripeCustomer, conn)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, invoice)
}
