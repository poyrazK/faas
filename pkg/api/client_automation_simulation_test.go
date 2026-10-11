package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSimulateAutomationPreservesSamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/apps/billing/automations:simulate" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		var body SimulateAutomationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || string(body.MockOutputs["a"]) != "null" || string(body.Input) != "9007199254740993" || len(body.MockItemOutputs["batch"]) != 1 || len(body.MockAttempts["retry"]) != 1 || body.MockAttempts["retry"][0].Outcome != "failure" || body.MockAttempts["retry"][0].HTTPStatus == nil || *body.MockAttempts["retry"][0].HTTPStatus != 503 {
			t.Errorf("samples: %+v %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"definition_valid":true,"definition_hash":"hash","complete":false,"issues":[],"warnings":[],"step_order":["a"],"trace":[{"step_name":"a","kind":"run","state":"mocked","output":null}]}`))
	}))
	defer server.Close()
	status := 503
	got, err := NewClient(server.URL, "token").SimulateAutomation(context.Background(), "billing", SimulateAutomationRequest{Definition: WorkflowSpec{Name: "sample", Steps: []WorkflowStepSpec{{Name: "a", Run: "a"}, {Name: "retry", Run: "retry"}}}, Input: json.RawMessage(`9007199254740993`), MockOutputs: map[string]json.RawMessage{"a": json.RawMessage(`null`)}, MockItemOutputs: map[string][]json.RawMessage{"batch": {json.RawMessage(`false`)}}, MockAttempts: map[string][]AutomationSimulationMockAttempt{"retry": {{Outcome: "failure", HTTPStatus: &status}}}})
	if err != nil || !got.DefinitionValid || got.Complete || len(got.Trace) != 1 || string(got.Trace[0].Output) != "null" {
		t.Fatal(got, err)
	}
}
