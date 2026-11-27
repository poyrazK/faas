package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/stagealerts"
	"github.com/onebox-faas/faas/pkg/apid/stageworkflows"
	"github.com/onebox-faas/faas/pkg/state"
)

// Stage stored workflows (ADR-20261104120000 amendment 2026-11-22): the
// rules live in pkg/apid/stageworkflows; this wires them to the stored
// workflow routes.

type storedWorkflowEnvironmentKey struct{}

// storedWorkflowEnvironment is the environment a stored workflow route
// addresses ("" is production), as storedWorkflowScope resolved it.
func storedWorkflowEnvironment(ctx context.Context) string {
	id, _ := ctx.Value(storedWorkflowEnvironmentKey{}).(string)
	return id
}

// storedWorkflowScope resolves ?environment for a stored workflow route and
// refuses what stageworkflows.Check refuses. stageReady marks the routes a
// stage's workflow supports.
func (s *server) storedWorkflowScope(stageReady bool) func(accountHandler) accountHandler {
	return func(next accountHandler) accountHandler {
		return func(w http.ResponseWriter, r *http.Request, account state.Account) {
			if r.URL.Query().Get("environment") == "" {
				// Production; a stage's name is still not reachable here.
				if !s.storedWorkflowInEnvironment(w, r, account, "", stageReady) {
					return
				}
				next(w, r, account)
				return
			}
			app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
			if !ok {
				return
			}
			envID, prob := stagealerts.Environment(r.Context(), s.store, app, r.URL.Query().Get("environment"))
			if prob != nil {
				api.WriteProblem(w, prob)
				return
			}
			if !s.storedWorkflowInEnvironment(w, r, account, envID, stageReady) {
				return
			}
			next(w, r.WithContext(context.WithValue(r.Context(), storedWorkflowEnvironmentKey{}, envID)), account)
		}
	}
}

// storedWorkflowInEnvironment checks that the named workflow, if any, is
// held by envID.
func (s *server) storedWorkflowInEnvironment(w http.ResponseWriter, r *http.Request, account state.Account, envID string, stageReady bool) bool {
	req := stageworkflows.Request{Requested: envID, StageReady: stageReady, Write: r.Method != http.MethodGet,
		Open: s.runtimeBool(runtimeConfigStageStoredWorkflows, false)}
	if name := r.PathValue("name"); name != "" {
		store, ok := s.store.(state.StoredWorkflowStore)
		if !ok {
			writeStoredWorkflowError(w, errors.New("stored workflow storage unavailable"))
			return false
		}
		app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
		if !ok {
			return false
		}
		held, err := store.StoredWorkflowEnvironment(r.Context(), app.ID, name)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			writeStoredWorkflowError(w, err)
			return false
		}
		req.Held, req.Found = held, err == nil
	}
	if prob := stageworkflows.Check(req); prob != nil {
		api.WriteProblem(w, prob)
		return false
	}
	return true
}

// stageStoredWorkflowList lists one stage's workflows. They never run yet,
// so the list reports them unavailable.
func (s *server) stageStoredWorkflowList(ctx context.Context, store state.StoredWorkflowStore, app state.App, account state.Account, envID string) (api.ListStoredWorkflowsResponse, error) {
	response := api.ListStoredWorkflowsResponse{AppSlug: app.Slug, RuntimeEnabled: false, UnavailableReason: "stage_workflows_not_runnable",
		MaxDefinitions: account.Plan.WorkflowMaxPerApp(), StoredWorkflows: []api.StoredWorkflowResponse{}}
	records, err := store.ListStageStoredWorkflows(ctx, app.ID, envID)
	if err != nil {
		return response, err
	}
	response.StoredWorkflows, err = storedWorkflowResponses(nil, records)
	return response, err
}
