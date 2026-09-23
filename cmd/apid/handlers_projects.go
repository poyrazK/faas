package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectEnvironmentCloner is optional on the large state.Store interface so
// narrow test embedders and external adapters remain source-compatible.
type projectEnvironmentCloner interface {
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
}

func (s *server) listProjects(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projects, err := s.store.ListProjectsForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list projects"))
		return
	}
	out := make([]api.ProjectSummaryResponse, 0, len(projects))
	for _, project := range projects {
		apps, listErr := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
		if listErr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list project workloads"))
			return
		}
		out = append(out, projectSummaryResponse(project, len(apps)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getProject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	response, problem := s.projectResponse(r, acct, project)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) updateProject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	var req api.UpdateProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.RepoFullName == nil && req.ProductionBranch == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Empty update", "repo_full_name or production_branch is required"))
		return
	}

	repo, branch, installID := project.RepoFullName, project.ProductionBranch, project.InstallID
	if req.ProductionBranch != nil {
		branch = strings.TrimSpace(*req.ProductionBranch)
		if !validProjectBranch(branch) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid production branch", "production_branch must be a valid non-empty Git ref name"))
			return
		}
	}
	if req.RepoFullName != nil {
		repo = strings.TrimSpace(*req.RepoFullName)
		if repo == "" {
			installID = 0
		} else {
			if !validProjectRepoFullName(repo) {
				api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
					"Invalid repository", "repo_full_name must be owner/name"))
				return
			}
			if repo != project.RepoFullName || installID == 0 {
				resolved, problem := s.resolveRepositoryInstallation(r.Context(), acct.ID, repo)
				if problem != nil {
					api.WriteProblem(w, problem)
					return
				}
				installID = resolved
			}
		}
	}

	updated, err := s.store.UpdateProjectBinding(r.Context(), acct.ID, project.ID, repo, branch, installID)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, projectNotFound(project.Slug))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Repository already bound", "another project already uses this repository binding"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not update project"))
		}
		return
	}
	s.audit.Emit(r.Context(), "project.updated", &acct.ID, map[string]any{
		"project_id": updated.ID, "slug": updated.Slug,
		"repo_full_name": updated.RepoFullName, "production_branch": updated.ProductionBranch,
	})
	response, problem := s.projectResponse(r, acct, updated)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) previewDeleteProject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	response, problem := s.projectResponse(r, acct, project)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	preview, problem := s.projectDeletePreview(r, acct, project, response)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *server) projectDeletePreview(r *http.Request, acct state.Account, project state.Project, response api.ProjectResponse) (api.ProjectDeletePreviewResponse, *api.Problem) {
	preview := api.ProjectDeletePreviewResponse{Project: response.ProjectSummaryResponse, Workloads: response.Workloads}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		return api.ProjectDeletePreviewResponse{}, api.ErrCapacity("could not inspect project workloads")
	}
	for _, app := range apps {
		domains, domainErr := s.store.ListDomainsForApp(r.Context(), app.ID)
		env, envErr := s.store.ListAppEnv(r.Context(), acct.ID, app.ID)
		crons, cronErr := s.store.ListCronsForApp(r.Context(), app.ID)
		if domainErr != nil || envErr != nil || cronErr != nil {
			return api.ProjectDeletePreviewResponse{}, api.ErrCapacity("could not inspect project dependencies")
		}
		preview.DomainCount += len(domains)
		preview.EnvCount += len(env)
		preview.CronCount += len(crons)
	}
	return preview, nil
}

func (s *server) deleteProject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	if err := s.store.DeleteProject(r.Context(), project.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectNotFound(project.Slug))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete project"))
		return
	}
	s.audit.Emit(r.Context(), "project.deleted", &acct.ID, map[string]any{
		"project_id": project.ID, "slug": project.Slug,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) loadProject(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Project, bool) {
	slug := r.PathValue("slug")
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, slug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectNotFound(slug))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not load project"))
		}
		return state.Project{}, false
	}
	return project, true
}

