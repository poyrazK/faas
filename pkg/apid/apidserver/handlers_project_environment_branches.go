package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Environment branches (ADR-20261031193635). Creating a branch copies the
// parent's configuration in one transaction and publishes the parent's built
// artifacts as branch deployments. Nothing boots: schedd activates each
// deployment without a VM and its first request cold boots.

func (s *server) environmentBranchesEnabled() bool {
	return s.runtimeBool(runtimeConfigEnvironmentBranches, false)
}

func environmentBranchesUnavailable() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchUnavailable,
		"Environment branches are unavailable", "environment branches are not enabled on this platform")
}

// POST /v1/projects/{slug}/environments/{environment}/branches
func (s *server) createProjectEnvironmentBranch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.environmentBranchesEnabled() {
		api.WriteProblem(w, environmentBranchesUnavailable())
		return
	}
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	if !ok || s.notif == nil {
		api.WriteProblem(w, api.ErrCapacity("environment branches are unavailable"))
		return
	}
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	parent := r.PathValue("environment")
	var req api.CreateProjectEnvironmentBranchRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid request body", "the body must be a JSON environment branch request"))
		return
	}
	if !api.ValidProjectEnvironmentSlug(req.Slug) || req.Slug == "production" || req.Slug == parent {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid environment branch",
			"slug must be a valid environment slug that differs from production and the parent"))
		return
	}
	ttl, err := api.ParseEnvironmentBranchTTL(req.TTL)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid environment branch TTL", err.Error()))
		return
	}
	egress := req.Egress
	if egress == "" {
		egress = api.EnvironmentBranchEgressLive
	}
	if problem := s.environmentBranchEgressProblem(egress); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	ctx := r.Context()
	env, branch, _, err := store.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: parent, Slug: req.Slug,
		ExpiresAt: time.Now().UTC().Add(ttl), EgressMode: egress,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits)
	if err != nil {
		api.WriteProblem(w, s.environmentBranchCreateProblem(ctx, acct, project, parent, req.Slug, limits, err))
		return
	}
	workloads, err := s.publishEnvironmentBranchWorkloads(ctx, store, acct, project, branch)
	if err != nil {
		if s.log != nil {
			s.log.Error("environment branch workloads could not be published", "project_id", project.ID, "environment", branch.Slug, "err", err)
		}
		if cleanupErr := store.DeleteProjectEnvironmentBranch(context.WithoutCancel(ctx), acct.ID, project.ID, branch.Slug); cleanupErr != nil && s.log != nil {
			s.log.Error("environment branch compensation failed; the expiry sweep will retire it", "project_id", project.ID, "environment", branch.Slug, "err", cleanupErr)
		}
		api.WriteProblem(w, api.ErrCapacity("could not publish the branch's workloads; the branch was not created"))
		return
	}
	s.audit.Emit(ctx, "project.environment.branched", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug, "environment_id": env.ID, "environment_slug": env.Slug,
		"parent_slug": branch.ParentSlug, "expires_at": branch.ExpiresAt.Format(time.RFC3339), "workloads": len(workloads),
	})
	response := projectEnvironmentResponse(env)
	response.Branch = environmentBranchResponse(branch)
	response.Branch.Workloads = workloads
	writeJSON(w, http.StatusCreated, response)
}

// publishEnvironmentBranchWorkloads creates one branch deployment per parent
// workload and hands each to schedd's prime, which activates it without a
// boot. A lost notification is recovered by schedd's prime recovery, which
// re-primes deployments left in snapshotting.
func (s *server) publishEnvironmentBranchWorkloads(ctx context.Context, store state.ProjectEnvironmentBranchStore, acct state.Account, project state.Project, branch state.ProjectEnvironmentBranch) ([]api.ProjectEnvironmentBranchWorkload, error) {
	parents, err := store.ProjectEnvironmentBranchWorkloads(ctx, acct.ID, project.ID, branch.Slug)
	if err != nil {
		return nil, err
	}
	workloads := make([]api.ProjectEnvironmentBranchWorkload, 0, len(parents))
	for _, parent := range parents {
		d, err := store.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, branch.Slug, parent.AppID)
		if err != nil {
			return nil, fmt.Errorf("branch workload %q: %w", parent.WorkloadSlug, err)
		}
		// With data pending, the branch data reconciler primes the deployment
		// once the database copies exist.
		if branch.DataState == state.EnvironmentBranchDataReady {
			payload, err := json.Marshal(map[string]string{"app_id": d.AppID, "deployment_id": d.ID})
			if err != nil {
				return nil, err
			}
			if err := s.notif.Notify(ctx, db.NotifySnapshotPrime, string(payload)); err != nil && s.log != nil {
				s.log.Warn("environment branch activation notification failed; prime recovery will retry", "deployment_id", d.ID, "err", err)
			}
		}
		workloads = append(workloads, api.ProjectEnvironmentBranchWorkload{
			Workload: parent.WorkloadSlug, DeploymentID: d.ID, SourceDeployment: parent.SourceDeploymentID, Status: string(d.Status),
		})
	}
	return workloads, nil
}

