package main

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

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
	var req api.CreateProjectEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	if !api.ValidProjectEnvironmentSlug(req.Slug) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid environment slug", "slug must contain 1-33 lowercase letters, numbers, or internal hyphens"))
		return
	}
	protected := false
	if req.Protected != nil {
		protected = *req.Protected
	}
	environment, err := s.store.CreateProjectEnvironment(r.Context(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: req.Slug, Protected: protected,
	})
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, projectNotFound(project.Slug))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Environment already exists", "the project already has an environment with this slug"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not create project environment"))
		}
		return
	}
	s.audit.Emit(r.Context(), "project.environment.created", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug,
		"environment_id": environment.ID, "environment_slug": environment.Slug,
		"protected": environment.Protected,
	})
	writeJSON(w, http.StatusCreated, projectEnvironmentResponse(environment))
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
	if err := s.store.DeleteProjectEnvironment(r.Context(), acct.ID, project.ID, environmentSlug); err != nil {
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
