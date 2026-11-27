package apidserver_test

import (
	"encoding/json"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"net/http"
	"strings"
	"testing"
)

func TestVersionedTaskPublicationInvocationAndRollback(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	identity := apidserver.EnableExecutionAPIForTest(t, &e)
	oldIdentity, oldIdentities := *apidserver.MfaIdentity, *apidserver.MfaIdentities
	*apidserver.MfaIdentity = func() *age.X25519Identity { return identity }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.MfaIdentity, *apidserver.MfaIdentities = oldIdentity, oldIdentities })
	(*e.S()).WithExecutionV2Enabled(true).WithExecutionTasksEnabled(true)
	definition := apidserver.ExecutionRequest()
	definition.Input = nil
	pub := api.PublishExecutionTaskRequest{Name: "report", Version: 1, Definition: definition}
	published := e.Do(t, http.MethodPost, "/v1/run-tasks", pub, nil)
	if published.Code != 201 {
		t.Fatal(published.Code, published.Body.String())
	}
	var meta api.ExecutionTaskVersionResponse
	if json.Unmarshal(published.Body.Bytes(), &meta) != nil || meta.ID == "" || strings.Contains(published.Body.String(), definition.Source) {
		t.Fatal("unsafe metadata", published.Body.String())
	}
	pub.Definition.Source += "\n// changed"
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks", pub, nil); r.Code != 409 {
		t.Fatal("mutable version", r.Code, r.Body.String())
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, hash, "task-invoker", []string{api.ScopeRunsRead, api.ScopeRunsWrite}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "task-call"}
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks", pub, headers); r.Code != 403 {
		t.Fatal("narrow publisher", r.Code)
	}
	invoke := api.InvokeExecutionTaskRequest{Name: "report", Version: 1, Input: json.RawMessage(`{"hello":"world"}`)}
	response := e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", invoke, headers)
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	var first api.ExecutionResponse
	_ = json.Unmarshal(response.Body.Bytes(), &first)
	if first.Task == nil || first.Task.ID != meta.ID {
		t.Fatal("missing task lineage", response.Body.String())
	}
	(*e.S()).WithExecutionTasksEnabled(false)
	if r := e.Do(t, http.MethodDelete, "/v1/run-tasks/report/versions/1", nil, nil); r.Code != 200 {
		t.Fatal("retirement", r.Code, r.Body.String())
	}
	response = e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", invoke, headers)
	var replay api.ExecutionResponse
	_ = json.Unmarshal(response.Body.Bytes(), &replay)
	if response.Code != 202 || replay.ID != first.ID || response.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("rollback replay", response.Code, response.Body.String())
	}
	headers["Idempotency-Key"] = "new-call"
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", invoke, headers); r.Code != 503 {
		t.Fatal("writer gate", r.Code)
	}
	(*e.S()).WithExecutionTasksEnabled(true)
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", invoke, headers); r.Code != 404 {
		t.Fatal("retired new invocation", r.Code, r.Body.String())
	}
	version, err := (*e.Store()).ExecutionTaskVersionByName(t.Context(), e.Acct().ID, "report", 1)
	if err != nil || len(version.SealedDefinition) != 0 {
		t.Fatal("retired source retained", err)
	}
}