// environmentBranchPrivateNetworkProblem answers both sides of the rule
// that branches and private networks do not share a project yet
// (ADR-20261031193635 section 6): a private-network attachment is per app,
// and every instance of the app would join the network, a branch's too.
func environmentBranchPrivateNetworkProblem() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchPrivateNetwork,
		"Environment branches cannot use private networks yet",
		"a project cannot have environment branches while one of its workloads is attached to a private network: detach the workload, or delete the project's branches first")
}

// environmentBranchesBlockPrivateNetwork refuses attaching a project
// workload while the project has environment branches. The store refuses the
// same race-free; this answers before a network address is reserved.
func (s *server) environmentBranchesBlockPrivateNetwork(ctx context.Context, acct state.Account, app state.App) *api.Problem {
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	if !ok || app.ProjectID == "" {
		return nil
	}
	branches, err := store.ListProjectEnvironmentBranches(ctx, acct.ID, app.ProjectID)
	if err != nil {
		return api.ErrCapacity("could not check the project's environment branches")
	}
	if len(branches) > 0 {
		return environmentBranchPrivateNetworkProblem()
	}
	return nil
}

func (s *server) environmentBranchCreateProblem(ctx context.Context, acct state.Account, project state.Project, parent, slug string, limits api.Limits, err error) *api.Problem {
	var managed *state.ProjectEnvironmentCloneManagedBindingsError
	var quota *state.ProjectEnvironmentCloneQuotaError
	switch {
	case errors.Is(err, state.ErrProjectEnvironmentBranchPrivateNetwork):
		return environmentBranchPrivateNetworkProblem()
	case errors.Is(err, state.ErrProjectEnvironmentBranchContainedObjects):
		return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchContainedObjects,
			"Contained environment branch cannot use object storage",
			"the parent environment binds object storage; contained branches reach only their branch's services, and overlay buckets are served by the public S3 gateway. Create the branch with live egress.")
	case errors.As(err, &managed):
		return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchManagedData,
			"Environment branch cannot copy the parent's managed data",
			fmt.Sprintf("the parent environment binds %d managed secret(s) that environment branches cannot copy", managed.ManagedSecretCount))
	case errors.As(err, &quota):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Environment branch exceeds a configuration quota",
			fmt.Sprintf("workload %s would hold %d %s; the plan allows %d", quota.WorkloadSlug, quota.Observed, quota.Resource, quota.Limit))
	case errors.Is(err, state.ErrProjectEnvironmentBranchLimit):
		observed := limits.EnvironmentBranches
		if store, ok := s.store.(state.ProjectEnvironmentBranchStore); ok {
			if listed, listErr := store.ListProjectEnvironmentBranches(ctx, acct.ID, project.ID); listErr == nil && len(listed) > observed {
				observed = len(listed)
			}
		}
		return api.NewProblem(http.StatusForbidden, api.CodePlanLimitApps, "Environment branch limit reached",
			fmt.Sprintf("%s plan allows %d live environment branch(es). Delete one with `gregale environment delete`.", limits.Plan, limits.EnvironmentBranches)).
			WithLimit(int64(limits.EnvironmentBranches), int64(observed))
	case errors.Is(err, state.ErrProjectEnvironmentBranchDepth):
		return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchDepth, "Environment branch is too deep",
			fmt.Sprintf("a branch of a branch may go %d levels deep", api.EnvironmentBranchMaxDepth))
	case errors.Is(err, state.ErrInvalidArgument):
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid environment branch", "the branch request is invalid")
	case errors.Is(err, state.ErrNotFound):
		return projectEnvironmentNotFound(project.Slug, parent)
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Environment already exists",
			"environment "+slug+" already exists or is reserved in project "+project.Slug)
	default:
		if s.log != nil {
			s.log.Error("environment branch create failed", "project_id", project.ID, "parent", parent, "slug", slug, "err", err)
		}
		return api.ErrCapacity("could not create the environment branch")
	}
}

