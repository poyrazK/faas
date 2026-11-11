package apidserver

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// appDependentsProblem refuses to delete a project service that other
// services of the same project depend on (a service binding or a Compose
// depends_on condition). Deleting it alone left the dependents' bindings
// pointing at nothing: their next deploy failed the dependency gate and their
// calls to the service failed at run time. A reviewed project deploy that
// removes the service from the Compose file is the supported path, because it
// shows the dependents in the same plan.
func (s *server) appDependentsProblem(ctx context.Context, app state.App) *api.Problem {
	if app.ProjectID == "" || app.WorkloadName == "" || app.PreviewOfSlug != "" {
		return nil
	}
	apps, err := s.store.AppsForProject(ctx, app.AccountID, app.ProjectID)
	if err != nil {
		return api.ErrCapacity("list-project-apps")
	}
	dependents := projectServiceDependents(app, apps)
	if len(dependents) == 0 {
		return nil
	}
	return api.NewProblem(http.StatusConflict, "app_has_dependents", "Conflict",
		fmt.Sprintf("Other services of this project depend on %s: %s.", app.WorkloadName, strings.Join(dependents, ", "))).
		WithHint("Remove the service and its references from your Compose file, then run a reviewed deploy (gregale deploy), or delete the dependent apps first.").
		WithDocs("https://gregale.dev/docs/multi-service")
}

// projectServiceDependents lists the slugs of the live, non-preview project
// apps whose manifest names target's service in a binding or a dependency
// condition.
func projectServiceDependents(target state.App, apps []state.App) []string {
	var out []string
	for _, other := range apps {
		if other.ID == target.ID || other.Status == state.AppDeleted || other.PreviewOfSlug != "" ||
			other.AccountID != target.AccountID || other.ProjectID != target.ProjectID {
			continue
		}
		if dependsOnService(other.Manifest, target.WorkloadName) {
			out = append(out, other.Slug)
		}
	}
	sort.Strings(out)
	return out
}

func dependsOnService(manifest state.AppManifest, service string) bool {
	for _, binding := range manifest.ServiceBindings {
		if api.SameServiceName(binding.Service, service) {
			return true
		}
	}
	for name := range manifest.ProjectDependencyConditions {
		if api.SameServiceName(name, service) {
			return true
		}
	}
	return false
}
