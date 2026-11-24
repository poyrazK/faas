// adr: 20261124090000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDeclaredAccessPatch pins a single app's gregale.yaml access key, which
// the CLI deploy sends as declared_access (ADR-20261124090000 amendment 1):
// MCP and CLI agents deploying one browser-facing app open its URL from the
// source, removing the key closes it again, and an app opened by hand that
// never declared access keeps its owner's setting.
func TestDeclaredAccessPatch(t *testing.T) {
	declare := func(t *testing.T, e apidserver.TestEnv, slug, access string) api.AppResponse {
		t.Helper()
		rec := e.Do(t, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{DeclaredAccess: &access}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH declared_access=%q: %d %s", access, rec.Code, rec.Body.String())
		}
		var app api.AppResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
			t.Fatal(err)
		}
		return app
	}
	gated := func(t *testing.T, e apidserver.TestEnv, slug string) api.AppResponse {
		t.Helper()
		app := apidserver.SeedAppForAudit(t, e, slug)
		on := true
		if _, err := (*e.Store()).UpdateApp(t.Context(), app.ID, state.UpdateAppParams{RequireAuthn: &on, SetRequireAuthn: true,
			PublicAuth: &state.AppPublicAuthUpdate{Mode: api.AppPublicAuthModeBearer}, SetPublicAuth: true}); err != nil {
			t.Fatal(err)
		}
		return app
	}

	t.Run("public_then_removed_on_pro", func(t *testing.T) {
		e := apidserver.Setup(t, api.PlanPro)
		app := gated(t, e, "da-pro")
		got := declare(t, e, app.Slug, api.ServiceAccessPublic)
		if got.RequireAuthn || got.PublicAuth.Mode != api.AppPublicAuthModeOpen || got.DeclaredAccess != api.ServiceAccessPublic {
			t.Fatalf("after access: public = require_authn %v mode %q declared %q", got.RequireAuthn, got.PublicAuth.Mode, got.DeclaredAccess)
		}
		got = declare(t, e, app.Slug, "")
		if !got.RequireAuthn || got.PublicAuth.Mode != api.AppPublicAuthModeBearer || got.DeclaredAccess != "" {
			t.Fatalf("after removing access = require_authn %v mode %q declared %q; want the Pro defaults", got.RequireAuthn, got.PublicAuth.Mode, got.DeclaredAccess)
		}
	})
	t.Run("hand_opened_app_kept", func(t *testing.T) {
		e := apidserver.Setup(t, api.PlanPro)
		app := apidserver.SeedAppForAudit(t, e, "da-hand")
		off := false
		if _, err := (*e.Store()).UpdateApp(t.Context(), app.ID, state.UpdateAppParams{RequireAuthn: &off, SetRequireAuthn: true,
			PublicAuth: &state.AppPublicAuthUpdate{Mode: api.AppPublicAuthModeOpen}, SetPublicAuth: true}); err != nil {
			t.Fatal(err)
		}
		if got := declare(t, e, app.Slug, ""); got.RequireAuthn || got.PublicAuth.Mode != api.AppPublicAuthModeOpen {
			t.Fatalf("hand-opened app = require_authn %v mode %q; an undeclared deploy must keep it", got.RequireAuthn, got.PublicAuth.Mode)
		}
	})
	t.Run("key_on_hobby_passes_the_manual_gate", func(t *testing.T) {
		e := apidserver.Setup(t, api.PlanHobby)
		app := apidserver.SeedAppForAudit(t, e, "da-hobby")
		if got := declare(t, e, app.Slug, api.ServiceAccessKey); !got.RequireAuthn || got.DeclaredAccess != api.ServiceAccessKey {
			t.Fatalf("access: key on Hobby = require_authn %v declared %q", got.RequireAuthn, got.DeclaredAccess)
		}
	})
	t.Run("unknown_value_refused", func(t *testing.T) {
		e := apidserver.Setup(t, api.PlanPro)
		app := apidserver.SeedAppForAudit(t, e, "da-bad")
		bad := "anyone"
		if rec := e.Do(t, http.MethodPatch, "/v1/apps/"+app.Slug, api.UpdateAppRequest{DeclaredAccess: &bad}, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("declared_access=anyone: %d %s; want 400", rec.Code, rec.Body.String())
		}
	})
}
