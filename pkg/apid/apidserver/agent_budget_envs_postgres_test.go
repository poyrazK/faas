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

// zeroBudgetAgent mints an agent key whose budget is zero and that chose no
// approval categories, so only the budget can ask a person.
func zeroBudgetAgent(t *testing.T, store *state.MemStore, accountID string, scopes ...string) string {
	t.Helper()
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(t.Context(), accountID, hash, "zero-budget-agent", append([]string{api.ScopeAgent}, scopes...))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPIKeyApprovalCategories(t.Context(), key.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAgentKeyBudget(t.Context(), key.ID, state.AgentKeyBudget{Set: true}, accountID); err != nil {
		t.Fatal(err)
	}
	return plaintext
}

func wantBudgetApproval(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var problem api.Problem
	if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil ||
		problem.AgentApproval == nil || problem.AgentApproval.Category != api.AgentApprovalBudget {
		t.Fatalf("got %d %s; want 428 budget", rec.Code, rec.Body)
	}
}

// Slice 3: an environment an agent creates from another copies its warm
// instances, so it is priced at their standing cost. A branch drops the
// min_instances floor but keeps a service's replicas.
func TestHandlers_AgentBudgetEnvironmentClones(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "budget-envs@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512}); err != nil {
		t.Fatal(err)
	}
	agent := zeroBudgetAgent(t, store, acct.ID, api.ScopeAppsRead, api.ScopeDeployWrite)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+agent)
		r.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, r)
		return rec
	}

	// Nothing in production keeps an instance warm: the copy is free.
	if rec := call(http.MethodPost, "/v1/projects/shop/environments", `{"slug":"staging","from_environment":"production"}`); rec.Code != http.StatusCreated {
		t.Fatalf("clone of a cold environment = %d %s", rec.Code, rec.Body)
	}
	// One warm instance in production makes the copy cost money.
	app, err := store.AppBySlug(ctx, "shop-web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MinInstances: ptrInt(1), SetMinInstances: true}); err != nil {
		t.Fatal(err)
	}
	wantBudgetApproval(t, call(http.MethodPost, "/v1/projects/shop/environments", `{"slug":"qa","from_environment":"production"}`))
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "qa"); err == nil {
		t.Fatal("the refused environment was created")
	}
	// An empty environment copies nothing.
	if rec := call(http.MethodPost, "/v1/projects/shop/environments", `{"slug":"blank"}`); rec.Code != http.StatusCreated {
		t.Fatalf("empty environment = %d %s", rec.Code, rec.Body)
	}
}

// Slice 3: managed Postgres costs standing money only when it never scales
// to zero; resizing such a database to a larger minimum compute asks.
func TestHandlers_AgentBudgetPostgres(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	acct := *e.Acct()
	service, d, _ := resizeAPIService(t, acct.ID)
	(*(*e.S()).ManagedPostgres()) = service
	if _, err := service.ChangeComputePolicy(t.Context(), managedpostgres.ChangeComputePolicyRequest{
		AccountID: acct.ID, DatabaseID: d.ID, RequestID: uuid.NewString(), ScaleToZero: false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(t.Context(), acct.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	agent := zeroBudgetAgent(t, *e.Store(), acct.ID, api.ScopeManagedPostgresManage)
	headers := map[string]string{"Authorization": "Bearer " + agent}
	path := "/v1/postgres/databases/" + d.ID + "/resize"

	// development → production: 0.25 → 1 compute unit always on.
	wantBudgetApproval(t, e.Do(t, http.MethodPost, path, map[string]any{"request_id": uuid.NewString(), "service_class": "production"}, headers))
	// development → burstable keeps the same minimum: nothing added.
	if rec := e.Do(t, http.MethodPost, path, map[string]any{"request_id": uuid.NewString(), "service_class": "burstable"}, headers); rec.Code != http.StatusAccepted {
		t.Fatalf("same-minimum resize = %d %s", rec.Code, rec.Body)
	}
}

func TestAgentPostgresStandingMillicents(t *testing.T) {
	quarter := int64(api.AgentBudgetHoursPerMonth) * api.ManagedPostgresOverageMillicentsPerComputeUnitHour / 4
	for _, tc := range []struct {
		class       string
		scaleToZero bool
		want        int64
	}{
		{"development", true, 0},
		{"production", true, 0},
		{"development", false, quarter},
		{"burstable", false, quarter},
		{"production", false, 4 * quarter},
		{"unknown", false, 4 * quarter},
	} {
		if got := api.AgentPostgresStandingMillicents(tc.class, tc.scaleToZero); got != tc.want {
			t.Errorf("%s scale_to_zero=%v = %d, want %d", tc.class, tc.scaleToZero, got, tc.want)
		}
	}
}
