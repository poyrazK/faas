package apidserver_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261026193018: a project's workloads name the services each declares
// it calls, deduplicated and sorted; a service that calls nothing names none.
func TestProjectWorkloadsNameTheServicesTheyCall(t *testing.T) {
	srv, store, account, project, _ := apidserver.NewProjectLifecycleFixture(t)
	manifest := state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared, ServiceBindings: []api.AppServiceBinding{
		{Service: "worker", Binding: "GREGALE_SERVICE_WORKER_URL"},
		{Service: "api", Binding: "GREGALE_SERVICE_API_URL"},
		{Service: "api", Binding: "API_URL"},
	}}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web",
		Type: state.AppTypeApp, Status: state.AppActive, Manifest: manifest}); err != nil {
		t.Fatal(err)
	}
	req, rec := apidserver.ProjectRequest(http.MethodGet, "/v1/projects/shop", "shop", nil)
	srv.GetProject(rec, req, account)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got api.ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	calls := map[string][]string{}
	for _, workload := range got.Workloads {
		calls[workload.WorkloadName] = workload.DependsOn
	}
	if len(calls) != 2 || !slices.Equal(calls["web"], []string{"api", "worker"}) || calls["api"] != nil {
		t.Fatalf("workload dependencies = %v; want web calling api and worker, api calling nothing", calls)
	}
}
