package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Environment branch lifecycle (ADR-20261031193635, amended 2026-11-12): a
// branch reports its state, moves its expiry, and is made again from its
// parent. The path names the branch itself:
// /v1/projects/{slug}/environments/{environment}/branch.

// environmentBranchTarget loads the branch the path names. It writes the
// problem and reports false when there is none.
func (s *server) environmentBranchTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProjectEnvironmentBranchStore, state.Project, state.ProjectEnvironmentBranch, bool) {
	if !s.environmentBranchesEnabled() {
		api.WriteProblem(w, environmentBranchesUnavailable())
		return nil, state.Project{}, state.ProjectEnvironmentBranch{}, false
	}
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment branches are unavailable"))
		return nil, state.Project{}, state.ProjectEnvironmentBranch{}, false
	}
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return nil, state.Project{}, state.ProjectEnvironmentBranch{}, false
	}
	slug := r.PathValue("environment")
	branch, err := store.ProjectEnvironmentBranchBySlug(r.Context(), acct.ID, project.ID, slug)
	switch {
	case errors.Is(err, state.ErrProjectEnvironmentNotBranch):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not an environment branch",
			"environment "+slug+" in project "+project.Slug+" is not a branch"))
		return nil, state.Project{}, state.ProjectEnvironmentBranch{}, false
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, slug))
		return nil, state.Project{}, state.ProjectEnvironmentBranch{}, false
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not load the environment branch"))
		return nil, state.Project{}, state.ProjectEnvironmentBranch{}, false
	}
	return store, project, branch, true
}

// GET /v1/projects/{slug}/environments/{environment}/branch
func (s *server) getProjectEnvironmentBranch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, project, branch, ok := s.environmentBranchTarget(w, r, acct)
	if !ok {
		return
	}
	ctx := r.Context()
	env, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, branch.Slug)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the environment branch"))
		return
	}
	response := projectEnvironmentResponse(env)
	response.Branch = environmentBranchResponse(branch)
	response.Branch.CreatedAt = branch.CreatedAt.Format(time.RFC3339Nano)
	if response.Branch.Databases, err = s.environmentBranchDatabases(ctx, branch); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list the branch's database copies"))
		return
	}
	if response.Branch.Workloads, err = s.environmentBranchWorkloadStatus(ctx, store, acct, project, branch); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list the branch's workloads"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) environmentBranchDatabases(ctx context.Context, branch state.ProjectEnvironmentBranch) ([]api.ProjectEnvironmentBranchDatabase, error) {
	data, ok := s.store.(state.ProjectEnvironmentBranchDataStore)
	if !ok {
		return nil, nil
	}
	rows, err := data.ListProjectEnvironmentBranchDatabases(ctx, branch.EnvironmentID)
	if err != nil {
		return nil, err
	}
	out := make([]api.ProjectEnvironmentBranchDatabase, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.ProjectEnvironmentBranchDatabase{SourceDatabaseID: row.SourceDatabaseID, Name: row.CopyName, CopyDatabaseID: row.CopyDatabaseID})
	}
	return out, nil
}

// environmentBranchWorkloadStatus lists the branch's workloads: each one the
// parent serves, with its URL in the branch and its deployment there. A
// workload with no live deployment in the branch is starting (it has not
// been activated yet).
func (s *server) environmentBranchWorkloadStatus(ctx context.Context, store state.ProjectEnvironmentBranchStore, acct state.Account, project state.Project, branch state.ProjectEnvironmentBranch) ([]api.ProjectEnvironmentBranchWorkload, error) {
	parents, err := store.ProjectEnvironmentBranchWorkloads(ctx, acct.ID, project.ID, branch.Slug)
	if err != nil {
		return nil, err
	}
	out := make([]api.ProjectEnvironmentBranchWorkload, 0, len(parents))
	for _, parent := range parents {
		workload := api.ProjectEnvironmentBranchWorkload{Workload: parent.WorkloadSlug, Status: "starting"}
		workload.URL, workload.EnvironmentURL = environmentBranchWorkloadURLs(branch, parent.AppID)
		live, err := s.store.LiveDeploymentForScope(ctx, parent.AppID, branch.Slug)
		switch {
		case err == nil:
			workload.DeploymentID, workload.Status = live.ID, string(live.Status)
		case !errors.Is(err, state.ErrNotFound):
			return nil, err
		}
		out = append(out, workload)
	}
	return out, nil
}

