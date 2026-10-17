package apidserver

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) stageCheckedProjectRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	var request api.StageProjectReleaseRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.ProjectReleaseStagingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("release staging is unavailable"))
		return
	}
	stage, problem := bindings.StageProjectRelease(r.Context(), store, acct.ID, project, r.PathValue("environment"), request)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.notifyProjectReleaseStage(r.Context(), stage)
	s.audit.Emit(r.Context(), "project.release_staged", &acct.ID, map[string]any{"project_id": project.ID, "environment": stage.Environment, "stage_id": stage.ID, "expires_at": stage.ExpiresAt})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, stage)
}
func (s *server) getProjectReleaseStage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.readProjectReleaseStage(w, r, acct, false)
}
func (s *server) cancelProjectReleaseStage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.readProjectReleaseStage(w, r, acct, true)
}
func (s *server) readProjectReleaseStage(w http.ResponseWriter, r *http.Request, acct state.Account, cancel bool) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.ProjectReleaseStagingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("release staging is unavailable"))
		return
	}
	var stage api.ProjectReleaseStage
	var err error
	if cancel {
		stage, err = store.CancelProjectReleaseStage(r.Context(), acct.ID, project.ID, r.PathValue("environment"), r.PathValue("stage"))
	} else {
		stage, err = store.ProjectReleaseStage(r.Context(), acct.ID, project.ID, r.PathValue("environment"), r.PathValue("stage"))
	}
	if problem := bindings.ReleaseStageProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if cancel {
		s.notifyProjectReleaseStage(r.Context(), stage)
		s.audit.Emit(r.Context(), "project.release_stage_cancelled", &acct.ID, map[string]any{"project_id": project.ID, "environment": stage.Environment, "stage_id": stage.ID})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, stage)
}

// Notifications accelerate the normal recovery loop; the durable lease remains
// authoritative if delivery fails or schedd restarts.
func (s *server) notifyProjectReleaseStage(ctx context.Context, stage api.ProjectReleaseStage) {
	if s.notif == nil {
		return
	}
	for _, member := range stage.Members {
		dep, err := bindings.ProjectReleaseDeployment(ctx, s.store, member.DeploymentID)
		if err != nil {
			continue
		}
		payload, err := json.Marshal(map[string]string{"kind": "service_staging", "status": string(state.DeployLive), "app_id": dep.AppID, "deployment_id": dep.ID})
		if err != nil {
			continue
		}
		if err := s.notif.Notify(ctx, db.NotifyDeploymentChanged, string(payload)); err != nil {
			s.log.Warn("apid: notify service staging failed", "stage", stage.ID, "err", err)
		}
	}
}
