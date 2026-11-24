package apidserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// applyDeclaredAccess folds a single app's declared access, the `access:`
// key of its gregale.yaml that a deploy sends as declared_access
// (ADR-20261124090000 amendment 1), into an app update. The source is the
// truth: "public" opens the URL, "key" needs a Gregale API key where the
// plan has that gate, and "" returns an app that used to declare access to
// the plan default. It writes the store fields directly, below the
// require_authn plan gate, as a project deploy does. An explicit
// require_authn or public_auth in the same request wins for its field.
// Project apps are left alone: their project source manages access.
func applyDeclaredAccess(acct state.Account, app state.App, req api.UpdateAppRequest, params *state.UpdateAppParams) *api.Problem {
	if req.DeclaredAccess == nil || app.ProjectID != "" {
		return nil
	}
	declared, err := reposcan.NormalizeServiceAccess(*req.DeclaredAccess)
	if err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid declared_access", "declared_access "+err.Error())
	}
	previous := app.Manifest.DeclaredAccess
	requireAuthn, mode := app.RequireAuthn, app.PublicAuthMode
	switch {
	case declared != "":
		requireAuthn, mode = reconcile.ServiceAccessAuth(declared, acct.Plan, requireAuthn, mode)
	case previous != "":
		requireAuthn, mode = reconcile.ServiceAccessRemovedAuth(acct.Plan, mode)
	}
	if requireAuthn != app.RequireAuthn && req.RequireAuthn == nil {
		params.RequireAuthn, params.SetRequireAuthn = &requireAuthn, true
	}
	if mode != app.PublicAuthMode && req.PublicAuth == nil {
		params.PublicAuth, params.SetPublicAuth = &state.AppPublicAuthUpdate{Mode: mode}, true
	}
	if declared != previous {
		if params.Manifest == nil {
			manifest := app.Manifest
			params.Manifest = &manifest
		}
		params.Manifest.DeclaredAccess = declared
	}
	return nil
}

// applyManifestAccess applies a source deploy's access key to its app, as
// the CLI's declared_access does for an uploaded source. Only a production
// deploy applies it: access is app-wide.
func (s *server) applyManifestAccess(ctx context.Context, acct state.Account, app state.App, access, deploymentScope string) *api.Problem {
	if deploymentScope != "" && deploymentScope != api.DefaultEnvScope {
		return nil
	}
	var params state.UpdateAppParams
	if problem := applyDeclaredAccess(acct, app, api.UpdateAppRequest{DeclaredAccess: &access}, &params); problem != nil {
		return problem
	}
	if !params.SetRequireAuthn && !params.SetPublicAuth && params.Manifest == nil {
		return nil
	}
	updated, err := s.store.UpdateApp(ctx, app.ID, params)
	if err != nil {
		return api.ErrCapacity("could not apply the manifest's access key")
	}
	_ = s.notif.Notify(ctx, db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"updated","slug":"%s","app_id":"%s","lifecycle_changed":false}`, app.Slug, app.ID))
	s.audit.Emit(ctx, "app.access_changed", &acct.ID, map[string]any{
		"app_id": app.ID, "slug": app.Slug, "source": "manifest", "declared": access,
		"old_require_authn": app.RequireAuthn, "new_require_authn": updated.RequireAuthn,
		"old_public_auth_mode": app.PublicAuthMode, "new_public_auth_mode": updated.PublicAuthMode,
	})
	return nil
}
