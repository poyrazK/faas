package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Legacy queue endpoints operate on app-wide production consumers. Never
// discard a requested stage and then read or mutate that production state.
func queueBindingProductionRequest(w http.ResponseWriter, r *http.Request) bool {
	return productionWorkRequest(w, r, "this endpoint manages production queues; use the project environment workload queue-bindings endpoint for stage configuration")
}

func productionQueueBindingHandler(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		if queueBindingProductionRequest(w, r) {
			next(w, r, account)
		}
	}
}

func (s *server) getProjectEnvironmentQueueBindings(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, environment, problem := s.projectEnvironmentQueuesTarget(r, account, false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.ProjectEnvironmentWorkloadSpecReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment workload settings unavailable"))
		return
	}
	bindings, spec, err := state.EnvironmentQueueBindings(r.Context(), store, app, environment.Slug)
	if spec.ID != "" {
		setWorkloadSpecHeaders(w, spec)
	}
	if errors.Is(err, state.ErrNotFound) {
		w.Header().Set("X-Gregale-Workload-Revision", "0")
		err = state.ErrProjectEnvironmentQueueCollectionUnavailable
	}
	if err != nil {
		writeEnvironmentQueueProblem(w, err, "load environment queue definitions")
		return
	}
	writeJSON(w, http.StatusOK, projectEnvironmentQueuesResponse(app, environment, spec, bindings))
}

func (s *server) replaceProjectEnvironmentQueueBindings(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, environment, problem := s.projectEnvironmentQueuesTarget(r, account, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	request, bindings, problem := decodeProjectEnvironmentQueuesRequest(r, account.Plan)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.ProjectEnvironmentWorkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment workload settings unavailable"))
		return
	}
	spec, err := state.ReplaceEnvironmentQueueBindings(r.Context(), store, app, environment.Slug, *request.ExpectedRevision, bindings)
	if err != nil {
		writeEnvironmentQueueProblem(w, err, "replace environment queue definitions")
		return
	}
	setWorkloadSpecHeaders(w, spec)
	s.audit.Emit(r.Context(), "project_environment.queue_bindings_updated", &account.ID, map[string]any{
		"project_id": app.ProjectID, "app_id": app.ID, "environment": environment.Slug,
		"revision": spec.Revision, "config_hash": spec.Hash, "binding_count": len(bindings),
	})
	writeJSON(w, http.StatusOK, projectEnvironmentQueuesResponse(app, environment, spec, spec.Settings.QueueBindings.Bindings))
}

func (s *server) projectEnvironmentQueuesTarget(r *http.Request, account state.Account, write bool) (state.App, state.ProjectEnvironment, *api.Problem) {
	environment := r.PathValue("environment")
	if !api.ValidProjectEnvironmentSlug(environment) || environment == "production" {
		return state.App{}, state.ProjectEnvironment{}, api.ErrValidation("select a registered stage; manage production through the app queue-bindings API")
	}
	if query := r.URL.Query(); query.Has("environment") || query.Has("scope") {
		return state.App{}, state.ProjectEnvironment{}, api.ErrValidation("the environment is selected by the request path")
	}
	project, err := s.store.ProjectBySlug(r.Context(), account.ID, r.PathValue("slug"))
	if errors.Is(err, state.ErrNotFound) {
		return state.App{}, state.ProjectEnvironment{}, projectNotFound(r.PathValue("slug"))
	}
	if err != nil {
		return state.App{}, state.ProjectEnvironment{}, api.ErrCapacity("load project")
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), account.ID, project.ID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return state.App{}, env, projectEnvironmentNotFound(project.Slug, environment)
	}
	if err != nil {
		return state.App{}, env, api.ErrCapacity("load project environment")
	}
	app, problem := s.projectEnvironmentRoutesWorkload(r, account, project)
	if problem != nil {
		return app, env, problem
	}
	if write && env.Protected {
		return app, env, api.NewProblem(http.StatusConflict, api.CodeConflict, "Protected environment", "apply configuration through an approved project plan or environment promotion")
	}
	return app, env, nil
}

func decodeProjectEnvironmentQueuesRequest(r *http.Request, plan api.Plan) (api.ReplaceProjectEnvironmentQueueBindingsRequest, []state.ProjectEnvironmentQueueDefinition, *api.Problem) {
	var request api.ReplaceProjectEnvironmentQueueBindingsRequest
	if err := decodeJSON(r, &request); err != nil {
		return request, nil, api.ErrValidation(err.Error())
	}
	if request.ExpectedRevision == nil || *request.ExpectedRevision < 0 || request.Bindings == nil {
		return request, nil, api.ErrValidation("expected_revision and a complete bindings list are required; use [] to remove all stage bindings")
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return request, nil, api.ErrCapacity("plan limits unavailable")
	}
	bindings := make([]state.ProjectEnvironmentQueueDefinition, 0, len(*request.Bindings))
	for _, binding := range *request.Bindings {
		if binding.Mode == "push" && !limits.TriggersAllowed {
			return request, nil, api.ErrPlanTriggersNotAllowed(plan)
		}
		retry, err := json.Marshal(binding.RetryPolicy)
		if err != nil {
			return request, nil, api.ErrValidation("invalid retry policy")
		}
		bindings = append(bindings, state.ProjectEnvironmentQueueDefinition{
			Name: binding.Name, QueueName: binding.QueueName, Mode: binding.Mode,
			WorkloadClass: state.WorkloadClass(binding.WorkloadClass), Enabled: binding.Enabled,
			MaxConcurrency: binding.MaxConcurrency, RetryPolicyJSON: retry,
		})
	}
	return request, bindings, nil
}

func projectEnvironmentQueuesResponse(app state.App, env state.ProjectEnvironment, spec state.ProjectEnvironmentWorkloadSpec, bindings []state.ProjectEnvironmentQueueDefinition) api.ProjectEnvironmentQueueBindingsResponse {
	out := api.ProjectEnvironmentQueueBindingsResponse{
		Environment: env.Slug, Workload: app.Slug, Revision: spec.Settings.QueueBindings.Revision,
		WorkloadRevision: spec.Revision, ConfigHash: spec.Hash, ActivationState: "unavailable",
		Bindings: make([]api.ProjectEnvironmentQueueBinding, 0, len(bindings)),
	}
	for _, binding := range bindings {
		var retry api.RetryPolicyDTO
		_ = json.Unmarshal(binding.RetryPolicyJSON, &retry) // validated by the state reader/write
		out.Bindings = append(out.Bindings, api.ProjectEnvironmentQueueBinding{
			Name: binding.Name, QueueName: binding.QueueName, Mode: binding.Mode,
			WorkloadClass: string(binding.WorkloadClass), Enabled: binding.Enabled,
			MaxConcurrency: binding.MaxConcurrency, RetryPolicy: retry,
		})
	}
	return out
}

func writeEnvironmentQueueProblem(w http.ResponseWriter, err error, operation string) {
	switch {
	case errors.Is(err, state.ErrProjectEnvironmentQueueCollectionUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_queue_collection_unavailable", "Stage queue collection unavailable", "initialize a complete bindings list using the current workload revision"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Workload settings changed or protected", "reload the stage workload revision before replacing its queue bindings"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid or duplicate queue definitions for the stage workload"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Workload or environment not found", "the selected stage workload no longer exists"))
	default:
		api.WriteProblem(w, api.ErrCapacity(operation))
	}
}