func (s *server) projectResponse(r *http.Request, acct state.Account, project state.Project) (api.ProjectResponse, *api.Problem) {
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		return api.ProjectResponse{}, api.ErrCapacity("could not list project workloads")
	}
	exclusions, err := s.store.LookupDeploymentScopeExclusions(r.Context(), acct.ID, project.ID)
	if err != nil {
		return api.ProjectResponse{}, api.ErrCapacity("could not list project exclusions")
	}
	response := api.ProjectResponse{
		ProjectSummaryResponse: projectSummaryResponse(project, len(apps)),
		Workloads:              make([]api.ProjectWorkloadResponse, 0, len(apps)),
		Exclusions:             make([]string, 0, len(exclusions)),
	}
	var latest time.Time
	for _, app := range apps {
		workload := api.ProjectWorkloadResponse{Slug: app.Slug, WorkloadName: app.WorkloadName, Status: string(app.Status)}
		deployment, deployErr := s.store.LatestDeployment(r.Context(), app.ID)
		if deployErr == nil {
			workload.DeploymentStatus = string(deployment.Status)
			workload.RolloutState = deployment.RolloutState
			if deployment.BuildID != "" {
				if build, buildErr := s.store.BuildByID(r.Context(), deployment.BuildID); buildErr == nil {
					workload.BuildStatus = string(build.Status)
				}
			}
			if deployment.CreatedAt.After(latest) {
				latest = deployment.CreatedAt
				response.LastReconciliationStatus = workload.DeploymentStatus
				response.LastBuildStatus = workload.BuildStatus
			}
		} else if !errors.Is(deployErr, state.ErrNotFound) {
			return api.ProjectResponse{}, api.ErrCapacity("could not load project deployment status")
		}
		response.Workloads = append(response.Workloads, workload)
	}
	for _, exclusion := range exclusions {
		response.Exclusions = append(response.Exclusions, exclusion.Slug)
	}
	sort.Strings(response.Exclusions)
	return response, nil
}

func projectSummaryResponse(project state.Project, workloadCount int) api.ProjectSummaryResponse {
	return api.ProjectSummaryResponse{
		ID: project.ID, Slug: project.Slug, RepoFullName: project.RepoFullName,
		ProductionBranch: project.ProductionBranch, ScanSource: string(project.ScanSource),
		WorkloadCount: workloadCount, CreatedAt: project.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: project.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func validProjectBranch(branch string) bool {
	if branch == "" || len(branch) > 255 || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.Contains(branch, "..") {
		return false
	}
	// ContainsAny takes literal characters, not a regular-expression range.
	// Check controls explicitly so hyphenated branches remain valid.
	for _, r := range branch {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return !strings.ContainsAny(branch, "~^:?*[\\")
}

func projectNotFound(slug string) *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Project not found", "no project exists with slug "+slug)
}

func (s *server) listProjectEnvironments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	environments, err := s.store.ListProjectEnvironments(r.Context(), acct.ID, project.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectNotFound(project.Slug))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not list project environments"))
		}
		return
	}
	out := make([]api.ProjectEnvironmentResponse, 0, len(environments))
	for _, environment := range environments {
		out = append(out, projectEnvironmentResponse(environment))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getProjectEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	environment, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, r.PathValue("environment"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, r.PathValue("environment")))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not load project environment"))
		}
		return
	}
	writeJSON(w, http.StatusOK, projectEnvironmentResponse(environment))
}

func (s *server) createProjectEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	req, problem := decodeCreateProjectEnvironmentRequest(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	environment, clone, err := s.persistProjectEnvironment(r, acct, project, req)
	if err != nil {
		writeCreateProjectEnvironmentError(w, project.Slug, req, acct, err)
		return
	}
	s.audit.Emit(r.Context(), "project.environment.created", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug,
		"environment_id": environment.ID, "environment_slug": environment.Slug,
		"protected": environment.Protected, "cloned_from": req.FromEnvironment,
		"variables_copied": clone.VariablesCopied, "secrets_copied": clone.SecretsCopied,
		"bindings_copied": clone.BindingsCopied,
	})
	response := projectEnvironmentResponse(environment)
	if req.FromEnvironment != "" {
		response.ClonedFrom = req.FromEnvironment
		sharedResources := []string{"domains", "policies", "routes"}
		sharedResources = append(sharedResources, clone.SharedResources...)
		response.Clone = &api.ProjectEnvironmentCloneResponse{
			ConfigurationCopied: clone.ConfigurationCopied, VariablesCopied: clone.VariablesCopied,
			SecretsCopied: clone.SecretsCopied, WorkloadsCopied: clone.WorkloadsCopied,
			BindingsCopied: clone.BindingsCopied, SharedResources: sharedResources,
		}
	}
	writeJSON(w, http.StatusCreated, response)
}

