// adr: 20261124090000
package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestProjectDeployAppliesDeclaredAccess pins the agent eval of 2026-11-23
// (voting-app P11): the agent deployed two browser pages, found them behind a
// Gregale API key, and stopped to ask. A service now declares its access in
// the source, every production deploy applies it, and removing the
// declaration closes the URL again. An app opened by hand, with no
// declaration, keeps its owner's setting.
func TestProjectDeployAppliesDeclaredAccess(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	declared := "services:\n  vote:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n    x-gregale-access: public\n" +
		"  admin:\n    image: nginx:1.27\n    ports: [\"8081:80\"]\n"
	first := applyEnvironmentProject(t, *e.H(), *e.Key(), "", declared)
	store := *e.Store()
	apps := map[string]state.App{}
	for _, build := range first.Builds {
		app, err := store.AppByID(t.Context(), build.AppID)
		if err != nil {
			t.Fatal(err)
		}
		apps[app.WorkloadName] = app
	}
	vote, admin := apps["vote"], apps["admin"]
	if vote.RequireAuthn || vote.PublicAuthMode != api.AppPublicAuthModeOpen || vote.Manifest.DeclaredAccess != api.ServiceAccessPublic {
		t.Fatalf("vote = require_authn %v mode %q declared %q; want open", vote.RequireAuthn, vote.PublicAuthMode, vote.Manifest.DeclaredAccess)
	}
	if !admin.RequireAuthn || admin.PublicAuthMode != api.AppPublicAuthModeBearer {
		t.Fatalf("admin = require_authn %v mode %q; want the Pro defaults", admin.RequireAuthn, admin.PublicAuthMode)
	}
	// The owner opens admin by hand; nothing in the source declares it.
	open, requireAuthn := api.AppPublicAuthModeOpen, false
	if _, err := store.UpdateApp(t.Context(), admin.ID, state.UpdateAppParams{RequireAuthn: &requireAuthn, SetRequireAuthn: true,
		PublicAuth: &state.AppPublicAuthUpdate{Mode: open}, SetPublicAuth: true}); err != nil {
		t.Fatal(err)
	}

	undeclared := "services:\n  vote:\n    image: nginx:1.28\n    ports: [\"8080:80\"]\n" +
		"  admin:\n    image: nginx:1.28\n    ports: [\"8081:80\"]\n"
	applyEnvironmentProject(t, *e.H(), *e.Key(), "", undeclared)
	vote, _ = store.AppByID(t.Context(), vote.ID)
	admin, _ = store.AppByID(t.Context(), admin.ID)
	if !vote.RequireAuthn || vote.PublicAuthMode != api.AppPublicAuthModeBearer || vote.Manifest.DeclaredAccess != "" {
		t.Fatalf("vote after removing access = require_authn %v mode %q declared %q; want the Pro defaults",
			vote.RequireAuthn, vote.PublicAuthMode, vote.Manifest.DeclaredAccess)
	}
	if admin.RequireAuthn || admin.PublicAuthMode != api.AppPublicAuthModeOpen {
		t.Fatalf("admin opened by hand = require_authn %v mode %q; a deploy must keep it", admin.RequireAuthn, admin.PublicAuthMode)
	}
}
