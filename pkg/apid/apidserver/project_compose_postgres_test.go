package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

const composePostgresProject = `services:
  api:
    image: ghcr.io/example/api:v1
    ports: ["8080:8080"]
    depends_on: [db]
    environment:
      DATABASE_URL: postgres://app:secret@db:5432/app
  worker:
    image: ghcr.io/example/worker:v1
    depends_on: [db]
  db:
    image: postgres:17
`

// ADR-20261025111824: where the account has managed PostgreSQL, a Compose
// postgres service becomes a managed database that apply creates and binds
// to the services that use it; the plan shows it and stops listing the key
// as needing a value.
func TestProjectComposePostgresBecomesAManagedDatabase(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	databases := apidserver.AttachTestManagedPostgres(t, *e.S(), 3)
	post := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := scanAuditRegressionRequest(t, *e.Key(), composeTarGz(t, composePostgresProject), map[string]string{"project_slug": "shop"})
		req.URL.Path = path
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d; body=%s", path, rec.Code, rec.Body.String())
		}
		return rec
	}

	var plan api.PlanResponse
	if err := json.Unmarshal(post("/v1/projects/scan").Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Managed) != 1 || plan.Managed[0].Provision == nil {
		t.Fatalf("managed = %+v, want db provided", plan.Managed)
	}
	provision := plan.Managed[0].Provision
	wantBindings := []api.PlanManagedBinding{{Workload: "api", EnvKey: "DATABASE_URL"}, {Workload: "worker", EnvKey: "DATABASE_URL"}}
	if provision.Database != "shop-db" || provision.Existing || !slices.Equal(provision.Bindings, wantBindings) {
		t.Fatalf("provision = %+v, want shop-db bound to api and worker", provision)
	}
	for _, w := range plan.Workloads {
		if slices.Contains(w.UnsetEnvKeys, "DATABASE_URL") {
			t.Fatalf("%s still lists DATABASE_URL as needing a value", w.Name)
		}
	}

	var applied struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(post("/v1/projects").Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	database, err := databases.FindByName(context.Background(), (*e.Acct()).ID, "shop-db")
	if err != nil {
		t.Fatalf("shop-db not created: %v", err)
	}
	apps, err := (*e.Store()).AppsForProject(context.Background(), (*e.Acct()).ID, applied.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	appIDs := map[string]string{}
	for _, app := range apps {
		appIDs[app.ID] = app.WorkloadName
	}
	bindings, err := databases.ListBindings(context.Background(), (*e.Acct()).ID, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	var bound []string
	for _, binding := range bindings {
		if binding.EnvironmentKey != "DATABASE_URL" || binding.Scope != api.DefaultEnvScope {
			t.Fatalf("binding = %+v", binding)
		}
		bound = append(bound, appIDs[binding.AppID])
	}
	slices.Sort(bound)
	if !slices.Equal(bound, []string{"api", "worker"}) {
		t.Fatalf("bound services = %v", bound)
	}

	// A second apply reuses the database instead of creating another.
	post("/v1/projects")
	all, err := databases.List(context.Background(), (*e.Acct()).ID)
	if err != nil || len(all) != 1 {
		t.Fatalf("databases after reapply = %d, %v", len(all), err)
	}
}

func TestProjectComposePostgresWithoutManagedPostgresIsListedNotProvided(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	req := scanAuditRegressionRequest(t, *e.Key(), composeTarGz(t, composePostgresProject), map[string]string{"project_slug": "shop"})
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	var plan api.PlanResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil {
		t.Fatalf("scan = %d %s", rec.Code, rec.Body.String())
	}
	if len(plan.Managed) != 1 || plan.Managed[0].Provision != nil || plan.Managed[0].NotProvisionedReason != "unavailable" {
		t.Fatalf("managed = %+v, want listed as unavailable", plan.Managed)
	}
	for _, w := range plan.Workloads {
		if w.Name == "api" && !slices.Contains(w.UnsetEnvKeys, "DATABASE_URL") {
			t.Fatalf("api's DATABASE_URL is no longer listed as needing a value: %+v", w)
		}
	}
}
