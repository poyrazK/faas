// adr: 20261112200000 — queue review fixes (2026-11-12).
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Re-running `gregale queue setup` reset the default binding's concurrency to
// 1 and silently switched a pull binding to push. It now keeps an omitted
// concurrency and needs force to switch the mode.
func TestConfigureQueueWorkloadRerunKeepsBinding(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "queue-rerun", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	pull, err := (*e.Store()).CreateQueueBinding(ctx, state.QueueBinding{AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "default", QueueName: "default",
		Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 7})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPut, "/v1/apps/queue-rerun/queue-workload", api.QueueWorkloadProfileRequest{TargetDepth: 20}, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "force=true") {
		t.Fatalf("setup over a pull binding = %d %s; want 409 asking for force", rec.Code, rec.Body.String())
	}
	if got, err := (*e.Store()).QueueBindingByID(ctx, (*e.Acct()).ID, app.ID, pull.ID); err != nil || got.Mode != "pull" {
		t.Fatalf("refused setup changed the binding: %+v %v", got, err)
	}
	rec = e.Do(t, http.MethodPut, "/v1/apps/queue-rerun/queue-workload", api.QueueWorkloadProfileRequest{TargetDepth: 20, Force: true}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("forced setup = %d %s", rec.Code, rec.Body.String())
	}
	var got api.QueueWorkloadProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Binding.Mode != "push" || got.Binding.MaxConcurrency != 7 {
		t.Fatalf("forced setup binding = %+v; want push keeping max_concurrency 7", got.Binding)
	}
}

// A stage environment accepted an enabled pull binding, and sends to it, but
// leases serve production only: no worker could ever take those messages.
// An enabled stage pull binding is now refused; a disabled one is kept.
func TestProjectEnvironmentQueueRefusesEnabledStagePull(t *testing.T) {
	srv, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	class := state.WorkloadClassWorker
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{WorkloadClass: &class}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	put := func(body string) (int, string) {
		t.Helper()
		path := "/v1/projects/" + project.Slug + "/environments/stage/workloads/" + app.Slug + "/queue-bindings"
		r, w := apidserver.ProjectRequest(http.MethodPut, path, project.Slug, []byte(body))
		r.SetPathValue("environment", "stage")
		r.SetPathValue("workload", app.Slug)
		srv.ReplaceProjectEnvironmentQueueBindings(w, r, account)
		return w.Code, w.Body.String()
	}
	code, body := put(`{"expected_revision":0,"bindings":[{"name":"orders","queue_name":"orders","mode":"pull","workload_class":"worker","enabled":true,"max_concurrency":2}]}`)
	if code != http.StatusBadRequest || !strings.Contains(body, api.CodeValidation) || !strings.Contains(body, "cannot consume a queue by pull") {
		t.Fatalf("enabled stage pull binding = %d %s; want a 400 naming the missing pull consumer", code, body)
	}
	if code, body := put(`{"expected_revision":0,"bindings":[{"name":"orders","queue_name":"orders","mode":"pull","workload_class":"worker","enabled":false,"max_concurrency":2}]}`); code != http.StatusOK {
		t.Fatalf("disabled stage pull binding = %d %s; want it kept", code, body)
	}
}
