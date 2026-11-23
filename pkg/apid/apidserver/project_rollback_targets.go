package apidserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func normalizeProjectRollbackTarget(req *api.ProjectRollbackRequest) *api.Problem {
	if req.ToDeploy == "" {
		return nil
	}
	if req.All {
		return api.ErrValidation("to_deploy and all cannot be combined")
	}
	id, err := uuid.Parse(req.ToDeploy)
	if err != nil || id == uuid.Nil {
		return api.ErrValidation("to_deploy must be a non-zero deploy UUID")
	}
	req.ToDeploy = id.String()
	return nil
}

func (s *server) listProjectRollbackTargets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.ProjectRollbackTargetsStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project rollback targets are unavailable"))
		return
	}
	deploys, err := store.ListProjectRollbackTargets(r.Context(), project.AccountID, project.ID)
	if err != nil {
		s.log.Warn("list project rollback targets", "project", project.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not read project rollback targets"))
		return
	}
	writeJSON(w, http.StatusOK, api.ProjectRollbackTargets{
		Project: project.Slug, ProjectID: project.ID, Deploys: deploys,
	})
}

func (s *server) projectRollbackBatch(ctx context.Context, project state.Project, group string) (*api.ProjectRollbackBatch, *api.Problem) {
	var batch *api.ProjectRollbackBatch
	var err error
	if group != "" {
		catalog, ok := s.store.(state.ProjectRollbackTargetsStore)
		if !ok {
			return nil, api.ErrCapacity("project rollback targets are unavailable")
		}
		batch, err = catalog.ProjectRollbackBatchByID(ctx, project.AccountID, project.ID, group)
	} else {
		catalog, ok := s.store.(state.ProjectDeployRollbackStore)
		if !ok {
			return nil, api.ErrCapacity("project deployment membership is unavailable")
		}
		batch, err = catalog.LatestProjectRollbackBatch(ctx, project.AccountID, project.ID)
	}
	if err != nil {
		return nil, api.ErrCapacity("could not read project deployment membership")
	}
	if batch == nil || batch.ID == "" || len(batch.Members) == 0 {
		if group != "" {
			return nil, projectRollbackChanged("the selected deploy has no recorded members in this project. Use --targets to find a deploy")
		}
		return nil, projectRollbackChanged("no serving deploy has recorded project membership. Select individual app revisions or explicitly use --all")
	}
	return batch, nil
}

func (s *server) planProjectRollbackTargetStep(r *http.Request, app state.App, member api.ProjectRollbackMember, plan *state.ProjectRollbackRecord, step *api.ProjectRollbackStep) *api.Problem {
	step.TargetID, step.To = member.DeploymentID, member.Revision
	current, problem := s.projectRollbackCurrent(r.Context(), app.ID)
	if problem != nil {
		return problem
	}
	step.From, step.CurrentID, step.Scope, step.CurrentServedAt = current.Revision, current.ID, current.Scope, current.RolloutCompletedAt
	if !member.Restorable {
		return api.ErrRollbackTargetIneligible(fmt.Sprintf("recorded revision v%d must have served production and still be available to restore", member.Revision))
	}
	if current.ID == "" {
		return projectRollbackChanged("the service has no completed serving revision to replace")
	}
	if current.ID == member.DeploymentID {
		step.Status, step.Reason = "skipped", "selected revision already serves"
		return nil
	}
	return s.checkProjectRollbackPlannedStep(r, app, plan, step)
}
