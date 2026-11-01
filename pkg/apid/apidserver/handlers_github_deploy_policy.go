package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type githubDeployPolicyResponse struct {
	ProjectID            string                     `json:"project_id"`
	RootDir              string                     `json:"root_dir"`
	IgnoredPaths         []string                   `json:"ignored_paths"`
	PreviewEnabled       bool                       `json:"preview_enabled"`
	PreviewTTLHours      int                        `json:"preview_ttl_hours"`
	PreviewBotPRs        bool                       `json:"preview_bot_prs"`
	PreviewDraftPRs      bool                       `json:"preview_draft_prs"`
	PreviewServicePolicy state.PreviewServicePolicy `json:"preview_service_policy"`
	ProductionTrigger    state.ProductionTrigger    `json:"production_trigger"`
}

type githubDeployPolicyPatch struct {
	RootDir              *string                     `json:"root_dir,omitempty"`
	IgnoredPaths         *[]string                   `json:"ignored_paths,omitempty"`
	PreviewEnabled       *bool                       `json:"preview_enabled,omitempty"`
	PreviewTTLHours      *int                        `json:"preview_ttl_hours,omitempty"`
	PreviewBotPRs        *bool                       `json:"preview_bot_prs,omitempty"`
	PreviewDraftPRs      *bool                       `json:"preview_draft_prs,omitempty"`
	PreviewServicePolicy *state.PreviewServicePolicy `json:"preview_service_policy,omitempty"`
	ProductionTrigger    *state.ProductionTrigger    `json:"production_trigger,omitempty"`
}

func githubDeployPolicyDTO(policy state.GitHubDeployPolicy) githubDeployPolicyResponse {
	return githubDeployPolicyResponse{
		ProjectID:            policy.ProjectID,
		RootDir:              policy.RootDir,
		IgnoredPaths:         append([]string(nil), policy.IgnoredPaths...),
		PreviewEnabled:       policy.PreviewEnabled,
		PreviewTTLHours:      policy.PreviewTTLHours,
		PreviewBotPRs:        policy.PreviewBotPRs,
		PreviewDraftPRs:      policy.PreviewDraftPRs,
		PreviewServicePolicy: policy.PreviewServicePolicy,
		ProductionTrigger:    policy.ProductionTrigger,
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
	if problem := applyGitHubDeployPolicyPatch(&current, req); problem != nil {
		api.WriteProblem(w, problem)
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
		"app_id":                 app.ID,
		"project_id":             app.ProjectID,
		"root_dir":               stored.RootDir,
		"ignored_paths":          len(stored.IgnoredPaths),
		"preview_enabled":        stored.PreviewEnabled,
		"preview_ttl_hours":      stored.PreviewTTLHours,
		"preview_bot_prs":        stored.PreviewBotPRs,
		"preview_draft_prs":      stored.PreviewDraftPRs,
		"preview_service_policy": stored.PreviewServicePolicy,
		"production_trigger":     stored.ProductionTrigger,
	})
	if problem := s.retireProjectPreviewsWhenOff(r.Context(), acct.ID, app.ProjectID, stored); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, githubDeployPolicyDTO(stored))
}

// retireProjectPreviewsWhenOff tears down the project's open PR previews once
// its policy turns previews off: githubd then ignores the PRs' pushes, so the
// previews would serve their last commit to the end of their lease.
// Idempotent, so a retried PATCH retires whatever is still open.
func (s *server) retireProjectPreviewsWhenOff(ctx context.Context, accountID, projectID string, policy state.GitHubDeployPolicy) *api.Problem {
	retirer, ok := s.store.(state.ProjectPRPreviewRetirer)
	if policy.PreviewEnabled || !ok {
		return nil
	}
	if _, err := retirer.RetireProjectPRPreviews(ctx, accountID, projectID); err != nil {
		return api.ErrCapacity("previews were turned off, but the open previews could not be torn down; retry")
	}
	return nil
}

// applyGitHubDeployPolicyPatch merges a partial update into current and
// validates the result.
func applyGitHubDeployPolicyPatch(current *state.GitHubDeployPolicy, req githubDeployPolicyPatch) *api.Problem {
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
	if req.PreviewBotPRs != nil {
		current.PreviewBotPRs = *req.PreviewBotPRs
	}
	if req.PreviewDraftPRs != nil {
		current.PreviewDraftPRs = *req.PreviewDraftPRs
	}
	if req.PreviewServicePolicy != nil {
		current.PreviewServicePolicy = *req.PreviewServicePolicy
	}
	if req.ProductionTrigger != nil {
		if !req.ProductionTrigger.Valid() {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid GitHub deployment policy", "production_trigger must be webhook or actions")
		}
		current.ProductionTrigger = *req.ProductionTrigger
	}
	if err := current.Validate(); err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid GitHub deployment policy", err.Error())
	}
	return nil
}
