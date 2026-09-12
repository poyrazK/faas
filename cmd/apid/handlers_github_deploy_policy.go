package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type githubDeployPolicyResponse struct {
	ProjectID       string   `json:"project_id"`
	RootDir         string   `json:"root_dir"`
	IgnoredPaths    []string `json:"ignored_paths"`
	PreviewEnabled  bool     `json:"preview_enabled"`
	PreviewTTLHours int      `json:"preview_ttl_hours"`
}

type githubDeployPolicyPatch struct {
	RootDir         *string   `json:"root_dir,omitempty"`
	IgnoredPaths    *[]string `json:"ignored_paths,omitempty"`
	PreviewEnabled  *bool     `json:"preview_enabled,omitempty"`
	PreviewTTLHours *int      `json:"preview_ttl_hours,omitempty"`
}

func githubDeployPolicyDTO(policy state.GitHubDeployPolicy) githubDeployPolicyResponse {
	return githubDeployPolicyResponse{
		ProjectID:       policy.ProjectID,
		RootDir:         policy.RootDir,
		IgnoredPaths:    append([]string(nil), policy.IgnoredPaths...),
		PreviewEnabled:  policy.PreviewEnabled,
		PreviewTTLHours: policy.PreviewTTLHours,
	}
}

func (s *server) githubDeployPolicyForApp(r *http.Request, app state.App, acct state.Account) (state.GitHubDeployPolicy, *api.Problem) {
	if app.ProjectID == "" {
		return state.GitHubDeployPolicy{}, api.NewProblem(http.StatusConflict, "project_required",
			"Project required", "GitHub deployment policy is available for project-backed apps only")
	}
	store, ok := s.store.(state.GitHubDeployPolicyStore)
	if !ok {
		return state.GitHubDeployPolicy{}, api.ErrCapacity("GitHub deployment policy storage is unavailable")
	}
	policy, err := store.GetGitHubDeployPolicy(r.Context(), app.ProjectID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.GitHubDeployPolicy{}, api.NewProblem(http.StatusNotFound, "not_found", "Project not found", "the app project no longer exists")
		}
		return state.GitHubDeployPolicy{}, api.ErrCapacity("could not read GitHub deployment policy")
	}
	return policy, nil
}

func (s *server) getGitHubDeployPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	policy, problem := s.githubDeployPolicyForApp(r, app, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, githubDeployPolicyDTO(policy))
}

func (s *server) patchGitHubDeployPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	current, problem := s.githubDeployPolicyForApp(r, app, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var req githubDeployPolicyPatch
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid GitHub deployment policy", err.Error()))
		return
	}
	if req.RootDir != nil {
		current.RootDir = *req.RootDir
	}
	if req.IgnoredPaths != nil {
		current.IgnoredPaths = append([]string(nil), (*req.IgnoredPaths)...)
	}
	if req.PreviewEnabled != nil {
		current.PreviewEnabled = *req.PreviewEnabled
	}
	if req.PreviewTTLHours != nil {
		current.PreviewTTLHours = *req.PreviewTTLHours
	}
	if err := current.Validate(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid GitHub deployment policy", err.Error()))
		return
	}
	store, ok := s.store.(state.GitHubDeployPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("GitHub deployment policy storage is unavailable"))
		return
	}
	stored, err := store.UpsertGitHubDeployPolicy(r.Context(), current)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "Project not found", "the app project no longer exists"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not save GitHub deployment policy"))
		return
	}
	acctID := acct.ID
	s.audit.Emit(r.Context(), "github.deploy_policy.updated", &acctID, map[string]any{
		"app_id":            app.ID,
		"project_id":        app.ProjectID,
		"root_dir":          stored.RootDir,
		"ignored_paths":     len(stored.IgnoredPaths),
		"preview_enabled":   stored.PreviewEnabled,
		"preview_ttl_hours": stored.PreviewTTLHours,
	})
	writeJSON(w, http.StatusOK, githubDeployPolicyDTO(stored))
}
