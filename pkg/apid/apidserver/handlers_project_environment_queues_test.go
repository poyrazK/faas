// adr: 590
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

func TestProjectEnvironmentQueueAPIIsolatesCompleteConfiguration(t *testing.T) {
	srv, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	class := state.WorkloadClassWorker
	var err error
	app, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{WorkloadClass: &class})
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"stage", "other"} {
		if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: slug}); err != nil {
			t.Fatal(err)
		}
	}
	prod, err := store.CreateQueueBinding(t.Context(), state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, projectSlug, environment, workload, query, body string, owner state.Account) *httptest.ResponseRecorder {
		t.Helper()
		path := "/v1/projects/" + projectSlug + "/environments/" + environment + "/workloads/" + workload + "/queue-bindings" + query
		r, w := apidserver.ProjectRequest(method, path, projectSlug, []byte(body))
		r.SetPathValue("environment", environment)
		r.SetPathValue("workload", workload)
		if method == http.MethodGet {
			srv.GetProjectEnvironmentQueueBindings(w, r, owner)
		} else {
			srv.ReplaceProjectEnvironmentQueueBindings(w, r, owner)
		}
		return w
	}
	get := func(environment string) *httptest.ResponseRecorder {
		return call(http.MethodGet, project.Slug, environment, app.Slug, "", "", account)
	}
	put := func(body string) *httptest.ResponseRecorder {
		return call(http.MethodPut, project.Slug, "stage", app.Slug, "", body, account)
	}
	absent := get("stage")
	apidserver.AssertProblem(t, absent, http.StatusConflict, "environment_queue_collection_unavailable")
	if absent.Header().Get("X-Gregale-Workload-Revision") != "0" {
		t.Fatal("missing collection omitted the initial workload revision")
	}
	body := `{"expected_revision":0,"bindings":[{"name":"orders","queue_name":"orders","mode":"pull","workload_class":"worker","enabled":false,"max_concurrency":2,"retry_policy":{"max_attempts":3}}]}`
	configured := put(body)
	var response api.ProjectEnvironmentQueueBindingsResponse
	if err := json.Unmarshal(configured.Body.Bytes(), &response); err != nil || configured.Code != http.StatusOK || response.WorkloadRevision != 1 || response.Revision != 1 || response.ActivationState != "unavailable" || len(response.Bindings) != 1 || response.Bindings[0].Enabled || response.Bindings[0].MaxConcurrency != 2 {
		t.Fatalf("stage configuration = %d %s, %v", configured.Code, configured.Body.String(), err)
	}
	if configured.Header().Get("X-Gregale-Workload-Revision") != "1" || response.ConfigHash != configured.Header().Get("X-Gregale-Workload-Config-Hash") || len(response.ConfigHash) != 64 || strings.Contains(configured.Body.String(), prod.ID) {
		t.Fatal("stage response lacks configuration identity or exposes production identity")
	}
	if listed := get("stage"); listed.Code != http.StatusOK || listed.Body.String() != configured.Body.String() {
		t.Fatalf("stage read = %d %s", listed.Code, listed.Body.String())
	}
	apidserver.AssertProblem(t, get("other"), http.StatusConflict, "environment_queue_collection_unavailable")
	apidserver.AssertProblem(t, put(body), http.StatusConflict, api.CodeConflict)
	body = strings.Replace(body, `"expected_revision":0`, `"expected_revision":1`, 1)
	if noop := put(body); noop.Code != http.StatusOK || noop.Body.String() != configured.Body.String() {
		t.Fatalf("no-op changed stage head: %d %s", noop.Code, noop.Body.String())
	}
	if _, err := store.UpdateProjectEnvironmentProtection(t.Context(), account.ID, project.ID, "stage", true); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, put(body), http.StatusConflict, api.CodeConflict)
	if read := get("stage"); read.Code != http.StatusOK {
		t.Fatalf("protected stage read failed: %d %s", read.Code, read.Body.String())
	}
	if _, err := store.UpdateProjectEnvironmentProtection(t.Context(), account.ID, project.ID, "stage", false); err != nil {
		t.Fatal(err)
	}
	cleared := put(`{"expected_revision":1,"bindings":[]}`)
	if err := json.Unmarshal(cleared.Body.Bytes(), &response); err != nil || cleared.Code != http.StatusOK || response.Bindings == nil || len(response.Bindings) != 0 || response.Revision != 2 || response.WorkloadRevision != 2 {
		t.Fatalf("complete empty stage configuration = %d %s, %v", cleared.Code, cleared.Body.String(), err)
	}
	if current, err := store.QueueBindingByID(t.Context(), account.ID, app.ID, prod.ID); err != nil || current.MaxConcurrency != 4 || !current.Enabled {
		t.Fatalf("stage edit changed production: %+v, %v", current, err)
	}
	for _, invalid := range []string{
		`{}`, `{"bindings":[]}`, `{"expected_revision":2}`, `{"expected_revision":-1,"bindings":[]}`, `{"expected_revision":2,"bindings":null}`,
		`{"expected_revision":2,"bindings":[],"source_id":"foreign"}`, `{"expected_revision":2,"bindings":[]} {}`,
		`{"expected_revision":2,"bindings":[{"name":"orders","queue_name":"orders","mode":"pull","workload_class":"worker","enabled":true,"max_concurrency":-1}]}`,
		`{"expected_revision":2,"bindings":[{"name":"orders","queue_name":"orders","mode":"pull","workload_class":"worker","enabled":true,"max_concurrency":1,"retry_policy":{"unknown":1}}]}`,
	} {
		apidserver.AssertProblem(t, put(invalid), http.StatusBadRequest, api.CodeValidation)
	}
	for _, environment := range []string{"production", "default", "bad!"} {
		apidserver.AssertProblem(t, get(environment), http.StatusBadRequest, api.CodeValidation)
	}
	apidserver.AssertProblem(t, get("unknown"), http.StatusNotFound, api.CodeNotFound)
	for _, query := range []string{"?environment=other", "?scope=stage"} {
		apidserver.AssertProblem(t, call(http.MethodPut, project.Slug, "stage", app.Slug, query, body, account), http.StatusBadRequest, api.CodeValidation)
	}
	foreign, err := store.CreateAccount(t.Context(), "foreign-stage-queues@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, call(http.MethodGet, project.Slug, "stage", app.Slug, "", "", foreign), http.StatusNotFound, api.CodeNotFound)
	otherProject, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "other-project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: otherProject.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, call(http.MethodPut, otherProject.Slug, "stage", app.Slug, "", body, account), http.StatusNotFound, api.CodeNotFound)
}

