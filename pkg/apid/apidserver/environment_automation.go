package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// resumeEnvironmentAutomation enables, in one stage, the crons a full clone
// copied paused that the source ran (ADR-20261104120000 amendment
// 2026-11-12). Crons the source had disabled, and any a customer enabled or
// disabled since, stay as they are. Production has nothing a clone paused.
func (s *server) resumeEnvironmentAutomation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if p, ok := principalFrom(r); ok && p.Key != nil && p.Key.AppID != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account key required",
			"resuming an environment's automation requires an account-scoped credential"))
		return
	}
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	environment := r.PathValue("environment")
	if state.IsProductionScope(environment) {
		api.WriteProblem(w, api.ErrValidation("production has no automation a clone paused; name a stage"))
		return
	}
	store, ok := s.store.(state.CloneResumeStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("environment automation resume unavailable"))
		return
	}
	resumed, err := store.ResumeEnvironmentClonePausedCrons(r.Context(), project.AccountID, project.ID, environment)
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found",
			"no environment "+environment+" in project "+project.Slug))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not resume the environment's automation"))
		return
	}
	resp := api.ResumeEnvironmentAutomationResponse{Environment: environment, CronsResumed: len(resumed), CronIDs: make([]string, 0, len(resumed))}
	for _, c := range resumed {
		resp.CronIDs = append(resp.CronIDs, c.ID)
		_ = s.notif.Notify(r.Context(), db.NotifyCronChanged, `{"kind":"updated","cron":"`+c.ID+`"}`)
	}
	s.audit.Emit(r.Context(), "project.environment.automation_resumed", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug, "environment": environment, "cron_ids": resp.CronIDs,
	})
	writeJSON(w, http.StatusOK, resp)
}
