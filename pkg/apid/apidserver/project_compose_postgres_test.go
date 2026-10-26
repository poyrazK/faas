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
	if provision.Database != "shop-db" || provision.Existing || provision.PostgresMajor != 17 || !slices.Equal(provision.Bindings, wantBindings) {
		t.Fatalf("provision = %+v, want shop-db (PostgreSQL 17) bound to api and worker", provision)
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

	if database.Spec.PostgresMajor != 17 {
		t.Fatalf("shop-db runs PostgreSQL %d, want the image's 17", database.Spec.PostgresMajor)
	}

	// A second apply reuses the database instead of creating another.
	post("/v1/projects")
	all, err := databases.List(context.Background(), (*e.Acct()).ID)
	if err != nil || len(all) != 1 {
		t.Fatalf("databases after reapply = %d, %v", len(all), err)
	}
}

// The managed database runs the Compose image's major, or the nearest newer
// one offered; never an older one.
func TestComposePostgresMajor(t *testing.T) {
	offered := []int{15, 16, 17}
	for _, tc := range []struct {
		image string
		want  int
		ok    bool
	}{
		{"postgres:17", 17, true},
		{"postgres:16.4-alpine", 16, true},
		{"docker.io/library/postgres:15@sha256:abc", 15, true},
		{"bitnami/postgresql:15.6.0", 15, true},
		{"pgvector/pgvector:pg16", 16, true},
		{"postgres:14", 15, true},
		{"postgres", 17, true},
		{"postgres:latest", 17, true},
		{"localhost:5000/postgres", 17, true},
		{"postgres:18", 0, false},
	} {
		got, ok := apidserver.ComposePostgresMajor(tc.image, offered)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: major = %d, %v; want %d, %v", tc.image, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := apidserver.ComposePostgresMajor("postgres:16", nil); ok {
		t.Error("a major was picked with none offered")
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
