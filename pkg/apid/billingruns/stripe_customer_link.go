package billingruns

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// LinkStripeCustomer resolves a missing customer on a completed draft run.
// The connection and original statement both come from the owner's review.
func (s *Service) LinkStripeCustomer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, store, ok := s.completedDraftRun(w, r, acct)
	if !ok {
		return
	}
	consumerID, err := uuid.Parse(r.PathValue("consumer_id"))
	if err != nil {
		httpkit.NotFound(w, "no such billing run customer")
		return
	}
	var req api.LinkBillingRunStripeCustomerRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil || req.StripeCustomer == "" || req.StatementID == "" ||
		req.Livemode == nil || req.ConnectionUpdatedAt.IsZero() {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid Stripe customer link",
			"supply the Stripe customer, reviewed statement and connection"))
		return
	}
	ctx := r.Context()
	item, err := store.GetBillingRunAppConsumerItem(ctx, acct.ID, run.ID, consumerID.String())
	if errors.Is(err, state.ErrNotFound) || (err == nil && (item.StatementID != req.StatementID || item.Outcome != state.BillingRunItemDrafted)) {
		api.WriteProblem(w, draftReviewConflict("the customer has no draft for the reviewed statement in this run"))
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
	if conn.Provider == "" || conn.Collection != state.CollectionNone || conn.Livemode != *req.Livemode ||
		!conn.UpdatedAt.Equal(req.ConnectionUpdatedAt) {
		api.WriteProblem(w, draftReviewConflict("the Stripe connection changed; review the draft again"))
		return
	}
	if conn.StripeAccountID != "" {
		links, ok := s.Host.Store().(state.StripeConsumerLinkStore)
		if !ok {
			api.WriteProblem(w, api.ErrInternal("Stripe customer links are unavailable"))
			return
		}
		linked, err := links.GetStripeConsumerCustomer(ctx, acct.ID, item.SourceID, conn.StripeAccountID, conn.Livemode)
		switch {
		case err == nil && linked == req.StripeCustomer:
			w.WriteHeader(http.StatusNoContent)
			return
		case err == nil:
			api.WriteProblem(w, draftReviewConflict("this app customer already has a different Stripe link"))
			return
		case !errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.ErrInternal("could not read the Stripe customer link"))
			return
		}
	}
	draft, problem := s.prepareDraftExport(ctx, acct, run, item, conn)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if draft.row.BlockerCode != api.CodeStripeCustomerMissing {
		api.WriteProblem(w, draftReviewConflict("only a draft with a missing Stripe customer can be linked here"))
		return
	}
	if problem := s.Host.LinkConsumerStripeCustomer(ctx, acct, draft.consumer, conn, req.StripeCustomer); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
