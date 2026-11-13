// adr: 20261111114059
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 2: a service or worker runs its replicas whatever its traffic, and a
// deployment's own min_instances keeps its instances warm, so an agent pays
// for them from its budget. On a zero budget every change that adds standing
// cost asks a person; changes that add none go through.
func TestHandlers_AgentBudgetServicesAndDeployments(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "budget-deploys@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "budget-deploy-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAgentKeyBudget(ctx, key.ID, state.AgentKeyBudget{Set: true}, acct.ID); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+plaintext)
		r.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, r)
		return rec
	}
	asksBudget := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		var problem api.Problem
		if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil ||
			problem.AgentApproval == nil || problem.AgentApproval.Category != api.AgentApprovalBudget {
			t.Fatalf("got %d %s; want 428 budget", rec.Code, rec.Body)
		}
	}

	t.Run("a request app costs nothing to create", func(t *testing.T) {
		if rec := call(http.MethodPost, "/v1/apps", `{"slug":"plain-req"}`); rec.Code != http.StatusCreated {
			t.Fatalf("create request app = %d %s", rec.Code, rec.Body)
		}
	})
	t.Run("a service asks before it runs replicas", func(t *testing.T) {
		asksBudget(t, call(http.MethodPost, "/v1/apps", `{"slug":"svc-new","execution_mode":"service","service_replicas":{"min":1,"max":2,"desired":2}}`))
		if _, err := store.AppBySlug(ctx, "svc-new"); err == nil {
			t.Fatal("the refused service was created")
		}
	})
	t.Run("turning a request app into a worker asks", func(t *testing.T) {
		asksBudget(t, call(http.MethodPatch, "/v1/apps/plain-req", `{"execution_mode":"worker"}`))
		if app, err := store.AppBySlug(ctx, "plain-req"); err != nil || app.Manifest.ExecutionMode == api.ExecutionModeWorker {
			t.Fatalf("app after the refused change = %+v, %v", app.Manifest, err)
		}
	})

	svc, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "svc-live", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5,
		Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 4, Desired: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("more service replicas ask", func(t *testing.T) {
		asksBudget(t, call(http.MethodPatch, "/v1/apps/svc-live", `{"service_replicas":{"min":1,"max":4,"desired":3}}`))
	})
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: svc.ID, ImageDigest: "sha256:" + strings.Repeat("a", 64), Kind: state.DeploymentKindImage, Status: state.DeployLive, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/deployments/" + d.ID
	t.Run("a deployment floor at the app's floor is free", func(t *testing.T) {
		if rec := call(http.MethodPatch, path, `{"min_instances":1}`); rec.Code != http.StatusOK {
			t.Fatalf("floor 1 = %d %s", rec.Code, rec.Body)
		}
	})
	t.Run("a deployment floor above it asks", func(t *testing.T) {
		asksBudget(t, call(http.MethodPatch, path, `{"min_instances":3}`))
		if got, err := store.DeploymentByID(ctx, d.ID); err != nil || got.MinInstances != 1 {
			t.Fatalf("deployment after the refused change = %d, %v; want 1", got.MinInstances, err)
		}
	})
	t.Run("lowering it never asks", func(t *testing.T) {
		if rec := call(http.MethodPatch, path, `{"min_instances":0}`); rec.Code != http.StatusOK {
			t.Fatalf("floor 0 = %d %s", rec.Code, rec.Body)
		}
	})
}
