package apidserver

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectServiceAccessTarget is the require_authn and public_auth_mode a
// production deploy gives an existing app (ADR-20261124090000), and whether
// it changes anything. A service that declares access gets it; one that
// declared it before and no longer does returns to the plan defaults; an
// app whose access was never declared keeps what its owner set.
func projectServiceAccessTarget(w reposcan.Workload, app state.App, plan api.Plan) (requireAuthn bool, mode string, change bool) {
	switch {
	case w.Access != "":
		requireAuthn, mode = reconcile.ServiceAccessAuth(w.Access, plan, app.RequireAuthn, app.PublicAuthMode)
	case app.Manifest.DeclaredAccess != "":
		requireAuthn, mode = reconcile.ServiceAccessRemovedAuth(plan, app.PublicAuthMode)
	default:
		return app.RequireAuthn, app.PublicAuthMode, false
	}
	return requireAuthn, mode, requireAuthn != app.RequireAuthn || mode != app.PublicAuthMode
}

// projectAccessNote says, for a service whose app already exists, how a
// production deploy changes who may call its public URL. Access is app-wide,
// so an environment deploy leaves it alone and says so.
func projectAccessNote(w reposcan.Workload, app state.App, plan api.Plan, production bool) string {
	requireAuthn, mode, change := projectServiceAccessTarget(w, app, plan)
	if !change {
		if w.Access == api.ServiceAccessKey && !reconcile.ServiceAccessKeyAvailable(plan) {
			return fmt.Sprintf("access: %s declares access: key, but the %s plan has no API key gate; its URL stays open", w.Name, plan)
		}
		return ""
	}
	if !production {
		return fmt.Sprintf("access: %s changes only on a production deploy (access is app-wide)", w.Name)
	}
	if !requireAuthn && (mode == "" || mode == api.AppPublicAuthModeOpen) {
		return fmt.Sprintf("access: %s becomes public: anyone can call its URL without a Gregale API key", w.Name)
	}
	if w.Access == "" {
		return fmt.Sprintf("access: %s no longer declares access, so it returns to the plan default and needs a Gregale API key again", w.Name)
	}
	return fmt.Sprintf("access: %s will need a Gregale API key", w.Name)
}

// planWorkloadAccessAuthMode is the plan row's public auth mode for a
// workload that declares access: "" (open) for public, bearer for key where
// the plan has the gate.
func planWorkloadAccessAuthMode(w reposcan.Workload, existing *state.App, plan api.Plan) string {
	requireAuthn, mode := plan.RequireAuthnDefault(), plan.PublicAuthModeDefault()
	if existing != nil {
		requireAuthn, mode = existing.RequireAuthn, existing.PublicAuthMode
	}
	requireAuthn, mode = reconcile.ServiceAccessAuth(w.Access, plan, requireAuthn, mode)
	switch {
	case mode != "" && mode != api.AppPublicAuthModeOpen:
		return mode
	case requireAuthn:
		return api.AppPublicAuthModeBearer
	}
	return ""
}

// applyProjectServiceAccess gives each existing production app the access
// its service declares (or the plan defaults once a declaration is removed),
// on every production deploy: the source is the truth. apps are the
// project's apps as read before the reconcile, which records the new
// declaration in each manifest; the old one says whether a declaration was
// removed. An app the reconcile just created already has its access.
func (s *server) applyProjectServiceAccess(r *http.Request, acct state.Account, apps []state.App, workloads []reposcan.Workload) *api.Problem {
	byName := make(map[string]reposcan.Workload, len(workloads))
	for _, w := range workloads {
		byName[w.Name] = w
	}
	for _, before := range apps {
		app, err := s.store.AppByID(r.Context(), before.ID)
		if err != nil {
			return api.ErrCapacity("could not load " + before.Slug + " for access")
		}
		app.Manifest.DeclaredAccess = before.Manifest.DeclaredAccess
		w, ok := byName[app.WorkloadName]
		if !ok || app.Status == state.AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		requireAuthn, mode, change := projectServiceAccessTarget(w, app, acct.Plan)
		if !change {
			continue
		}
		update := state.UpdateAppParams{RequireAuthn: &requireAuthn, SetRequireAuthn: true}
		if mode != app.PublicAuthMode {
			update.PublicAuth, update.SetPublicAuth = &state.AppPublicAuthUpdate{Mode: mode}, true
		}
		if _, err := s.store.UpdateApp(r.Context(), app.ID, update); err != nil {
			return api.ErrCapacity("could not change access of " + app.Slug)
		}
		_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
			fmt.Sprintf(`{"kind":"updated","slug":"%s","app_id":"%s","lifecycle_changed":false}`, app.Slug, app.ID))
		s.audit.Emit(r.Context(), "app.access_changed", &acct.ID, map[string]any{
			"app_id": app.ID, "slug": app.Slug, "source": "project", "declared": w.Access,
			"old_require_authn": app.RequireAuthn, "new_require_authn": requireAuthn,
			"old_public_auth_mode": app.PublicAuthMode, "new_public_auth_mode": mode,
		})
	}
	return nil
}
