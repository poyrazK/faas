package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261019120041
func TestProjectDeploymentReadinessAdapter(t *testing.T) {
	srv, store, account, project, _ := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectEnvironmentProtection(t.Context(), account.ID, project.ID, "production", false); err != nil {
		t.Fatal(err)
	}
	request := api.ProjectDeploymentCheckRequest{Environment: "production", Workloads: []api.ProjectDeploymentCheckWorkload{{Workload: api.PlanWorkload{Name: "api", Class: "http"}}}}
	for _, protected := range []bool{false, true} {
		if _, err := store.UpdateProjectEnvironmentProtection(t.Context(), account.ID, project.ID, "production", protected); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(request)
		req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/deployment-readiness", project.Slug, raw)
		srv.checkProjectDeployment(rec, req, account)
		if rec.Code != http.StatusOK {
			t.Fatalf("readiness=%d %s", rec.Code, rec.Body)
		}
		var report api.ProjectDeploymentReadiness
		if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if protected && report.Status != "blocked" || !protected && report.Status != "ready" || report.RuntimeChecked {
			t.Fatalf("unexpected scoped readiness: %+v", report)
		}
		if _, err := store.ActiveProjectReleaseSet(t.Context(), account.ID, project.ID, "production"); err == nil {
			t.Fatal("readiness created a release graph")
		}
	}
}

// adr: 20261019120041
func TestProjectDeploymentReadinessReadScope(t *testing.T) {
	srv, store, account, key := projectSecretsTestServer(t, api.ScopesDeploymentReadSurface)
	raw := []byte(`{"environment":"production","workloads":[{"workload":{"name":"api","class":"http"}}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/fresh/deployment-readiness", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deployment-read scoped key could not check readiness: %d %s", rec.Code, rec.Body)
	}
	var report api.ProjectDeploymentReadiness
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil || report.Status != "ready" {
		t.Fatal("unexpected read-scope report", report, err)
	}
	if _, err := store.MemStore.ProjectBySlug(t.Context(), account.ID, "fresh"); err == nil {
		t.Fatal("read-scoped check created a project")
	}
}
