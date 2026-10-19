package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
)

// Resolve once so the service namespace and its evidence use the same caller.
func (s *server) bindingInventoryDeployment(ctx context.Context, app state.App, id string) (state.Deployment, error) {
	if id == "" {
		return s.store.LiveDeployment(ctx, app.ID)
	}
	dep, err := s.store.DeploymentByID(ctx, id)
	if err == nil && (dep.AppID != app.ID || dep.Status != state.DeployLive || dep.RootfsKey == "" || dep.ImageDigest == "") {
		return state.Deployment{}, state.ErrConflict
	}
	return dep, err
}

func (s *server) deploymentServiceInventory(ctx context.Context, app state.App, dep state.Deployment, selectionErr error, requested, scope string) ([]api.AppBindingInventoryItem, map[string]string, []api.BindingInventoryIssue, string) {
	if selectionErr != nil && (requested != "" || !errors.Is(selectionErr, state.ErrNotFound)) {
		section := bindingInventoryUnavailable(api.BindingTypeService, "service_context_unavailable", "error", "The service inventory deployment could not be read.")
		return []api.AppBindingInventoryItem{}, map[string]string{}, section.issues, "unavailable"
	}
	if selectionErr == nil && dep.EnvironmentWorkloadManaged() {
		items, issues := bindings.ManagedServiceInventory(ctx, s.store, app, dep, scope)
		return items, map[string]string{}, issues, "environment_release"
	}
	items := serviceBindingInventory(app)
	revisions, err := s.serviceBindingRevisions(ctx, app)
	if err != nil {
		section := bindingInventoryUnavailable(api.BindingTypeService, "query_failed", "error", "Service dependency metadata could not be read.")
		return items, map[string]string{}, section.issues, "app"
	}
	return items, revisions, nil, "app"
}

func managedServiceProbeProblem(dep state.Deployment, command []string, shell bool) *api.Problem {
	if !dep.EnvironmentWorkloadManaged() || len(command) == 0 || shell {
		return nil
	}
	switch command[0] {
	case api.AppTaskServiceBindingProbeCommand, api.AppTaskServiceBindingTargetProbeCommand, api.AppTaskServiceBindingSmokeCommand:
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Managed service verification unavailable", "Managed service bindings require the reviewed environment qualification workflow; ordinary task probes and smoke requests cannot verify this context.")
	}
	return nil
}
