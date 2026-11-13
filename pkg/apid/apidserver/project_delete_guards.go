package apidserver

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectDetachServicesParam opts a project deletion into detaching services
// that still call each other by service name (ADR-20261104221217).
const projectDetachServicesParam = "detach_services"

// projectServiceCallsProblem refuses deleting a project whose services call
// each other by service name: deletion detaches them, and those calls stop
// resolving at once (ADR-20261104221217). The CLI refused this already; the
// API, organization route and dashboard now refuse it too, so every client
// gets the same protection. It returns nil when no call would break.
func (s *server) projectServiceCallsProblem(r *http.Request, acct state.Account, project state.Project) *api.Problem {
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		return api.ErrCapacity("could not inspect project workloads")
	}
	calls := projectDetachedServiceCalls(apps)
	if len(calls) == 0 {
		return nil
	}
	return api.NewProblem(http.StatusConflict, api.CodeProjectServicesCallEachOther, "Project services call each other",
		"deleting "+project.Slug+" detaches its services, and these calls stop working: "+strings.Join(calls, ", ")+".").
		WithHint("Delete the services instead (gregale app delete), or pass " + projectDetachServicesParam + "=true (gregale project delete --detach-services) once they no longer call each other by name.")
}

// projectDetachedServiceCalls lists each "caller -> service" call between a project's
// live production services.
func projectDetachedServiceCalls(apps []state.App) []string {
	var calls []string
	for _, app := range apps {
		if app.Status == state.AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		for _, service := range projectWorkloadDependencies(app) {
			calls = append(calls, orDashName(app.WorkloadName)+" -> "+service)
		}
	}
	sort.Strings(calls)
	return calls
}

func orDashName(name string) string {
	if name == "" {
		return "-"
	}
	return name
}

// deleteProjectChecked deletes through the store's guarded path, which
// serializes with stage creation and clone reservation and enforces the
// organization access fence. It returns "" on success, else the dashboard
// status naming the refusal.
func (s *server) deleteProjectChecked(r *http.Request, acct state.Account, project state.Project) string {
	checked, ok := s.store.(state.CheckedProjectDeleteStore)
	if !ok {
		return "error"
	}
	err := checked.DeleteProjectIfNoStages(r.Context(), acct.ID, project.ID)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, state.ErrConflict):
		return "stages"
	default:
		return "error"
	}
}
