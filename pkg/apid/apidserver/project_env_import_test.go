package apidserver_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An opted-in project's plain Compose values are written at apply; secret
// and service-address values are not, and a value set on the app is kept
// (ADR-20261023085230).
func TestApplyProjectImportsOptedInComposeEnv(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	compose := `services:
  api:
    image: ghcr.io/example/api:v1
    ports: ["8080:8080"]
    environment:
      LOG_LEVEL: info
      GREETING: imported-greeting-sentinel
      STRIPE_KEY: plain-but-named-like-a-secret
      BILLING_URL: http://billing:8080
  billing:
    image: ghcr.io/example/billing:v1
`
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	apply := func(t *testing.T, slug, body string) []state.App {
		t.Helper()
		req := scanAuditRegressionRequest(t, *e.Key(), composeTarGz(t, body), map[string]string{"project_slug": slug})
		req.URL.Path = "/v1/projects"
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("apply status = %d; body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "imported-greeting-sentinel") {
			t.Fatal("the apply receipt carried an environment value")
		}
		var resp struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		apps, err := (*e.Store()).AppsForProject(context.Background(), (*e.Acct()).ID, resp.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		return apps
	}
	envOf := func(t *testing.T, apps []state.App, workload string) (state.App, map[string]string) {
		t.Helper()
		for _, app := range apps {
			if app.WorkloadName != workload {
				continue
			}
			rows, err := (*e.Store()).ListAppEnvInScope(context.Background(), (*e.Acct()).ID, app.ID, api.DefaultEnvScope)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{}
			for _, row := range rows {
				values[row.Key] = row.Value
			}
			return app, values
		}
		t.Fatalf("no app for workload %s", workload)
		return state.App{}, nil
	}

	_, values := envOf(t, apply(t, "plain-env", compose), "api")
	if len(values) != 0 {
		t.Fatalf("without the opt-in, values = %v", values)
	}

	optedIn := "x-gregale-env-import: true\n" + compose
	app, values := envOf(t, apply(t, "import-env", optedIn), "api")
	// BILLING_URL calls the project's billing service, so it imports as that
	// service's Gregale address (ADR-20261026181020).
	if want := map[string]string{"LOG_LEVEL": "info", "GREETING": "imported-greeting-sentinel", "BILLING_URL": "http://billing.svc.gregale:10081"}; !maps.Equal(values, want) {
		t.Fatalf("imported = %v, want %v", values, want)
	}
	if err := (*e.Store()).UpsertAppEnvInScope(context.Background(), (*e.Acct()).ID, app.ID, api.DefaultEnvScope, "LOG_LEVEL", "debug"); err != nil {
		t.Fatal(err)
	}
	_, values = envOf(t, apply(t, "import-env", optedIn), "api")
	if values["LOG_LEVEL"] != "debug" {
		t.Fatalf("a configured value was replaced: %v", values)
	}
}
