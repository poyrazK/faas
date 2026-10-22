package appevents

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func writeLineageCaseBackfillControlError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_lineage_case_backfill_changed", "Diagnostic scan changed or inactive", "refresh the scan UUID and original creation timestamp; completed, cancelled and expired scans cannot be paused or resumed"))
		return true
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Diagnostic scan control timed out", "read the latest scan status, then repeat the desired control with the original UUID and creation timestamp"))
		return true
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("valid scan UUID and original expected_created_at timestamp are required"))
		return true
	default:
		return writeLineageCaseBackfillError(w, err)
	}
}
func (s *Service) controlEventExecutionLineageCaseBackfill(w http.ResponseWriter, r *http.Request, acct state.Account, action string) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventExecutionLineageCaseBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("lineage diagnostic controls unavailable"))
		return
	}
	var req api.ControlEventExecutionLineageCaseBackfillRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.EventExecutionLineageRetryJobBodyMaxBytes) {
		return
	}
	out, err := store.ControlEventExecutionLineageCaseBackfill(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), r.PathValue("requestID"), action, req)
	if !writeLineageCaseBackfillControlError(w, err) {
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
func (s *Service) PauseEventExecutionLineageCaseBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.controlEventExecutionLineageCaseBackfill(w, r, acct, "pause")
}
func (s *Service) ResumeEventExecutionLineageCaseBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.controlEventExecutionLineageCaseBackfill(w, r, acct, "resume")
}
func (s *Service) CancelEventExecutionLineageCaseBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.controlEventExecutionLineageCaseBackfill(w, r, acct, "cancel")
}
