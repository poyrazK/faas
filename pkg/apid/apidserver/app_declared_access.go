package apidserver

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceAccessRestore is an app's access before a source deploy applied its
// gregale.yaml access key, so a rolled-back deploy can put it back.
type sourceAccessRestore struct {
	requireAuthn   bool
	publicAuthMode string
	declared       string
}

// applyManifestAccess applies a single app's gregale.yaml access key on a
// production source deploy (ADR-20261124090000 amendment 1): "public" opens
// the URL, "key" needs a Gregale API key where the plan has that gate, and ""
// returns an app that used to declare access to the plan default. It writes
// below the manual require_authn plan gate, as a project deploy does. The
// app is reloaded first: earlier manifest staging may have rewritten its
// manifest. Project apps are left alone; their project source manages access.
// It returns what to restore if the deploy is rolled back, or nil when
// nothing changed.
func (s *server) applyManifestAccess(ctx context.Context, acct state.Account, appID, access string) (*sourceAccessRestore, *api.Problem) {
	app, err := s.store.AppByID(ctx, appID)
	if err != nil {
		return nil, api.ErrCapacity("could not read the app to apply the manifest's access key")
	}
	if app.ProjectID != "" {
		return nil, nil
	}
	change := reconcile.DeclaredAccessChange(access, app.Manifest.DeclaredAccess, acct.Plan, app.RequireAuthn, app.PublicAuthMode)
	if !change.Changed() {
		return nil, nil
	}
	restore := &sourceAccessRestore{requireAuthn: app.RequireAuthn, publicAuthMode: app.PublicAuthMode, declared: app.Manifest.DeclaredAccess}
	if err := s.writeAppAccess(ctx, app, change.RequireAuthn, change.PublicAuthMode, change.Declared); err != nil {
		return nil, api.ErrCapacity("could not apply the manifest's access key")
	}
	s.audit.Emit(ctx, "app.access_changed", &acct.ID, map[string]any{
		"app_id": app.ID, "slug": app.Slug, "source": "manifest", "declared": access,
		"old_require_authn": app.RequireAuthn, "new_require_authn": change.RequireAuthn,
		"old_public_auth_mode": app.PublicAuthMode, "new_public_auth_mode": change.PublicAuthMode,
	})
	return restore, nil
}

// restoreManifestAccess undoes applyManifestAccess for a rolled-back deploy.
func (s *server) restoreManifestAccess(ctx context.Context, appID string, restore *sourceAccessRestore) error {
	if restore == nil {
		return nil
	}
	app, err := s.store.AppByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("restore app access: %w", err)
	}
	if err := s.writeAppAccess(ctx, app, restore.requireAuthn, restore.publicAuthMode, restore.declared); err != nil {
		return fmt.Errorf("restore app access: %w", err)
	}
	return nil
}

func (s *server) writeAppAccess(ctx context.Context, app state.App, requireAuthn bool, mode, declared string) error {
	params := state.UpdateAppParams{}
	if requireAuthn != app.RequireAuthn {
		params.RequireAuthn, params.SetRequireAuthn = &requireAuthn, true
	}
	if mode != app.PublicAuthMode {
		params.PublicAuth, params.SetPublicAuth = &state.AppPublicAuthUpdate{Mode: mode}, true
	}
	if declared != app.Manifest.DeclaredAccess {
		manifest := app.Manifest
		manifest.DeclaredAccess = declared
		params.Manifest = &manifest
	}
	if _, err := s.store.UpdateApp(ctx, app.ID, params); err != nil {
		return err
	}
	_ = s.notif.Notify(ctx, db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"updated","slug":"%s","app_id":"%s","lifecycle_changed":false}`, app.Slug, app.ID))
	return nil
}
