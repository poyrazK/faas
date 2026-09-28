package main

import (
	"net/http"
	"strconv"
	"time"

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

// listOrgAppDeployments serves only the small status/history projection to
// active workspace members; full deployment details stay creator-scoped.
func (s *server) listOrgAppDeployments(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionView) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("app_slug"))
	if err != nil || app.OrgID == "" || app.OrgID != mem.OrgID {
		s.notFound(w, "no such app")
		return
	}
	before, limit, ok := parseOrgAppDeploymentPage(w, r)
	if !ok {
		return
	}
	rows, err := s.listDeploymentsForAppBefore(r.Context(), app.ID, before, limit+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list workspace app deployments"))
		return
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	writeOrgAppDeploymentPage(w, rows, hasMore)
}

func parseOrgAppDeploymentPage(w http.ResponseWriter, r *http.Request) (time.Time, int, bool) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return time.Time{}, 0, false
		}
		if n > 200 {
			n = 200
		}
		limit = n
	}
	var before time.Time
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, raw)
		}
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad cursor", "expected RFC3339 timestamp"))
			return time.Time{}, 0, false
		}
		before = parsed
	}
	return before, limit, true
}

func writeOrgAppDeploymentPage(w http.ResponseWriter, rows []state.Deployment, hasMore bool) {
	response := api.OrgAppDeploymentListResponse{Items: make([]api.OrgAppDeploymentSummary, 0, len(rows))}
	for _, d := range rows {
		response.Items = append(response.Items, api.OrgAppDeploymentSummary{
			ID: d.ID, Revision: d.Revision, Kind: string(d.Kind), Status: string(d.Status),
			CreatedAt: api.FormatAlertTime(d.CreatedAt),
		})
	}
	if hasMore && len(rows) > 0 {
		response.NextBefore = rows[len(rows)-1].CreatedAt.UTC().Format(time.RFC3339Nano)
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