// GET /v1/projects/{slug}/environments/{environment}/branches lists the
// direct branches of an environment.
func (s *server) listProjectEnvironmentBranches(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.environmentBranchesEnabled() {
		api.WriteProblem(w, environmentBranchesUnavailable())
		return
	}
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment branches are unavailable"))
		return
	}
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	parent := r.PathValue("environment")
	if _, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, parent); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, parent))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not load project environment"))
		}
		return
	}
	branches, err := store.ListProjectEnvironmentBranches(r.Context(), acct.ID, project.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list environment branches"))
		return
	}
	out := api.ProjectEnvironmentBranchListResponse{Branches: []api.ProjectEnvironmentBranchSummary{}}
	for _, b := range branches {
		if b.ParentSlug != parent {
			continue
		}
		out.Branches = append(out.Branches, api.ProjectEnvironmentBranchSummary{
			Slug: b.Slug, Parent: b.ParentSlug, BranchPoint: b.BranchPoint.Format(time.RFC3339Nano),
			ExpiresAt: b.ExpiresAt.Format(time.RFC3339Nano), Egress: b.EgressMode, Depth: b.Depth,
			Data: b.DataState, DataError: b.DataError, CreatedAt: b.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func environmentBranchResponse(b state.ProjectEnvironmentBranch) *api.ProjectEnvironmentBranchResponse {
	return &api.ProjectEnvironmentBranchResponse{
		Parent: b.ParentSlug, BranchPoint: b.BranchPoint.Format(time.RFC3339Nano),
		ExpiresAt: b.ExpiresAt.Format(time.RFC3339Nano), Egress: b.EgressMode, Depth: b.Depth,
		Data: b.DataState, DataError: b.DataError,
	}
}

// deleteProjectEnvironmentBranch retires a branch's deployments and deletes
// it. It reports whether the environment was a branch at all, so the
// ordinary delete handles everything else.
func (s *server) deleteProjectEnvironmentBranch(w http.ResponseWriter, r *http.Request, acct state.Account, project state.Project, environment state.ProjectEnvironment) bool {
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	if !ok {
		return false
	}
	if _, err := store.ProjectEnvironmentBranchBySlug(r.Context(), acct.ID, project.ID, environment.Slug); errors.Is(err, state.ErrNotFound) {
		return false
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load project environment"))
		return true
	}
	if err := store.DeleteProjectEnvironmentBranch(r.Context(), acct.ID, project.ID, environment.Slug); err != nil {
		switch {
		case errors.Is(err, state.ErrProjectEnvironmentBranchChildren):
			api.WriteProblem(w, environmentHasBranchesProblem(environment.Slug))
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, environment.Slug))
		case errors.Is(err, state.ErrEnvironmentInvocationWorkBusy):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_work_busy",
				"Environment work is running", "wait for running work to finish or for expired claims to be released before deleting the environment"))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Environment branch cannot be deleted", "the branch is protected or changed while it was being deleted"))
		default:
			if s.log != nil {
				s.log.Error("environment branch delete failed", "project_id", project.ID, "environment", environment.Slug, "err", err)
			}
			api.WriteProblem(w, api.ErrCapacity("could not delete the environment branch"))
		}
		return true
	}
	s.audit.Emit(r.Context(), "project.environment.deleted", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug,
		"environment_id": environment.ID, "environment_slug": environment.Slug, "branch": true,
	})
	w.WriteHeader(http.StatusNoContent)
	return true
}

func environmentHasBranchesProblem(slug string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchHasBranches, "Environment has branches",
		"delete the branches of "+slug+" before deleting it")
}

// environmentBranchEgressProblem admits live egress, contained egress once
// the operator gate is on (slice 6a), and simulated egress once rehearsal
// containment and simulation are on too (slice 6b): its twins rehearse
// through them.
func (s *server) environmentBranchEgressProblem(egress string) *api.Problem {
	switch {
	case egress == api.EnvironmentBranchEgressLive:
		return nil
	case egress != api.EnvironmentBranchEgressContained && egress != api.EnvironmentBranchEgressSimulated:
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid environment branch egress",
			"egress must be live, contained or simulated")
	case !s.environmentBranchContainmentEnabled:
		return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchUnavailable, "Contained environment branches are unavailable",
			egress+" egress is not enabled on this platform; create the branch with live egress")
	case egress == api.EnvironmentBranchEgressSimulated && !(s.rehearsalNetworkEnabled && s.rehearsalSimulationEnabled):
		return api.NewProblem(http.StatusConflict, api.CodeEnvironmentBranchUnavailable, "Simulated environment branches are unavailable",
			"simulated egress needs rehearsal containment and action simulation, which are not enabled on this platform")
	}
	return nil
}
