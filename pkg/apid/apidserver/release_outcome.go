// adr: 1448 — amendment 1: a person records a check after a release command
// whose outcome is unknown, and later release commands run again.
package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordReleaseOutcomeCheck handles POST /v1/deployments/{id}/release-outcome.
func (s *server) recordReleaseOutcomeCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.ReleaseOutcomeStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("release outcome checks are unavailable"))
		return
	}
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	var req api.RecordReleaseOutcomeCheckRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	task, err := s.store.ReleaseAppTaskByDeployment(r.Context(), d.ID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrReleaseOutcomeKnown("This release's command never ran, so there is nothing to check."))
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "load release task", "Gregale could not read this release's command.", "Retry in a moment.", err)
		return
	}
	if !state.ReleaseOutcomeUnknown(task) {
		api.WriteProblem(w, api.ErrReleaseOutcomeKnown("This release's command ended with status "+string(task.Status)+" and a known exit, so there is nothing to check."))
		return
	}
	check, err := store.RecordReleaseOutcomeCheck(r.Context(), state.ReleaseOutcomeCheck{
		TaskID: task.ID, AccountID: app.AccountID, CheckedBy: releaseApprovalActorLabel(r, acct), Note: req.Note, CheckedAt: time.Now(),
	})
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.ErrReleaseOutcomeKnown("This release's command was checked already."))
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "record release outcome check", "Gregale could not record the check.", "Retry in a moment.", err)
		return
	}
	s.audit.Emit(r.Context(), "app.release_outcome_checked", &acct.ID, map[string]any{
		"app_id": app.ID, "deployment": d.ID, "task_id": task.ID, "task_status": string(task.Status), "checked_by": check.CheckedBy,
	})
	writeJSON(w, http.StatusOK, api.ReleaseOutcomeCheck{
		DeploymentID: d.ID, TaskID: task.ID, TaskStatus: string(task.Status),
		CheckedBy: check.CheckedBy, Note: check.Note, CheckedAt: check.CheckedAt,
	})
}
