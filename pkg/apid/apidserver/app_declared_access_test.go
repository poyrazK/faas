// adr: 20261124090000
package apidserver_test

import (
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSourceDeployAppliesDeclaredAccess pins a single app's gregale.yaml
// access key (ADR-20261124090000 amendment 1), which MCP and CLI agents
// deploy through source uploads: a production deploy that takes traffic
// opens the URL, removing the key (or the whole gregale.yaml) closes it
// again, a dark or staging deploy leaves access alone, a rolled-back deploy
// restores it, and an app opened by hand that never declared access keeps
// its owner's setting.
func TestSourceDeployAppliesDeclaredAccess(t *testing.T) {
	setup := func(t *testing.T, plan api.Plan, slug string, requireAuthn bool, mode string) (apidserver.TestEnv, state.App) {
		t.Helper()
		e := apidserver.Setup(t, plan)
		store, acct := *e.Store(), *e.Acct()
		app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{RequireAuthn: &requireAuthn, SetRequireAuthn: true,
			PublicAuth: &state.AppPublicAuthUpdate{Mode: mode}, SetPublicAuth: true}); err != nil {
			t.Fatal(err)
		}
		return e, app
	}
	stage := func(t *testing.T, e apidserver.TestEnv, app state.App, m *gregalemanifest.Manifest, rollout state.Deployment) apidserver.SourceRefManifestStaged {
		t.Helper()
		r := httptest.NewRequest("POST", "/", nil).WithContext(t.Context())
		staged, problem := (*e.S()).StageSourceDeploymentManifest(r, *e.Acct(), app, m, rollout, false)
		if problem != nil {
			t.Fatalf("stage: %+v", problem)
		}
		return staged
	}
	access := func(t *testing.T, e apidserver.TestEnv, app state.App) state.App {
		t.Helper()
		got, err := (*e.Store()).AppByID(t.Context(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	production := state.Deployment{Scope: state.DefaultEnvScope}

	t.Run("declare_then_remove_on_pro", func(t *testing.T) {
		e, app := setup(t, api.PlanPro, "da-pro", true, api.AppPublicAuthModeBearer)
		stage(t, e, app, &gregalemanifest.Manifest{Access: api.ServiceAccessPublic}, production)
		if got := access(t, e, app); got.RequireAuthn || got.PublicAuthMode != api.AppPublicAuthModeOpen || got.Manifest.DeclaredAccess != api.ServiceAccessPublic {
			t.Fatalf("after access: public = require_authn %v mode %q declared %q", got.RequireAuthn, got.PublicAuthMode, got.Manifest.DeclaredAccess)
		}
		stage(t, e, app, nil, production) // gregale.yaml deleted
		if got := access(t, e, app); !got.RequireAuthn || got.PublicAuthMode != api.AppPublicAuthModeBearer || got.Manifest.DeclaredAccess != "" {
			t.Fatalf("after removing gregale.yaml = require_authn %v mode %q declared %q; want the Pro defaults", got.RequireAuthn, got.PublicAuthMode, got.Manifest.DeclaredAccess)
		}
	})
	t.Run("rollback_restores", func(t *testing.T) {
		e, app := setup(t, api.PlanPro, "da-rollback", true, api.AppPublicAuthModeBearer)
		staged := stage(t, e, app, &gregalemanifest.Manifest{Access: api.ServiceAccessPublic}, production)
		if err := (*e.S()).RollbackSourceRefManifest(t.Context(), staged); err != nil {
			t.Fatal(err)
		}
		if got := access(t, e, app); !got.RequireAuthn || got.PublicAuthMode != api.AppPublicAuthModeBearer || got.Manifest.DeclaredAccess != "" {
			t.Fatalf("after rollback = require_authn %v mode %q declared %q; want it unchanged", got.RequireAuthn, got.PublicAuthMode, got.Manifest.DeclaredAccess)
		}
	})
	t.Run("dark_and_stage_deploys_leave_access", func(t *testing.T) {
		e, app := setup(t, api.PlanPro, "da-dark", true, api.AppPublicAuthModeBearer)
		m := &gregalemanifest.Manifest{Access: api.ServiceAccessPublic}
		stage(t, e, app, m, state.Deployment{Scope: state.DefaultEnvScope, TrafficPercentExplicit: true})
		stage(t, e, app, m, state.Deployment{Scope: "staging"})
		if got := access(t, e, app); !got.RequireAuthn {
			t.Fatal("a dark or staging deploy opened the app")
		}
	})
	t.Run("hand_opened_app_kept", func(t *testing.T) {
		e, app := setup(t, api.PlanPro, "da-hand", false, api.AppPublicAuthModeOpen)
		stage(t, e, app, nil, production)
		if got := access(t, e, app); got.RequireAuthn || got.PublicAuthMode != api.AppPublicAuthModeOpen {
			t.Fatalf("hand-opened app = require_authn %v mode %q; an undeclared deploy must keep it", got.RequireAuthn, got.PublicAuthMode)
		}
	})
	t.Run("key_on_hobby_passes_the_manual_gate", func(t *testing.T) {
		e, app := setup(t, api.PlanHobby, "da-hobby", false, api.AppPublicAuthModeOpen)
		stage(t, e, app, &gregalemanifest.Manifest{Access: api.ServiceAccessKey}, production)
		if got := access(t, e, app); !got.RequireAuthn || got.Manifest.DeclaredAccess != api.ServiceAccessKey {
			t.Fatalf("access: key on Hobby = require_authn %v declared %q", got.RequireAuthn, got.Manifest.DeclaredAccess)
		}
	})
}