func decodeCreateProjectEnvironmentRequest(r *http.Request) (api.CreateProjectEnvironmentRequest, *api.Problem) {
	var req api.CreateProjectEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error())
	}
	req.Slug, req.FromEnvironment = strings.TrimSpace(req.Slug), strings.TrimSpace(req.FromEnvironment)
	if !api.ValidProjectEnvironmentSlug(req.Slug) {
		return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid environment slug", "slug must contain 1-33 lowercase letters, numbers, or internal hyphens")
	}
	if req.FromEnvironment != "" && (!api.ValidProjectEnvironmentSlug(req.FromEnvironment) || req.FromEnvironment == req.Slug) {
		return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid source environment", "from_environment must name a different project environment")
	}
	if req.ShareResources && req.FromEnvironment == "" {
		return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid resource sharing option", "share_resources is only valid when cloning with from_environment")
	}
	return req, nil
}

func (s *server) persistProjectEnvironment(r *http.Request, acct state.Account, project state.Project, req api.CreateProjectEnvironmentRequest) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error) {
	ctx := r.Context()
	protected := req.Protected != nil && *req.Protected
	if req.FromEnvironment == "" {
		environment, err := s.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
			AccountID: acct.ID, ProjectID: project.ID, Slug: req.Slug, Protected: protected,
		})
		return environment, state.ProjectEnvironmentCloneResult{}, err
	}
	cloner, ok := s.store.(projectEnvironmentCloner)
	if !ok {
		return state.ProjectEnvironment{}, state.ProjectEnvironmentCloneResult{}, errors.New("project environment cloning is unavailable")
	}
	if _, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, req.Slug); err == nil {
		return state.ProjectEnvironment{}, state.ProjectEnvironmentCloneResult{}, state.ErrConflict
	} else if !errors.Is(err, state.ErrNotFound) {
		return state.ProjectEnvironment{}, state.ProjectEnvironmentCloneResult{}, err
	}
	bindingPlans, err := s.planProjectEnvironmentBindingClones(ctx, acct, project, req.FromEnvironment, req.ShareResources)
	if err != nil {
		return state.ProjectEnvironment{}, state.ProjectEnvironmentCloneResult{}, err
	}
	var preparedBindingIDs []string
	preparedSecretCount := 0
	var preparedCleanup []func(context.Context) error
	if !req.ShareResources && len(bindingPlans) > 0 {
		preparedBindingIDs, preparedSecretCount, preparedCleanup, err = s.prepareIsolatedProjectEnvironmentBindings(r, acct, project, req.Slug, bindingPlans)
		if err != nil {
			return state.ProjectEnvironment{}, state.ProjectEnvironmentCloneResult{}, err
		}
	}
	clone := state.ProjectEnvironmentClone{
		AccountID: acct.ID, ProjectID: project.ID, SourceSlug: req.FromEnvironment,
		TargetSlug: req.Slug, TargetProtected: protected, ShareResources: req.ShareResources,
		ManagedBindingsPrepared:   len(preparedBindingIDs) > 0,
		PreparedManagedBindingIDs: preparedBindingIDs, PreparedManagedSecretCount: preparedSecretCount,
	}
	environment, result, err := cloner.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(acct.Plan))
	if err != nil {
		// A concurrent clone may have committed after our early existence check.
		// Its environment may now depend on these deterministic resources, so
		// never compensate them until we know the target was not created.
		if _, lookupErr := s.store.ProjectEnvironmentBySlug(context.WithoutCancel(ctx), acct.ID, project.ID, req.Slug); lookupErr == nil {
			return state.ProjectEnvironment{}, result, err
		} else if !errors.Is(lookupErr, state.ErrNotFound) {
			return state.ProjectEnvironment{}, result, &projectEnvironmentBindingCloneError{
				cause: err, cleanup: fmt.Errorf("could not determine whether the target environment committed: %w", lookupErr),
			}
		}
		if cleanupErr := cleanupProjectEnvironmentBindingClone(context.WithoutCancel(ctx), preparedCleanup); cleanupErr != nil {
			return state.ProjectEnvironment{}, result, &projectEnvironmentBindingCloneError{cause: err, cleanup: cleanupErr}
		}
		return state.ProjectEnvironment{}, result, err
	}
	if !req.ShareResources {
		result.BindingsCopied = len(preparedBindingIDs)
		return environment, result, nil
	}
	if len(bindingPlans) == 0 {
		return environment, result, nil
	}
	count, sharedKinds, bindErr := s.cloneProjectEnvironmentBindings(r, acct, req.Slug, bindingPlans)
	if bindErr != nil {
		var compensation *projectEnvironmentBindingCloneError
		if errors.As(bindErr, &compensation) && compensation.cleanup != nil {
			if s.log != nil {
				s.log.Error("project environment clone resource compensation incomplete", "project_id", project.ID, "environment", req.Slug, "err", bindErr)
			}
			return state.ProjectEnvironment{}, result, bindErr
		}
		rollbacker, supported := s.store.(interface {
			RollbackProjectEnvironmentClone(context.Context, string, string, string) error
		})
		if supported {
			if rollbackErr := rollbacker.RollbackProjectEnvironmentClone(context.WithoutCancel(ctx), acct.ID, project.ID, req.Slug); rollbackErr != nil && !errors.Is(rollbackErr, state.ErrNotFound) {
				bindErr = errors.Join(errProjectEnvironmentCloneCleanup, bindErr, fmt.Errorf("remove incomplete cloned environment: %w", rollbackErr))
			}
		} else {
			bindErr = errors.Join(errProjectEnvironmentCloneCleanup, bindErr, errors.New("incomplete cloned environment could not be rolled back by this state store"))
		}
		return state.ProjectEnvironment{}, result, bindErr
	}
	result.BindingsCopied = count
	result.SharedResources = sharedKinds
	return environment, result, nil
}

