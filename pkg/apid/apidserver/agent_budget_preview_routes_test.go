// adr: 20261111114059
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func decodePreview(t *testing.T, rec *httptest.ResponseRecorder) api.AgentBudgetPreview {
	t.Helper()
	var out api.AgentBudgetPreview
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.Preview {
		t.Fatalf("preview = %d %s", rec.Code, rec.Body)
	}
	return out
}

// Creating apps, deployment floors and project environments preview their
// cost and create nothing, including on the paths that add nothing (a
// request app, an empty environment) and for a person's credential.
func TestAgentBudgetPreviewAppsDeploymentsEnvironments(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "preview-routes@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	agent := zeroBudgetAgent(t, store, acct.ID, api.ScopeAppsRead, api.ScopeDeployWrite)
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "preview-routes-admin", []string{api.ScopeAdmin}); err != nil {
		t.Fatal(err)
	}
	owner := plaintext
	call := func(token, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(api.AgentPreviewHeader, api.AgentPreviewBudget)
		srv.ServeHTTP(rec, r)
		return rec
	}
	noApp := func(t *testing.T, slug string) {
		t.Helper()
		if _, err := store.AppBySlug(ctx, slug); err == nil {
			t.Fatalf("a preview created app %s", slug)
		}
	}

	t.Run("creating a service is priced, nothing is created", func(t *testing.T) {
		out := decodePreview(t, call(agent, http.MethodPost, "/v1/apps", `{"slug":"svc-preview","execution_mode":"service","service_replicas":{"min":1,"max":2,"desired":2}}`))
		if !out.Charged || out.AddedCents <= 0 || !out.NeedsApproval || out.ApprovalCategory != api.AgentApprovalBudget {
			t.Fatalf("preview = %+v", out)
		}
		noApp(t, "svc-preview")
	})
	t.Run("creating a request app is free, nothing is created", func(t *testing.T) {
		if out := decodePreview(t, call(agent, http.MethodPost, "/v1/apps", `{"slug":"req-preview"}`)); out.AddedCents != 0 || out.NeedsApproval {
			t.Fatalf("preview = %+v", out)
		}
		noApp(t, "req-preview")
	})

	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "floor-svc", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat("c", 64), Kind: state.DeploymentKindImage, Status: state.DeployLive, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a deployment floor is priced and not set", func(t *testing.T) {
		out := decodePreview(t, call(agent, http.MethodPatch, "/v1/deployments/"+d.ID, `{"min_instances":2}`))
		if out.AddedCents != api.CentsCeil(api.AgentStandingMillicents(2, 512)) || !out.NeedsApproval {
			t.Fatalf("preview = %+v", out)
		}
		if got, err := store.DeploymentByID(ctx, d.ID); err != nil || got.MinInstances != d.MinInstances {
			t.Fatalf("deployment after a preview = %d, %v", got.MinInstances, err)
		}
	})

	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MinInstances: 1}); err != nil {
		t.Fatal(err)
	}
	noEnvironment := func(t *testing.T, slug string) {
		t.Helper()
		if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, slug); err == nil {
			t.Fatalf("a preview created environment %s", slug)
		}
	}
	t.Run("an environment copied from a warm one is priced, nothing is created", func(t *testing.T) {
		out := decodePreview(t, call(agent, http.MethodPost, "/v1/projects/shop/environments", `{"slug":"qa","from_environment":"production"}`))
		if out.AddedCents <= 0 || !out.NeedsApproval {
			t.Fatalf("preview = %+v", out)
		}
		noEnvironment(t, "qa")
	})
	t.Run("an empty environment is free, nothing is created", func(t *testing.T) {
		if out := decodePreview(t, call(agent, http.MethodPost, "/v1/projects/shop/environments", `{"slug":"blank"}`)); out.AddedCents != 0 {
			t.Fatalf("preview = %+v", out)
		}
		noEnvironment(t, "blank")
	})
	t.Run("a person's preview is never charged and creates nothing", func(t *testing.T) {
		if out := decodePreview(t, call(owner, http.MethodPost, "/v1/projects/shop/environments", `{"slug":"mine","from_environment":"production"}`)); out.Charged {
			t.Fatalf("preview = %+v", out)
		}
		noEnvironment(t, "mine")
		if out := decodePreview(t, call(owner, http.MethodPost, "/v1/apps", `{"slug":"owner-preview"}`)); out.Charged {
			t.Fatalf("preview = %+v", out)
		}
		noApp(t, "owner-preview")
	})
	t.Run("full clones and branches create nothing", func(t *testing.T) {
		for path, body := range map[string]string{
			"/v1/projects/shop/environment-clones":               `{"slug":"full-qa","from_environment":"production"}`,
			"/v1/projects/shop/environments/production/branches": `{"slug":"pr-1","ttl":"2h"}`,
		} {
			rec := call(agent, http.MethodPost, path, body)
			if rec.Code/100 == 2 {
				decodePreview(t, rec)
			}
		}
		noEnvironment(t, "full-qa")
		noEnvironment(t, "pr-1")
	})
}

