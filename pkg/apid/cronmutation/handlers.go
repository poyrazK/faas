// Package cronmutation owns reviewed cron mutations and passive receipt reads.
package cronmutation

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type Deps struct {
	Deletion bool
	Store    state.CronMutationStore
	Resolve  func(http.ResponseWriter, *http.Request, state.Account) (state.App, bool)
	Validate func(*api.CronMutationRequest) *api.Problem
	After    func(*http.Request, state.Account, api.CronMutationReceipt)
}

func Apply(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		app, ok := d.Resolve(w, r, account)
		if !ok {
			return
		}
		if d.Store == nil {
			api.WriteProblem(w, api.ErrCapacity("reviewed cron mutations are unavailable"))
			return
		}
		var request api.CronMutationRequest
		if httpkit.DecodeJSON(r, &request) != nil || !request.Valid() || request.AppID != app.ID || request.Delete != d.Deletion {
			api.WriteProblem(w, api.ErrValidation("invalid reviewed cron mutation"))
			return
		}
		if problem := d.Validate(&request); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		out, err := d.Store.ApplyCronMutation(r.Context(), account.ID, app.ID, request)
		if err != nil {
			writeError(w, err)
			return
		}
		if d.After != nil {
			d.After(r, account, out)
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func Receipt(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		app, ok := d.Resolve(w, r, account)
		if !ok {
			return
		}
		if d.Store == nil {
			api.WriteProblem(w, api.ErrCapacity("cron receipt recovery is unavailable"))
			return
		}
		id, err := uuid.Parse(r.PathValue("request_id"))
		proofs := r.Header.Values(api.CronMutationReceiptProofHeader)
		if err != nil || id == uuid.Nil || len(proofs) != 1 || !api.ValidEnvBatchReceiptProof(proofs[0]) {
			api.WriteProblem(w, api.ErrValidation("request_id and one private receipt proof are required"))
			return
		}
		out, err := d.Store.CronMutationReceipt(r.Context(), account.ID, app.ID, id.String(), proofs[0])
		if err != nil {
			writeError(w, err)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrCronReviewConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "cron_review_conflict", "Cron changed", "The reviewed cron state changed; inspect before reviewing a new plan"))
	case errors.Is(err, state.ErrCronReviewExpired):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "cron_review_expired", "Review expired", "Write authority expired; retained receipts remain available read-only"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "No retained cron mutation for this target"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid reviewed cron mutation target or changes"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "cron_mutation_conflict", "Mutation conflict", "This request identity or cron configuration conflicts with retained state"))
	default:
		api.WriteProblem(w, api.ErrCapacity("cron mutation unavailable; inspect or retry the same unexpired request"))
	}
}
