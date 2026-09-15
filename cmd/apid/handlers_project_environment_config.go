package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getProjectEnvironmentConfig(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, environment, config, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, r.PathValue("slug"), r.PathValue("environment"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, projectEnvironmentConfigResponse(project.Slug, environment.Slug, config))
}

func (s *server) updateProjectEnvironmentConfig(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, environment, _, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, r.PathValue("slug"), r.PathValue("environment"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var req api.UpdateProjectEnvironmentConfigRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	values, hash, err := api.NormalizeProjectEnvironmentConfig(req.Values)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid environment configuration", err.Error()))
		return
	}
	config, err := s.store.CreateProjectEnvironmentConfigVersion(r.Context(), state.ProjectEnvironmentConfig{
		AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: environment.Slug,
		ConfigHash: hash, Values: values,
	})
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, environment.Slug))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not store environment configuration"))
		}
		return
	}
	s.audit.Emit(r.Context(), "project.environment.configured", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug,
		"environment_id": environment.ID, "environment_slug": environment.Slug,
		"version": config.Version, "config_hash": config.ConfigHash,
	})
	writeJSON(w, http.StatusOK, projectEnvironmentConfigResponse(project.Slug, environment.Slug, config))
}

func (s *server) diffProjectEnvironmentConfig(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := r.PathValue("slug")
	toEnvironment := r.PathValue("environment")
	fromEnvironment := strings.TrimSpace(r.URL.Query().Get("from"))
	if fromEnvironment == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Source environment required", "from is required"))
		return
	}
	project, to, toConfig, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, projectSlug, toEnvironment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	_, from, fromConfig, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, projectSlug, fromEnvironment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	changes, err := projectEnvironmentConfigDiff(fromConfig.Values, toConfig.Values)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not compare environment configurations"))
		return
	}
	writeJSON(w, http.StatusOK, api.ProjectEnvironmentConfigDiffResponse{
		ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: to.Slug,
		FromVersion: fromConfig.Version, ToVersion: toConfig.Version,
		FromHash: configHashOrEmpty(fromConfig), ToHash: configHashOrEmpty(toConfig), Changes: changes,
	})
}

func (s *server) loadProjectEnvironmentConfig(ctx context.Context, acct state.Account, projectSlug, environmentSlug string) (state.Project, state.ProjectEnvironment, state.ProjectEnvironmentConfig, *api.Problem) {
	project, err := s.store.ProjectBySlug(ctx, acct.ID, projectSlug)
	if errors.Is(err, state.ErrNotFound) {
		return state.Project{}, state.ProjectEnvironment{}, state.ProjectEnvironmentConfig{}, projectNotFound(projectSlug)
	}
	if err != nil {
		return state.Project{}, state.ProjectEnvironment{}, state.ProjectEnvironmentConfig{}, api.ErrCapacity("could not load project")
	}
	environment, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, environmentSlug)
	if errors.Is(err, state.ErrNotFound) {
		return state.Project{}, state.ProjectEnvironment{}, state.ProjectEnvironmentConfig{}, projectEnvironmentNotFound(projectSlug, environmentSlug)
	}
	if err != nil {
		return state.Project{}, state.ProjectEnvironment{}, state.ProjectEnvironmentConfig{}, api.ErrCapacity("could not load project environment")
	}
	config, err := s.store.ProjectEnvironmentConfigLatest(ctx, acct.ID, project.ID, environment.Slug)
	if errors.Is(err, state.ErrNotFound) {
		return project, environment, state.ProjectEnvironmentConfig{}, nil
	}
	if err != nil {
		return state.Project{}, state.ProjectEnvironment{}, state.ProjectEnvironmentConfig{}, api.ErrCapacity("could not load environment configuration")
	}
	return project, environment, config, nil
}

func projectEnvironmentConfigResponse(projectSlug, environmentSlug string, config state.ProjectEnvironmentConfig) api.ProjectEnvironmentConfigResponse {
	values := config.Values
	if len(values) == 0 {
		values = json.RawMessage(`{}`)
	}
	return api.ProjectEnvironmentConfigResponse{
		ProjectSlug: projectSlug, Environment: environmentSlug, Version: config.Version,
		ConfigHash: configHashOrEmpty(config), Values: values,
		UpdatedAt: func() string {
			if config.CreatedAt.IsZero() {
				return ""
			}
			return config.CreatedAt.UTC().Format(time.RFC3339Nano)
		}(),
	}
}

func configHashOrEmpty(config state.ProjectEnvironmentConfig) string {
	if config.ConfigHash != "" {
		return config.ConfigHash
	}
	return api.EmptyProjectEnvironmentConfigHash()
}

func projectEnvironmentConfigDiff(beforeRaw, afterRaw json.RawMessage) ([]api.ProjectEnvironmentConfigChange, error) {
	before := map[string]json.RawMessage{}
	after := map[string]json.RawMessage{}
	if len(beforeRaw) > 0 && !bytes.Equal(bytes.TrimSpace(beforeRaw), []byte("null")) {
		if err := json.Unmarshal(beforeRaw, &before); err != nil {
			return nil, err
		}
	}
	if len(afterRaw) > 0 && !bytes.Equal(bytes.TrimSpace(afterRaw), []byte("null")) {
		if err := json.Unmarshal(afterRaw, &after); err != nil {
			return nil, err
		}
	}
	keys := make(map[string]struct{}, len(before)+len(after))
	for key := range before {
		keys[key] = struct{}{}
	}
	for key := range after {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	changes := make([]api.ProjectEnvironmentConfigChange, 0, len(ordered))
	for _, key := range ordered {
		oldValue, hadBefore := before[key]
		newValue, hadAfter := after[key]
		if hadBefore && hadAfter && bytes.Equal(oldValue, newValue) {
			continue
		}
		change := api.ProjectEnvironmentConfigChange{Key: key, Before: oldValue, After: newValue}
		switch {
		case !hadBefore:
			change.Kind = "added"
		case !hadAfter:
			change.Kind = "removed"
		default:
			change.Kind = "changed"
		}
		changes = append(changes, change)
	}
	return changes, nil
}
