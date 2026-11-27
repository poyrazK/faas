package apidserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apisandbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPISandboxScenarioCopiesKeepTheirSeedAndGuide(t *testing.T) {
	e, parent, source := sandboxReleaseFixture(t)
	ctx := t.Context()
	base := "/v1/apps/" + parent.Slug + "/deployments/" + source.ID
	path := base + "/api-sandbox-scenario"
	if got := e.do(t, http.MethodGet, path, nil, nil); got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != `{"scenario":null}` {
		t.Fatalf("empty scenario response: %d %s", got.Code, got.Body)
	}
	definition := api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{"notes":[{"id":42,"body":"private-fixture"}]}`),
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name: "Create another note", SeededIDs: map[string]string{"Sample note": "42"},
			Requests: []api.APISandboxExampleRequest{
				{Title: "Find the note", Method: "GET", Path: "/notes?limit=10", ExpectedStatus: 200},
				{Title: "Add a note", Method: "POST", Path: "/notes", Body: json.RawMessage(`{"body":"another note"}`), ExpectedStatus: 201},
			},
		},
	}
	save := func(input api.APISandboxScenarioInput) api.APISandboxScenario {
		t.Helper()
		response := e.do(t, http.MethodPut, path, input, nil)
		var out api.APISandboxScenarioResponse
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != http.StatusOK || out.Scenario == nil {
			t.Fatalf("save scenario: %d %s (%v)", response.Code, response.Body, err)
		}
		return *out.Scenario
	}
	initial := save(definition)
	if again := save(definition); again.Revision != initial.Revision {
		t.Fatal("retry created a scenario revision for identical content")
	}
	id := uuid.NewString()
	copyPath := base + "/api-sandboxes/" + id
	request := api.APISandboxCreateRequest{ScenarioRevision: initial.Revision}
	if response := e.do(t, http.MethodPut, copyPath, request, nil); response.Code != http.StatusAccepted ||
		strings.Contains(response.Body.String(), "private-fixture") {
		t.Fatalf("create copy response: %d %s", response.Code, response.Body)
	}
	definition.Name = "Changed scenario"
	definition.Seed = json.RawMessage(`{"notes":[{"id":88,"body":"new-fixture"}]}`)
	definition.SeededIDs["Sample note"] = "88"
	updated := save(definition)
	if updated.Revision == initial.Revision {
		t.Fatal("scenario edit did not create a revision")
	}
	if retry := e.do(t, http.MethodPut, copyPath, request, nil); retry.Code != http.StatusOK {
		t.Fatalf("retry after scenario changed: %d %s", retry.Code, retry.Body)
	}
	nextID := uuid.NewString()
	nextPath := base + "/api-sandboxes/" + nextID
	if stale := e.do(t, http.MethodPut, nextPath, request, nil); stale.Code != http.StatusConflict {
		t.Fatalf("stale scenario revision on a new sandbox: %d %s", stale.Code, stale.Body)
	}
	if wrong := e.do(t, http.MethodPut, copyPath, api.APISandboxCreateRequest{ScenarioRevision: updated.Revision}, nil); wrong.Code != http.StatusConflict {
		t.Fatalf("changed an accepted copy: %d %s", wrong.Code, wrong.Body)
	}

	worker := func() {
		t.Helper()
		if err := e.s.processAPISandboxOperation(ctx, dueSandboxOperations{e.store}); err != nil {
			t.Fatal(err)
		}
	}
	sandboxDatabase := func(id string, scenario api.APISandboxScenario) (state.App, string) {
		t.Helper()
		app, err := e.store.AppBySlug(ctx, apiSandboxSlug(id))
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := e.s.describeDevPostgres(ctx, app)
		if err != nil || receipt == nil {
			t.Fatalf("sandbox database receipt: %+v (%v)", receipt, err)
		}
		env, err := e.store.ListAppEnvInScope(ctx, e.acct.ID, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range env {
			if entry.Key == apisandbox.SeedEnvironmentKey && entry.Value == string(scenario.Seed) {
				return app, receipt.DatabaseID
			}
		}
		t.Fatal("sandbox did not receive its pinned fixture")
		return state.App{}, ""
	}
	worker()
	app, databaseID := sandboxDatabase(id, initial)
	if _, err := e.s.managedPostgres.Reconcile(ctx, e.acct.ID, databaseID); err != nil {
		t.Fatal(err)
	}
	bindings, err := e.s.managedPostgresBindings.List(ctx, e.acct.ID, databaseID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("sandbox bindings: %+v (%v)", bindings, err)
	}
	for _, binding := range bindings {
		if _, err := e.s.managedPostgresBindings.Reconcile(ctx, e.acct.ID, binding.ID); err != nil {
			t.Fatal(err)
		}
	}
	worker()
	deployment, err := e.store.LatestDeployment(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	e.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != apisandbox.OpenAPIPath {
			t.Fatalf("scenario ran an example during provisioning: %s %s", r.Method, r.URL.Path)
		}
		headers := make(http.Header)
		headers.Set(api.RevisionHeader, deployment.ID)
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(
			`{"openapi":"3.1.0","info":{"title":"Notes","version":"1"},"paths":{}}`))}, nil
	})}
	worker()
	worker()
	issued := e.do(t, http.MethodPost, copyPath+"/invites", nil, nil)
	var invite api.APISandboxInviteResponse
	if err := json.Unmarshal(issued.Body.Bytes(), &invite); err != nil || issued.Code != http.StatusCreated ||
		strings.Contains(issued.Body.String(), "private-fixture") {
		t.Fatalf("issue invite: %d %s (%v)", issued.Code, issued.Body, err)
	}
	recipient := e.do(t, http.MethodGet, "/v1/api-sandboxes/"+id+"/invite", nil, map[string]string{"Authorization": "Bearer " + invite.Token})
	var receipt api.APISandboxResponse
	if err := json.Unmarshal(recipient.Body.Bytes(), &receipt); err != nil || recipient.Code != http.StatusOK ||
		strings.Contains(recipient.Body.String(), "private-fixture") || receipt.Scenario == nil ||
		receipt.Scenario.Revision != initial.Revision || receipt.Scenario.SeededIDs["Sample note"] != "42" ||
		len(receipt.Scenario.Requests) != 2 || receipt.Scenario.Requests[1].ExpectedStatus != 201 {
		t.Fatalf("recipient did not receive the original guide: %d %s (%v)", recipient.Code, recipient.Body, err)
	}

	if fresh := e.do(t, http.MethodPut, nextPath, api.APISandboxCreateRequest{ScenarioRevision: updated.Revision}, nil); fresh.Code != http.StatusAccepted {
		t.Fatalf("second copy: %d %s", fresh.Code, fresh.Body)
	}
	worker()
	if nextApp, nextDatabaseID := sandboxDatabase(nextID, updated); nextApp.ID == app.ID || nextDatabaseID == databaseID {
		t.Fatal("two fresh copies shared their database or app")
	}
}