func writeCreateProjectEnvironmentError(w http.ResponseWriter, projectSlug string, req api.CreateProjectEnvironmentRequest, acct state.Account, err error) {
	var quota *state.ProjectEnvironmentCloneQuotaError
	switch {
	case errors.Is(err, errProjectEnvironmentCloneCleanup):
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Clone cleanup incomplete",
			"resource cleanup could not be confirmed; the target environment may exist. Inspect it before retrying."))
	case errors.Is(err, state.ErrProjectEnvironmentCloneManagedBindings):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Managed bindings require recreation",
			"Gregale could not recreate one or more managed bindings in isolation; retry with --share-resources only if access to the source data is intentional"))
	case errors.Is(err, errIsolatedObjectStorageCloneUnsupported):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Isolated object-storage clone unavailable",
			"the selected object-storage provider cannot copy objects between buckets; retry with --share-resources only if access to the source bucket data is intentional"))
	case errors.Is(err, managedpostgres.ErrUnavailable), errors.Is(err, managedpostgres.ErrNotFound),
		errors.Is(err, managedpostgres.ErrConflict), errors.Is(err, managedpostgres.ErrInvalid),
		errors.Is(err, managedpostgres.ErrUnsupported), errors.Is(err, managedpostgres.ErrQuotaExceeded),
		errors.Is(err, managedpostgres.ErrUsageStale):
		api.WriteProblem(w, managedPostgresManifestProblem(err, "the managed PostgreSQL environment clone could not be completed"))
	case errors.As(err, &quota) && quota.Resource == "secrets":
		api.WriteProblem(w, api.ErrPlanLimitSecrets(api.MustLimitsFor(acct.Plan), quota.Observed))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrPlanLimitEnvVars(api.MustLimitsFor(acct.Plan), quota.Observed))
	case errors.Is(err, state.ErrNotFound) && req.FromEnvironment != "":
		api.WriteProblem(w, projectEnvironmentNotFound(projectSlug, req.FromEnvironment))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, projectNotFound(projectSlug))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Environment already exists", "the project already has an environment with this slug"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not create project environment"))
	}
}