// Postgres creates, compute-policy changes, resizes and restores preview
// their cost and change nothing, including a replay of an accepted resize,
// which commits nothing new, and a missing database.
func TestAgentBudgetPreviewPostgres(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	acct := *e.Acct()
	service, d, _ := resizeAPIService(t, acct.ID)
	(*(*e.S()).ManagedPostgres()) = service
	agent := zeroBudgetAgent(t, *e.Store(), acct.ID, api.ScopeManagedPostgresManage)
	real := map[string]string{"Authorization": "Bearer " + agent}
	preview := map[string]string{"Authorization": "Bearer " + agent, api.AgentPreviewHeader: api.AgentPreviewBudget}
	databases := func() int {
		t.Helper()
		list, err := service.List(t.Context(), acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	before := databases()
	unchanged := func(t *testing.T) {
		t.Helper()
		got, err := service.Get(t.Context(), acct.ID, d.ID)
		if err != nil || got.Spec.Class != managedpostgres.ClassDevelopment || !got.Spec.ScaleToZero {
			t.Fatalf("database after a preview = %+v, %v", got.Spec, err)
		}
		if n := databases(); n != before {
			t.Fatalf("databases after a preview = %d, want %d", n, before)
		}
	}
	db := "/v1/postgres/databases/" + d.ID

	t.Run("a compute policy the plan refuses is reported, not done", func(t *testing.T) {
		if rec := e.Do(t, http.MethodPost, db+"/compute-policy", map[string]any{"request_id": uuid.NewString(), "scale_to_zero": false}, preview); rec.Code/100 == 2 {
			t.Fatalf("preview = %d %s; want the plan's refusal", rec.Code, rec.Body)
		}
		unchanged(t)
	})
	t.Run("a scale-to-zero create is free and creates nothing", func(t *testing.T) {
		if out := decodePreview(t, e.Do(t, http.MethodPost, "/v1/postgres/databases", map[string]any{"name": "preview-db", "region": "us-east-1"}, preview)); !out.Charged || out.NeedsApproval {
			t.Fatalf("preview = %+v", out)
		}
		unchanged(t)
	})
	t.Run("a restore is priced and restores nothing", func(t *testing.T) {
		rec := e.Do(t, http.MethodPost, db+"/restore", map[string]any{"name": "restored"}, preview)
		if rec.Code/100 == 2 {
			decodePreview(t, rec)
		}
		unchanged(t)
	})
	t.Run("a resize replay previews as free and does nothing", func(t *testing.T) {
		id := uuid.NewString()
		// A resize of a scale-to-zero database adds nothing, so it is made.
		if rec := e.Do(t, http.MethodPost, db+"/resize", map[string]any{"request_id": id, "service_class": "burstable"}, real); rec.Code != http.StatusAccepted {
			t.Fatalf("resize = %d %s", rec.Code, rec.Body)
		}
		out := decodePreview(t, e.Do(t, http.MethodPost, db+"/resize", map[string]any{"request_id": id, "service_class": "burstable"}, preview))
		if out.AddedCents != 0 || out.NeedsApproval {
			t.Fatalf("replay preview = %+v", out)
		}
	})
	t.Run("resizing an always-on database is priced and not done", func(t *testing.T) {
		// Finish the resize the replay case made before changing the policy.
		if _, err := service.Reconcile(t.Context(), acct.ID, d.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ChangeComputePolicy(t.Context(), managedpostgres.ChangeComputePolicyRequest{
			AccountID: acct.ID, DatabaseID: d.ID, RequestID: uuid.NewString(), ScaleToZero: false,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Reconcile(t.Context(), acct.ID, d.ID); err != nil {
			t.Fatal(err)
		}
		out := decodePreview(t, e.Do(t, http.MethodPost, db+"/resize", map[string]any{"request_id": uuid.NewString(), "service_class": "production"}, preview))
		if out.AddedCents <= 0 || !out.NeedsApproval || out.ApprovalCategory != api.AgentApprovalBudget {
			t.Fatalf("preview = %+v", out)
		}
		if got, err := service.Get(t.Context(), acct.ID, d.ID); err != nil || got.Spec.Class == managedpostgres.ClassProduction {
			t.Fatalf("database after a preview = %+v, %v", got.Spec, err)
		}
	})
	t.Run("a missing database is reported, not previewed", func(t *testing.T) {
		rec := e.Do(t, http.MethodPost, "/v1/postgres/databases/"+uuid.NewString()+"/compute-policy", map[string]any{"request_id": uuid.NewString(), "scale_to_zero": false}, preview)
		if rec.Code/100 == 2 {
			t.Fatalf("preview of a missing database = %d %s", rec.Code, rec.Body)
		}
	})
}
