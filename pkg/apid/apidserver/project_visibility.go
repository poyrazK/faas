package apidserver

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectVisibilityNote says, for a service whose app already exists, how the
// deploy treats a Compose visibility that differs from the app's
// (ADR-20261107090000). Visibility used to be set only when the app was
// created, so removing a service's ports: left it on the internet with no
// word in the plan.
func projectVisibilityNote(w reposcan.Workload, app state.App, production bool) string {
	current := api.NormalizeAppVisibility(app.Visibility)
	switch {
	case reposcan.NewWorkloadVisibility(w) == api.AppVisibilityInternal && current != api.AppVisibilityInternal:
		if !production {
			return fmt.Sprintf("visibility: %s is public; Compose publishes no ports for it, but visibility is app-wide and changes only on a production deploy", w.Name)
		}
		return fmt.Sprintf("visibility: %s becomes internal (Compose publishes no ports for it) and loses its public URL", w.Name)
	case w.DetectedBy.Detector == "compose" && !w.Unpublished && current == api.AppVisibilityInternal:
		return fmt.Sprintf("visibility: %s stays internal although Compose publishes its ports; publish it with gregale app %s --visibility public", w.Name, app.Slug)
	}
	return ""
}

// applyComposeInternalVisibility makes internal each existing production app
// whose Compose service no longer publishes ports. Only that direction is
// applied: it removes a public URL, never adds one.
func (s *server) applyComposeInternalVisibility(r *http.Request, acct state.Account, projectID string, workloads []reposcan.Workload) *api.Problem {
	internal := map[string]bool{}
	for _, w := range workloads {
		if reposcan.NewWorkloadVisibility(w) == api.AppVisibilityInternal {
			internal[w.Name] = true
		}
	}
	if len(internal) == 0 {
		return nil
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, projectID)
	if err != nil {
		return api.ErrCapacity("could not load project apps for visibility")
	}
	visibility := api.AppVisibilityInternal
	for _, app := range apps {
		if app.Status == state.AppDeleted || app.PreviewOfSlug != "" || !internal[app.WorkloadName] ||
			api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
			continue
		}
		if _, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{Visibility: &visibility, SetVisibility: true}); err != nil {
			return api.ErrCapacity("could not make " + app.Slug + " internal")
		}
		_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
			fmt.Sprintf(`{"kind":"updated","slug":"%s","app_id":"%s","lifecycle_changed":false}`, app.Slug, app.ID))
		s.audit.Emit(r.Context(), "app.visibility_changed", &acct.ID, map[string]any{
			"app_id": app.ID, "slug": app.Slug, "source": "compose",
			"old": string(api.NormalizeAppVisibility(app.Visibility)), "new": string(visibility),
		})
	}
	return nil
}
