package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261031193635 — slice 6b, rehearsal twins of a simulated branch.

// POST /v1/dev/sessions/{project}/seed deploys one workload of a simulated
// environment branch into the developer session of a rehearsal run, reusing
// the branch's build and its non-managed values. The member then registers
// with the run as any other; its prime boots it under the run's policy.
func (s *server) seedDevSessionFromEnvironmentBranch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.environmentBranchesEnabled() || !s.environmentBranchContainmentEnabled {
		api.WriteProblem(w, environmentBranchesUnavailable())
		return
	}
	store, ok := s.store.(state.EnvironmentBranchSeedStore)
	if !ok || s.notif == nil {
		api.WriteProblem(w, api.ErrCapacity("rehearsal seeding is unavailable"))
		return
	}
	var req api.SeedDevSessionFromEnvironmentBranchRequest
	if err := decodeJSON(r, &req); err != nil || !validDevWorkspaceID(req.WorkspaceID) || req.WorkspaceID == "" ||
		!api.ValidProjectSlug(req.Project) || !api.ValidProjectEnvironmentSlug(req.Environment) || !validSlug(req.Workload) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid seed request",
			"workspace_id must be 32 lowercase hexadecimal characters, and project, environment and workload must be valid slugs"))
		return
	}
	ctx := r.Context()
	member, err := s.store.AppBySlug(ctx, devSessionSlug(acct.ID, r.PathValue("project"), req.WorkspaceID))
	if err != nil || member.AccountID != acct.ID || member.PreviewOfSlug == "" || member.PreviewPrNumber != 0 {
		s.notFound(w, "no such developer session")
		return
	}
	project, err := s.store.ProjectBySlug(ctx, acct.ID, req.Project)
	if err != nil {
		api.WriteProblem(w, projectNotFound(req.Project))
		return
	}
	seeded, err := store.SeedScenarioMemberFromEnvironmentBranch(ctx, state.EnvironmentBranchSeed{
		AccountID: acct.ID, ProjectID: project.ID, Branch: req.Environment, Workload: req.Workload, MemberAppID: member.ID,
	})
	if problem := environmentBranchSeedProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if seeded.Status == state.DeploySnapshotting {
		payload, err := json.Marshal(map[string]string{"app_id": seeded.AppID, "deployment_id": seeded.ID})
		if err == nil {
			err = s.notif.Notify(ctx, db.NotifySnapshotPrime, string(payload))
		}
		if err != nil && s.log != nil {
			s.log.Warn("rehearsal seed prime notification failed; prime recovery will retry", "deployment_id", seeded.ID, "err", err)
		}
	}
	s.audit.Emit(ctx, "dev_session.seeded_from_environment_branch", &acct.ID, map[string]any{
		"app_id": member.ID, "project_id": project.ID, "environment": req.Environment, "workload": req.Workload, "deployment_id": seeded.ID,
	})
	writeJSON(w, http.StatusAccepted, api.SeedDevSessionFromEnvironmentBranchResponse{AppSlug: member.Slug, DeploymentID: seeded.ID, Status: string(seeded.Status)})
}

func environmentBranchSeedProblem(err error) *api.Problem {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrProjectEnvironmentBranchNotSimulated):
		return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchNotSimulated, "Environment branch is not simulated",
			"only a branch created with simulated egress can seed a rehearsal")
	case errors.Is(err, state.ErrProjectEnvironmentNotBranch), errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Branch workload not found",
			"the environment is not a branch of this project, or the workload has no live deployment in it")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Workload shape differs",
			"the developer session and the branch workload must be the same type")
	}
	return api.ErrCapacity("could not seed the developer session")
}
