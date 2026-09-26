package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cancelCronCommandRun handles POST /v1/crons/{id}/runs/{run_id}/cancel.
// It resolves the cron and verifies the run's immutable cron_id before
// invoking the app-task cancellation path. This keeps a run id from another
// cron (or account) indistinguishable from a missing run.
func (s *server) cancelCronCommandRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	cronID := r.PathValue("id")
	if _, err := uuid.Parse(cronID); err != nil {
		s.notFound(w, "no such cron run")
		return
	}
	runID := r.PathValue("run_id")
	if _, err := uuid.Parse(runID); err != nil {
		s.notFound(w, "no such cron run")
		return
	}
	cron, err := s.store.CronByID(r.Context(), cronID)
	if err != nil {
		s.notFound(w, "no such cron run")
		return
	}
	app, err := s.store.AppByID(r.Context(), cron.AppID)
	if err != nil || app.AccountID != acct.ID || len(cron.Command) == 0 {
		s.notFound(w, "no such cron run")
		return
	}
	tasks, ok := s.store.(state.AppTaskStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cancel cron command run"))
		return
	}
	task, err := tasks.AppTaskByID(r.Context(), acct.ID, app.ID, runID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && (task.Kind != state.AppTaskKindCron || task.CronID != cron.ID)) {
		s.notFound(w, "no such cron run")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("cancel cron command run"))
		return
	}
	task, err = tasks.RequestAppTaskCancellation(r.Context(), acct.ID, app.ID, runID, time.Now().UTC())
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such cron run")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("cancel cron command run"))
		return
	}
	writeJSON(w, http.StatusAccepted, appTaskResponse(task))
}
