package appevents

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) PauseEventExecutionLineageRetryJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.controlEventExecutionLineageRetryJob(w, r, acct, "pause")
}
func (s *Service) ResumeEventExecutionLineageRetryJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.controlEventExecutionLineageRetryJob(w, r, acct, "resume")
}
func (s *Service) controlEventExecutionLineageRetryJob(w http.ResponseWriter, r *http.Request, acct state.Account, action string) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	store, app, ok := s.lineageRetryJobTarget(w, r, acct)
	if !ok {
		return
	}
	var req api.ControlEventExecutionLineageRetryJobRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.EventExecutionLineageRetryJobBodyMaxBytes) {
		return
	}
	ctx = lineageControlActorContext(ctx, r)
	out, err := store.ControlEventExecutionLineageRetryJob(ctx, acct.ID, app, r.PathValue("subscriptionID"), r.PathValue("requestID"), action, req)
	if writeLineageRetryControlReceiptError(w, err) {
		return
	}
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_lineage_retry_job_changed", "Lineage retry job changed", "refresh the original creation timestamp and state; completed or cancelled jobs cannot pause or resume"))
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Lineage job control timed out", "repeat the same command UUID, action and creation guard if supplied; otherwise read current job state before repeating the desired control"))
	default:
		if !writeLineageCaseError(w, err) {
			httpkit.WriteJSON(w, http.StatusOK, out)
		}
	}
}

func writeLineageRetryControlReceiptError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, state.ErrLineageControlCommandConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_lineage_retry_control_command_changed", "Control command UUID reused", "reuse a command UUID only with its original action and creation guard"))
	case errors.Is(err, state.ErrLineageControlReceiptLimit):
		api.WriteProblem(w, api.NewProblem(429, "event_lineage_retry_control_limit", "Control receipt limit reached", fmt.Sprintf("the job has %d retained command receipts; existing command repeats and controls without a command UUID remain available", api.EventExecutionLineageRetryControlMaxReceipts)))
	default:
		return false
	}
	return true
}