func TestProjectEnvironmentQueueAPIValidatesFunctionAndPlan(t *testing.T) {
	srv, store, account, project, _ := apidserver.NewProjectLifecycleFixture(t)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "queue-function", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	body := `{"expected_revision":0,"bindings":[{"name":"orders","queue_name":"orders","mode":"push","workload_class":"http","enabled":true,"max_concurrency":2}]}`
	call := func(body string, owner state.Account) *httptest.ResponseRecorder {
		r, w := apidserver.ProjectRequest(http.MethodPut, "/v1/projects/shop/environments/stage/workloads/queue-function/queue-bindings", project.Slug, []byte(body))
		r.SetPathValue("environment", "stage")
		r.SetPathValue("workload", app.Slug)
		srv.ReplaceProjectEnvironmentQueueBindings(w, r, owner)
		return w
	}
	apidserver.AssertProblem(t, call(strings.Replace(body, `"mode":"push"`, `"mode":"pull"`, 1), account), http.StatusBadRequest, api.CodeValidation)
	free := account
	free.Plan = api.PlanFree
	if response := call(body, free); response.Code != http.StatusPaymentRequired {
		t.Fatalf("push configuration bypassed plan: %d %s", response.Code, response.Body.String())
	}
	if response := call(body, account); response.Code != http.StatusOK {
		t.Fatalf("function push definition rejected: %d %s", response.Code, response.Body.String())
	}
	if triggers, err := store.ListTriggersForApp(t.Context(), app.ID); err != nil || len(triggers) != 0 {
		t.Fatalf("desired queue configuration activated a global consumer: %+v, %v", triggers, err)
	}
}

