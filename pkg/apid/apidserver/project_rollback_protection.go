package apidserver

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) protectProjectRollbackDeploy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("deploy"))
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("deploy must be a non-zero deploy UUID"))
		return
	}
	var req api.ProtectProjectRollbackDeployRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.TTLSeconds < 1 || req.TTLSeconds > api.ProjectRollbackProtectionMaxSeconds {
		api.WriteProblem(w, api.ErrValidation("ttl_seconds must be between 1 and 604800"))
		return
	}
	store, ok := s.store.(state.ProjectRollbackProtectionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project rollback protection is unavailable"))
		return
	}
	ctx := r.Context()
	group := id.String()
	artifacts, err := store.ProjectRollbackProtectionArtifacts(ctx, project.AccountID, project.ID, group)
	if err != nil {
		api.WriteProblem(w, projectRollbackProtectionProblem(err))
		return
	}
	apps := make(map[string]bool)
	for _, artifact := range artifacts {
		if apps[artifact.AppID] {
			continue
		}
		app, problem := s.projectRollbackApp(ctx, project, api.ProjectRollbackStep{AppID: artifact.AppID, Slug: artifact.Service})
		if problem == nil {
			_, problem = appaccess.AuthorizeRoute(r, s.store, loadOrgAuditFrom(s.audit), actorAccount(r, acct), app, "POST /v1/apps/{slug}/rollback")
		}
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		apps[artifact.AppID] = true
	}
	verified := make(map[string]bool)
	for _, artifact := range artifacts {
		if verified[artifact.Key] {
			continue
		}
		if problem := s.verifyRollbackArtifact(ctx, artifact.DeploymentID, artifact.Key); problem != nil {
			problem.Detail = fmt.Sprintf("Could not protect the artifacts for %s. %s", artifact.Service, problem.Detail)
			api.WriteProblem(w, problem)
			return
		}
		verified[artifact.Key] = true
	}
	hold, err := store.ProtectProjectRollbackDeploy(ctx, project.AccountID, project.ID, group, req.TTLSeconds, artifacts)
	if err != nil {
		api.WriteProblem(w, projectRollbackProtectionProblem(err))
		return
	}
	if s.audit != nil {
		s.audit.Emit(ctx, "project.rollback.deploy_protected", &acct.ID, projectaccess.AuditData(ctx, map[string]any{
			"project_id": project.ID, "deploy_group_id": group, "expires_at": hold.ExpiresAt, "layer_bytes": hold.LayerBytes,
		}))
	}
	writeJSON(w, http.StatusOK, hold)
}

func projectRollbackProtectionProblem(err error) *api.Problem {
	var artifact *state.ProjectRollbackProtectionArtifactError
	switch {
	case errors.As(err, &artifact):
		return api.ErrRollbackTargetUnavailable(fmt.Sprintf("Could not protect the artifacts for %s.", artifact.Service))
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Recorded deploy not found", "The deploy does not have recorded members in this project.")
	case errors.Is(err, state.ErrInvalidArgument):
		return projectRollbackChanged("the deploy has an ineligible or ambiguous recorded member")
	case errors.Is(err, state.ErrConflict):
		return projectRollbackChanged("the recorded deploy changed during verification. Retry protection")
	default:
		return api.ErrCapacity("could not protect the project rollback deploy")
	}
}
