package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A Compose datastore Gregale has no managed service for is not run. The plan
// says so and names the services that will start without its address
// (ADR-20261104221217); it listed the datastore with no reason before.
// Redis is not that case any more: apply provisions a Compose redis service
// as a managed Valkey cache (#7647), so this uses MongoDB.
func TestScanProjectExplainsDatastoreWithoutManagedEquivalent(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	compose := `services:
  api:
    image: ghcr.io/example/api:v1
    ports: ["8080:8080"]
    environment:
      MONGODB_URL: mongodb://store:27017/app
    depends_on: [store]
  store:
    image: mongo:7
`
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	req := scanAuditRegressionRequest(t, *e.Key(), composeTarGz(t, compose), map[string]string{"project_slug": "cache-plan"})
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scan status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Managed) != 1 {
		t.Fatalf("managed = %+v, want the mongo service", plan.Managed)
	}
	store := plan.Managed[0]
	if store.Provision != nil || store.NotProvisionedReason != "" || !slices.Equal(store.Unbound, []string{"api.MONGODB_URL"}) {
		t.Fatalf("store = %+v, want api.MONGODB_URL unbound and no provision or reason", store)
	}
	if !slices.ContainsFunc(plan.Warnings, func(w string) bool {
		return strings.Contains(w, "store (mongo)") && strings.Contains(w, "api.MONGODB_URL")
	}) {
		t.Fatalf("warnings = %q, want one naming store and api.MONGODB_URL", plan.Warnings)
	}
}
