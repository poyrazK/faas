package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func (s *server) workPolicyEnvironment(r *http.Request, account state.Account, app state.App, write bool) (string, *api.Problem) {
	query := r.URL.Query()
	values, selected := query["environment"]
	if query.Has("scope") || (selected && (len(values) != 1 || values[0] == "")) {
		return "", api.ErrValidation("select exactly one environment with ?environment=<name>")
	}
	if !selected {
		return "", nil
	}
	environment := values[0]
	if app.ProjectID == "" || !api.ValidProjectEnvironmentSlug(environment) {
		return "", api.ErrValidation("environment must name an environment in this app's project")
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), account.ID, app.ProjectID, environment)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Project environment not found", "the selected environment does not exist in this app's project")
		}
		return "", api.ErrCapacity("load project environment")
	}
	if write && env.Protected {
		return "", api.NewProblem(http.StatusConflict, api.CodeConflict, "Protected environment", "apply configuration through an approved project plan or environment promotion")
	}
	if environment == "production" {
		return "", nil
	}
	return environment, nil
}

func workPolicyExpectedRevision(r *http.Request) (*int64, *api.Problem) {
	raw := r.Header.Get("If-Workload-Revision")
	if raw == "" {
		return nil, nil
	}
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision < 0 {
		return nil, api.ErrValidation("If-Workload-Revision must be a non-negative integer")
	}
	return &revision, nil
}

func writeWorkPolicyProblem(w http.ResponseWriter, err error, operation string) {
	switch {
	case errors.Is(err, state.ErrProjectEnvironmentWorkPolicyCollectionUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_work_policy_collection_unavailable", "Stage policy collection unavailable", "initialize an explicit environment policy collection before reading or deleting it"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Workload settings changed or protected", "reload the environment's settings before applying this edit"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid environment work policy"))
	case errors.Is(err, state.ErrQuotaExceeded):
		api.WriteProblem(w, api.ErrValidation("maximum work policies per app reached"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Work policy not found", "the selected app or environment has no work policy with this name"))
	default:
		api.WriteProblem(w, api.ErrCapacity(operation))
	}
}

func (s *server) upsertEnvironmentWorkPolicy(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, environment string, policy workpolicy.Policy) {
	store, ok := s.store.(state.ProjectEnvironmentWorkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment workload settings unavailable"))
		return
	}
	expected, problem := workPolicyExpectedRevision(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	record, spec, err := state.UpsertEnvironmentWorkPolicy(r.Context(), store, app, environment, expected, policy)
	if err != nil {
		writeWorkPolicyProblem(w, err, "save environment work policy")
		return
	}
	setWorkloadSpecHeaders(w, spec)
	s.auditEnvironmentWorkPolicies(r, account, app, environment, spec)
	writeJSON(w, http.StatusOK, workPolicyResponse(record))
}

func (s *server) listEnvironmentWorkPolicies(w http.ResponseWriter, r *http.Request, app state.App, environment string) {
	store, ok := s.store.(state.ProjectEnvironmentWorkloadSpecReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment workload settings unavailable"))
		return
	}
	records, spec, err := state.EnvironmentWorkPolicies(r.Context(), store, app, environment)
	if errors.Is(err, state.ErrNotFound) {
		err = state.ErrProjectEnvironmentWorkPolicyCollectionUnavailable
	}
	if err != nil {
		writeWorkPolicyProblem(w, err, "list environment work policies")
		return
	}
	setWorkloadSpecHeaders(w, spec)
	out := api.WorkPolicyListResponse{Policies: make([]api.WorkPolicyResponse, 0, len(records))}
	for _, record := range records {
		out.Policies = append(out.Policies, workPolicyResponse(record))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteEnvironmentWorkPolicy(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, environment string) {
	store, ok := s.store.(state.ProjectEnvironmentWorkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment workload settings unavailable"))
		return
	}
	expected, problem := workPolicyExpectedRevision(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	spec, err := state.DeleteEnvironmentWorkPolicy(r.Context(), store, app, environment, r.PathValue("name"), expected)
	if err != nil {
		writeWorkPolicyProblem(w, err, "delete environment work policy")
		return
	}
	setWorkloadSpecHeaders(w, spec)
	s.auditEnvironmentWorkPolicies(r, account, app, environment, spec)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) auditEnvironmentWorkPolicies(r *http.Request, account state.Account, app state.App, environment string, spec state.ProjectEnvironmentWorkloadSpec) {
	s.audit.Emit(r.Context(), "project_environment.work_policies_updated", &account.ID, map[string]any{
		"project_id": app.ProjectID, "app_id": app.ID, "environment": environment,
		"revision": spec.Revision, "config_hash": spec.Hash,
	})
}