// POST /v1/projects/{slug}/environments/{environment}/branch/extend
func (s *server) extendProjectEnvironmentBranch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, project, branch, ok := s.environmentBranchTarget(w, r, acct)
	if !ok {
		return
	}
	extender, ok := s.store.(state.ProjectEnvironmentBranchExtender)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment branches cannot be extended"))
		return
	}
	var req api.ExtendProjectEnvironmentBranchRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid request body", "the body must be a JSON environment branch extend request"))
		return
	}
	ttl, err := api.ParseEnvironmentBranchTTL(req.TTL)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid environment branch TTL", err.Error()))
		return
	}
	if branch.DataState == state.EnvironmentBranchDataPending {
		api.WriteProblem(w, environmentBranchDataPending(branch.Slug))
		return
	}
	ctx := r.Context()
	extended, problem := s.moveEnvironmentBranchExpiry(ctx, extender, acct, project, branch, time.Now().UTC().Add(ttl).Truncate(time.Second), false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	env, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, branch.Slug)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the environment branch"))
		return
	}
	response := projectEnvironmentResponse(env)
	response.Branch = environmentBranchResponse(extended)
	response.Branch.CreatedAt = extended.CreatedAt.Format(time.RFC3339Nano)
	writeJSON(w, http.StatusOK, response)
}

// moveEnvironmentBranchExpiry moves a branch's expiry, and its database
// copies' first, to expiresAt. A move past the branch's longest lifetime is
// refused, or with clamp moved to the end of that lifetime instead.
func (s *server) moveEnvironmentBranchExpiry(ctx context.Context, extender state.ProjectEnvironmentBranchExtender, acct state.Account, project state.Project, branch state.ProjectEnvironmentBranch, expiresAt time.Time, clamp bool) (state.ProjectEnvironmentBranch, *api.Problem) {
	copies, err := s.environmentBranchDatabases(ctx, branch)
	if err != nil {
		return state.ProjectEnvironmentBranch{}, api.ErrCapacity("could not list the branch's database copies")
	}
	if clamp {
		expiresAt = environmentBranchLifetimeEnd(branch, len(copies) > 0, expiresAt)
	}
	if problem := environmentBranchLifetimeProblem(branch, len(copies) > 0, expiresAt); problem != nil {
		return state.ProjectEnvironmentBranch{}, problem
	}
	// The copies move first: a copy that ended before its branch would take
	// the branch's database away while it still serves.
	for _, copy := range copies {
		if problem := s.moveEnvironmentBranchCopyExpiry(ctx, acct, branch, copy, expiresAt); problem != nil {
			return state.ProjectEnvironmentBranch{}, problem
		}
	}
	extended, err := extender.ExtendProjectEnvironmentBranch(ctx, acct.ID, project.ID, branch.Slug, expiresAt)
	switch {
	case errors.Is(err, state.ErrProjectEnvironmentBranchDataPending):
		return state.ProjectEnvironmentBranch{}, environmentBranchDataPending(branch.Slug)
	case err != nil:
		return state.ProjectEnvironmentBranch{}, api.ErrCapacity("could not extend the environment branch")
	}
	s.audit.Emit(ctx, "project.environment.branch_extended", &acct.ID, map[string]any{
		"project_id": project.ID, "environment_id": branch.EnvironmentID, "environment_slug": branch.Slug,
		"previous_expires_at": branch.ExpiresAt.Format(time.RFC3339), "expires_at": extended.ExpiresAt.Format(time.RFC3339),
		"database_copies": len(copies),
	})
	return extended, nil
}

// environmentBranchLifetimeEnd is expiresAt, or the end of the branch's
// longest lifetime when expiresAt is past it.
func environmentBranchLifetimeEnd(branch state.ProjectEnvironmentBranch, hasCopies bool, expiresAt time.Time) time.Time {
	longest := api.EnvironmentBranchMaxTTL
	if hasCopies {
		longest = api.EnvironmentBranchWithDatabasesMaxLifetime
	}
	if end := branch.CreatedAt.Add(longest).Truncate(time.Second); expiresAt.After(end) {
		return end
	}
	return expiresAt
}

func environmentBranchDataPending(slug string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchDataPending, "Environment branch data is still being copied",
		"branch "+slug+" is still copying its parent's data; extend it once its data is ready")
}

// environmentBranchLifetimeProblem refuses an expiry past the branch's
// longest lifetime: EnvironmentBranchMaxTTL after its creation, or, with
// database copies, EnvironmentBranchWithDatabasesMaxLifetime, which keeps
// the copies in the plan's PostgreSQL branch allowance.
func environmentBranchLifetimeProblem(branch state.ProjectEnvironmentBranch, hasCopies bool, expiresAt time.Time) *api.Problem {
	lifetime := api.EnvironmentBranchMaxTTL
	why := ""
	if hasCopies {
		lifetime = api.EnvironmentBranchWithDatabasesMaxLifetime
		why = " A branch with database copies lives at most 7 days, so its copies stay in the plan's PostgreSQL branch allowance."
	}
	limit := branch.CreatedAt.Add(lifetime)
	if !expiresAt.After(limit) {
		return nil
	}
	return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchLifetime, "Environment branch lifetime exceeded",
		fmt.Sprintf("branch %s was created at %s and may live until %s; the requested expiry is %s.%s",
			branch.Slug, branch.CreatedAt.Format(time.RFC3339), limit.Format(time.RFC3339), expiresAt.Format(time.RFC3339), why)).
		WithLimit(int64(lifetime/time.Second), int64(expiresAt.Sub(branch.CreatedAt)/time.Second)).
		WithHint("Reset the branch (`gregale environment branch <project> <branch> --reset`) to start a new lifetime.").
		WithDocs("https://gregale.dev/docs/environment-branches#extend-and-reset")
}

