package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) appEnvironmentSettings(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, write bool) (state.App, state.ProjectEnvironment, *api.Problem) {
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	if environment == "" {
		return app, state.ProjectEnvironment{}, nil
	}
	if !api.ValidProjectEnvironmentSlug(environment) || app.ProjectID == "" {
		return state.App{}, state.ProjectEnvironment{}, api.ErrValidation("environment must name an environment in this app's project")
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), account.ID, app.ProjectID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return state.App{}, state.ProjectEnvironment{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Project environment not found", "the selected environment does not exist in this app's project")
	}
	if err != nil {
		return state.App{}, state.ProjectEnvironment{}, api.ErrCapacity("could not load project environment")
	}
	if write && env.Protected {
		return state.App{}, state.ProjectEnvironment{}, api.NewProblem(http.StatusConflict, api.CodeConflict, "Protected environment", "apply configuration through an approved project plan or environment promotion")
	}
	reader, ok := s.store.(state.ProjectEnvironmentWorkloadSpecReader)
	if !ok {
		return state.App{}, state.ProjectEnvironment{}, api.ErrCapacity("environment workload settings unavailable")
	}
	spec, err := reader.ProjectEnvironmentWorkloadSpec(r.Context(), account.ID, app.ProjectID, env.Slug, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		settings, err := state.MaterializeEnvironmentWorkloadSettings(r.Context(), s.store, app, env.Slug)
		if err != nil {
			return state.App{}, state.ProjectEnvironment{}, api.ErrCapacity("could not load environment workload settings")
		}
		app, err = settings.ApplyTo(app)
		if err != nil {
			return state.App{}, state.ProjectEnvironment{}, api.ErrCapacity("could not read environment workload settings")
		}
		if !write {
			w.Header().Set("X-Gregale-Workload-Revision", "0")
		}
		return app, env, nil
	}
	if err != nil {
		return state.App{}, state.ProjectEnvironment{}, api.ErrCapacity("could not load environment workload settings")
	}
	hash, err := state.WorkloadSettingsHash(spec.Settings)
	if err != nil || hash != spec.Hash {
		return state.App{}, state.ProjectEnvironment{}, api.ErrCapacity("invalid environment workload settings")
	}
	resolved, err := spec.Settings.ApplyTo(app)
	if err != nil {
		return state.App{}, state.ProjectEnvironment{}, api.ErrCapacity("could not read environment workload settings")
	}
	if !write {
		setWorkloadSpecHeaders(w, spec)
	}
	return resolved, env, nil
}

func setWorkloadSpecHeaders(w http.ResponseWriter, spec state.ProjectEnvironmentWorkloadSpec) {
	w.Header().Set("X-Gregale-Workload-Revision", strconv.FormatInt(spec.Revision, 10))
	w.Header().Set("X-Gregale-Workload-Config-Hash", spec.Hash)
}

func (s *server) updateEnvironmentAppSettings(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, environment state.ProjectEnvironment, params state.UpdateAppParams) {
	store, ok := s.store.(state.ProjectEnvironmentWorkloadSpecStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment workload settings unavailable"))
		return
	}
	var expectedRevision *int64
	if raw := r.Header.Get("If-Workload-Revision"); raw != "" {
		revision, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || revision < 0 {
			api.WriteProblem(w, api.ErrValidation("If-Workload-Revision must be a non-negative integer"))
			return
		}
		expectedRevision = &revision
	}
	spec, err := state.UpdateEnvironmentWorkloadSettings(r.Context(), store, app, environment, expectedRevision, params)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Workload settings changed", "reload the environment's settings before applying this edit"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update environment workload settings"))
		return
	}
	updated, err := spec.Settings.ApplyTo(app)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read updated workload settings"))
		return
	}
	setWorkloadSpecHeaders(w, spec)
	s.audit.Emit(r.Context(), "project_environment.workload_config_updated", &account.ID, map[string]any{
		"project_id": app.ProjectID, "app_id": app.ID, "environment": environment.Slug,
		"revision": spec.Revision, "config_hash": spec.Hash,
	})
	response := s.appResponseWithContext(r.Context(), updated, account.Plan)
	response.URL = projectEnvironmentWorkloadURL(environment.ID, app.ID)
	response.CanonicalURL = response.URL
	writeJSON(w, http.StatusOK, response)
}