func TestProjectEnvironmentQueueAPIReadScopeIsNarrow(t *testing.T) {
	env := apidserver.SetupWithScopes(t, []string{api.ScopeProjectEnvironmentRead})
	project, err := (*env.Store()).CreateProject(t.Context(), state.Project{AccountID: (*env.Acct()).ID, Slug: "queue-scope"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*env.Store()).CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: (*env.Acct()).ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	app, err := (*env.Store()).CreateApp(t.Context(), state.App{AccountID: (*env.Acct()).ID, ProjectID: project.ID, Slug: "queue-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(t.Context(), (*env.Store()), app, "stage", 0, nil); err != nil {
		t.Fatal(err)
	}
	path := "/v1/projects/queue-scope/environments/stage/workloads/queue-worker/queue-bindings"
	if response := env.Do(t, http.MethodGet, path, nil, nil); response.Code != http.StatusOK {
		t.Fatalf("environment read key rejected: %d %s", response.Code, response.Body.String())
	}
	apidserver.AssertProblem(t, env.Do(t, http.MethodPut, path, map[string]any{"expected_revision": 1, "bindings": []any{}}, nil), http.StatusForbidden, api.CodeForbidden)
	apidserver.AssertProblem(t, env.Do(t, http.MethodGet, "/v1/apps/queue-worker/queue-bindings", nil, nil), http.StatusForbidden, api.CodeForbidden)
}

func TestLegacyQueueAPINeverDiscardsStageSelection(t *testing.T) {
	srv, store, account, _, app := apidserver.NewProjectLifecycleFixture(t)
	class := state.WorkloadClassWorker
	var err error
	app, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{WorkloadClass: &class})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBinding(t.Context(), state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, handler := range []struct {
		name    string
		method  string
		handler func(http.ResponseWriter, *http.Request, state.Account)
	}{
		{"list", http.MethodGet, srv.ListQueueBindings}, {"get", http.MethodGet, srv.GetQueueBinding},
		{"status", http.MethodGet, srv.GetQueueBindingStatus}, {"create", http.MethodPost, srv.CreateQueueBinding},
		{"update", http.MethodPatch, srv.UpdateQueueBinding}, {"delete", http.MethodDelete, srv.DeleteQueueBinding},
		{"profile", http.MethodPut, srv.ConfigureQueueWorkload},
	} {
		t.Run(handler.name, func(t *testing.T) {
			for _, query := range []string{"?environment=stage", "?environment=", "?environment=production&environment=stage", "?scope=stage"} {
				r, w := apidserver.ProjectRequest(handler.method, "/v1/apps/"+app.Slug+"/queue-bindings"+query, app.Slug, []byte(`{}`))
				r.SetPathValue("id", binding.ID)
				handler.handler(w, r, account)
				apidserver.AssertProblem(t, w, http.StatusBadRequest, api.CodeValidation)
			}
		})
	}
	if current, err := store.QueueBindingByID(t.Context(), account.ID, app.ID, binding.ID); err != nil || current.MaxConcurrency != 4 || !current.Enabled {
		t.Fatalf("stage query touched production: %+v, %v", current, err)
	}
	for _, query := range []string{"", "?environment=production"} {
		r, w := apidserver.ProjectRequest(http.MethodGet, "/v1/apps/"+app.Slug+"/queue-bindings"+query, app.Slug, nil)
		srv.ListQueueBindings(w, r, account)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), binding.ID) {
			t.Fatalf("production query = %d %s", w.Code, w.Body.String())
		}
	}
	// Validate selection before reserving or replaying a legacy receipt: that
	// receipt's identity intentionally preserves the old method/path namespace.
	handler := apidserver.ProductionQueueBindingHandler(srv.Idempotent(srv.CreateQueueBinding))
	create := func(query, key string) *httptest.ResponseRecorder {
		r, w := apidserver.ProjectRequest(http.MethodPost, "/v1/apps/"+app.Slug+"/queue-bindings"+query, app.Slug, []byte(`{"name":"exports","queue_name":"exports","mode":"pull","workload_class":"worker"}`))
		r.Header.Set("Idempotency-Key", key)
		handler(w, r, account)
		return w
	}
	apidserver.AssertProblem(t, create("?environment=stage", "queue-selection-key"), http.StatusBadRequest, api.CodeValidation)
	created := create("", "queue-selection-key")
	if created.Code != http.StatusCreated {
		t.Fatalf("stage rejection poisoned production receipt: %d %s", created.Code, created.Body.String())
	}
	apidserver.AssertProblem(t, create("?environment=stage", "queue-selection-key"), http.StatusBadRequest, api.CodeValidation)
	if replay := create("?environment=production", "queue-selection-key"); replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != created.Body.String() {
		t.Fatalf("production replay changed: %d %s", replay.Code, replay.Body.String())
	}
}
