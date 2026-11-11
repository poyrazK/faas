package apidserver

import (
	"context"
	"maps"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/functionsrepo"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceFunctionCalls is what a pushed function may call through its typed
// client (ADR-20261111150000): its siblings, by their apps in the project,
// and its `calls:` apps.
func (s *server) sourceFunctionCalls(ctx context.Context, acct state.Account, app state.App, spec gregalemanifest.FunctionSpec) (gregalemanifest.FunctionCalls, *api.Problem) {
	siblings := map[string]string{}
	if app.ProjectID != "" && len(spec.ProjectFunctions) > 1 {
		apps, err := s.store.AppsForProject(ctx, acct.ID, app.ProjectID)
		if err != nil {
			return gregalemanifest.FunctionCalls{}, api.ErrCapacity("could not list the project's functions")
		}
		for _, other := range apps {
			if other.ID != app.ID && functionsrepo.Member(other) && other.PreviewOfSlug == "" && other.Status != state.AppDeleted {
				siblings[other.WorkloadName] = other.Slug
			}
		}
	}
	calls, err := spec.CallsFor(app.Slug, siblings)
	if err != nil {
		return gregalemanifest.FunctionCalls{}, api.ErrValidation(err.Error())
	}
	return calls, nil
}

// appResponseBindings is the part of an app's response BindingUpdate reads.
func appResponseBindings(app state.App) api.AppResponse {
	return api.AppResponse{ServiceBindings: app.Manifest.ServiceBindings, ServiceBindingPolicy: app.Manifest.ServiceBindingPolicy}
}

// nonEmptyCallables stores an empty map as nil, so {} clears the field.
func nonEmptyCallables(callables map[string]string) map[string]string {
	if len(callables) == 0 {
		return nil
	}
	return maps.Clone(callables)
}
