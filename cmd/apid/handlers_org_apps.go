package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// listOrgApps exposes only safe inventory fields for apps persisted to the
// verified organization. It does not widen access to creator-scoped app APIs.
func (s *server) listOrgApps(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionView) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	lister, ok := s.store.(state.OrgAppLister)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workspace app inventory is unavailable"))
		return
	}
	apps, err := lister.ListAppsByOrg(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list workspace apps"))
		return
	}
	writeOrgAppList(w, apps)
}

func writeOrgAppList(w http.ResponseWriter, apps []state.App) {
	response := api.OrgAppListResponse{Apps: make([]api.OrgAppSummary, 0, len(apps))}
	for _, app := range apps {
		response.Apps = append(response.Apps, api.OrgAppSummary{
			ID: app.ID, Slug: app.Slug, Type: string(app.Type), Runtime: app.Runtime,
			Status: string(app.Status), CreatedAt: api.FormatAlertTime(app.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// createOrgApp creates an app whose persisted organization is the
// LoadOrg-verified workspace. The creator remains the app's account owner
// until the broader app API authorization and billing migration is complete.
func (s *server) createOrgApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionCreateApp) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	s.createAppInOrg(w, r, acct, mem.OrgID)
}