// moveEnvironmentBranchCopyExpiry ends a copy with its extended branch. A
// copy not yet made, or already being deleted, has nothing to move.
func (s *server) moveEnvironmentBranchCopyExpiry(ctx context.Context, acct state.Account, branch state.ProjectEnvironmentBranch, copy api.ProjectEnvironmentBranchDatabase, expiresAt time.Time) *api.Problem {
	if copy.CopyDatabaseID == "" {
		return nil
	}
	if s.managedPostgres == nil {
		return api.ErrCapacity("managed PostgreSQL is unavailable; the branch was not extended")
	}
	_, err := s.managedPostgres.MoveBranchExpiry(ctx, acct.ID, copy.CopyDatabaseID, expiresAt)
	switch {
	case err == nil, errors.Is(err, managedpostgres.ErrNotFound):
		return nil
	case errors.Is(err, managedpostgres.ErrConflict):
		// Deleted, or not ending on its own (a copy made before copies
		// carried a TTL): the branch's delete removes it either way.
		return nil
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		return environmentBranchLifetimeProblem(branch, true, expiresAt)
	default:
		if s.log != nil {
			s.log.Error("environment branch copy expiry could not move", "environment_id", branch.EnvironmentID, "database_id", copy.CopyDatabaseID, "err", err)
		}
		return api.ErrCapacity("could not extend the branch's database copies; the branch was not extended")
	}
}

// POST /v1/projects/{slug}/environments/{environment}/branch/reset makes the
// branch again from its parent, with its settings and its lifetime: the
// branch is deleted (its deployments retired, its copies left to the
// reconciler) and created anew with the same slot, so its workloads keep
// their URLs. Work done in the branch is discarded.
func (s *server) resetProjectEnvironmentBranch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, project, branch, ok := s.environmentBranchTarget(w, r, acct)
	if !ok {
		return
	}
	if s.notif == nil {
		api.WriteProblem(w, api.ErrCapacity("environment branches are unavailable"))
		return
	}
	if problem := s.environmentBranchParentProblem(r, acct, project, branch.ParentSlug); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	spec := environmentBranchSpec{parent: branch.ParentSlug, slug: branch.Slug, egress: branch.EgressMode,
		privateNetworks: branch.PrivateNetworks, ttl: environmentBranchResetTTL(branch)}
	if problem := s.environmentBranchEgressProblem(spec.egress, spec.privateNetworks); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	// A reset makes a new branch, so an agent's budget pays for it as for a
	// create; a refused charge leaves the old branch in place.
	commitBudget, answered := s.chargeEnvironmentClone(w, r, acct, project, branch.ParentSlug, true)
	if answered {
		return
	}
	ctx := r.Context()
	if err := store.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, branch.Slug); err != nil {
		if errors.Is(err, state.ErrProjectEnvironmentBranchChildren) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchHasBranches, "Environment branch has branches",
				"branch "+branch.Slug+" has branches of its own; delete them before resetting it"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not reset the environment branch"))
		return
	}
	response, problem := s.branchProjectEnvironment(ctx, store, acct, project, spec)
	if problem != nil {
		// The old branch is gone; say so, so the caller creates it again.
		problem.Detail = "branch " + branch.Slug + " was deleted but could not be made again: " + problem.Detail
		api.WriteProblem(w, problem)
		return
	}
	commitBudget()
	s.audit.Emit(ctx, "project.environment.branch_reset", &acct.ID, map[string]any{
		"project_id": project.ID, "environment_slug": branch.Slug, "parent_slug": branch.ParentSlug,
		"previous_environment_id": branch.EnvironmentID, "environment_id": response.ID,
	})
	writeJSON(w, http.StatusOK, response)
}

// environmentBranchResetTTL is the lifetime a reset branch gets: the one it
// was created with, in whole seconds, inside the TTL range.
func environmentBranchResetTTL(branch state.ProjectEnvironmentBranch) time.Duration {
	ttl := branch.ExpiresAt.Sub(branch.CreatedAt).Round(time.Second)
	return min(max(ttl, api.EnvironmentBranchMinTTL), api.EnvironmentBranchMaxTTL)
}