func (s *server) updateProjectEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	var req api.UpdateProjectEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Protected == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Empty update", "protected is required"))
		return
	}
	environment, err := s.store.UpdateProjectEnvironmentProtection(r.Context(), acct.ID, project.ID, r.PathValue("environment"), *req.Protected)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, r.PathValue("environment")))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not update project environment"))
		}
		return
	}
	s.audit.Emit(r.Context(), "project.environment.updated", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug,
		"environment_id": environment.ID, "environment_slug": environment.Slug,
		"protected": environment.Protected,
	})
	writeJSON(w, http.StatusOK, projectEnvironmentResponse(environment))
}

func (s *server) deleteProjectEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	environmentSlug := r.PathValue("environment")
	environment, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, environmentSlug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, environmentSlug))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not load project environment"))
		}
		return
	}
	resourceCleanup, err := s.planProjectEnvironmentManagedResourceCleanup(r.Context(), acct, project, environmentSlug)
	if err != nil {
		if errors.Is(err, managedpostgres.ErrUnavailable) || errors.Is(err, managedpostgres.ErrConflict) ||
			errors.Is(err, managedpostgres.ErrNotFound) {
			api.WriteProblem(w, managedPostgresManifestProblem(err, "managed resources could not be inspected; the environment was not deleted"))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not inspect environment resources; the environment was not deleted"))
		}
		return
	}
	cleanupResources := projectEnvironmentCleanupResources(resourceCleanup)
	var cleanupJob state.ProjectEnvironmentCleanupJob
	var cleanupStore state.ProjectEnvironmentCleanupStore
	if !cleanupResources.Empty() {
		var supported bool
		cleanupStore, supported = s.store.(state.ProjectEnvironmentCleanupStore)
		if !supported {
			api.WriteProblem(w, api.ErrCapacity("durable managed-resource cleanup is unavailable; the environment was not deleted"))
			return
		}
		cleanupJob, err = cleanupStore.DeleteProjectEnvironmentWithCleanup(
			r.Context(), acct.ID, project.ID, environmentSlug, cleanupResources,
			uuid.NewString(), projectEnvironmentCleanupLeaseDuration,
		)
	} else {
		err = s.store.DeleteProjectEnvironment(r.Context(), acct.ID, project.ID, environmentSlug)
	}
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, environmentSlug))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Project environment cannot be deleted",
				"only unprotected, non-production environments without live releases can be deleted"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not delete project environment"))
		}
		return
	}
	s.audit.Emit(r.Context(), "project.environment.deleted", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug,
		"environment_id": environment.ID, "environment_slug": environment.Slug,
	})
	if cleanupJob.ID != "" {
		cleanupErr := s.cleanupProjectEnvironmentManagedResourcePayload(context.WithoutCancel(r.Context()), acct, cleanupJob.Resources)
		if cleanupErr == nil {
			cleanupErr = cleanupStore.CompleteProjectEnvironmentCleanup(context.WithoutCancel(r.Context()), cleanupJob.ID, cleanupJob.LeaseToken)
		} else {
			retryErr := cleanupStore.RetryProjectEnvironmentCleanup(
				context.WithoutCancel(r.Context()), cleanupJob.ID, cleanupJob.LeaseToken,
				time.Now().UTC().Add(projectEnvironmentCleanupRetryDelay(cleanupJob.AttemptCount+1)),
			)
			cleanupErr = errors.Join(cleanupErr, retryErr)
		}
		if cleanupErr != nil {
			if s.log != nil {
				s.log.Error("project environment managed resource cleanup incomplete", "project_id", project.ID, "environment", environmentSlug, "err", cleanupErr)
			}
			s.audit.Emit(context.WithoutCancel(r.Context()), "project.environment.resource_cleanup_failed", &acct.ID, map[string]any{
				"project_id": project.ID, "project_slug": project.Slug, "environment_slug": environmentSlug,
			})
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func projectEnvironmentResponse(environment state.ProjectEnvironment) api.ProjectEnvironmentResponse {
	return api.ProjectEnvironmentResponse{
		ID: environment.ID, ProjectID: environment.ProjectID, Slug: environment.Slug,
		Protected: environment.Protected,
		CreatedAt: environment.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: environment.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func projectEnvironmentNotFound(projectSlug, environmentSlug string) *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Project environment not found", "no environment "+environmentSlug+" exists in project "+projectSlug)
}
