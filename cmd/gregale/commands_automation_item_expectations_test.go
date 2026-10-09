package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func itemExpectationFixture(t *testing.T, policy, expectations string) string {
	t.Helper()
	if policy == "stop" {
		policy = ""
	}
	dir := t.TempDir()
	definition := "name: batch\nsteps:\n  - name: sync\n    for_each:\n      items: input.items\n      on_item_failure: " + policy + "\n      action:\n        run: send\n        retry: {max_attempts: 2}\n        when: {ref: input.item.active, op: eq, value: true}\n"
	fixtures := map[string]string{
		"automation.yaml": definition,
		"input.json":      `{"items":[{"active":true},{"active":false},{"active":true}]}`,
		"attempts.json":   `{"sync":{"0":[{"outcome":"failure","http_status":503},{"outcome":"success","output":{"id":9007199254740993}}],"2":[{"outcome":"success","output":null}]}}`,
		"scenarios.yaml":  "definition: automation.yaml\nscenarios:\n  - name: items\n    input_file: input.json\n    mock_item_attempts_file: attempts.json\n" + expectations,
	}
	for name, contents := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "scenarios.yaml")
}

func TestAutomationItemExpectationsRealSimulation(t *testing.T) {
	suite := itemExpectationFixture(t, "continue", `    expect:
      sync: {state: resolved, output: [{id: 9007199254740993}, null, null]}
    expect_items:
      sync:
        "0":
          state: mocked
          when_matched: true
          output: {id: 9007199254740993}
          attempt_count: 2
          attempts: [{outcome: failure, http_status: 503}, {outcome: success}]
        "1": {state: skipped, when_matched: false, attempt_count: 0, attempts: []}
        "2": {state: mocked, output: null, attempts: [{outcome: success}]}
`)
	scenarios, err := loadAutomationScenarios(suite)
	if err != nil {
		t.Fatal(err)
	}
	response, err := state.SimulateAutomation(context.Background(), scenarios[0].request, api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if result := evaluateAutomationScenario(scenarios[0], response); !result.Passed {
		t.Fatalf("result=%+v", result)
	}
	// Keep the root's correct collected result while changing an individual item.
	for i := range response.Trace {
		if response.Trace[i].ParentStep == "sync" && response.Trace[i].ItemIndex != nil && *response.Trace[i].ItemIndex == 0 {
			response.Trace[i].Output = json.RawMessage(`{"id":9007199254740992,"token":"private-output"}`)
		}
	}
	result := evaluateAutomationScenario(scenarios[0], response)
	if result.Passed || len(result.Failures) != 1 || !strings.Contains(result.Failures[0], `loop "sync" item 0 output`) {
		t.Fatalf("missed incorrect item: %+v", result)
	}
	for _, failure := range result.Failures {
		if strings.Contains(failure, "private-output") {
			t.Fatal("leaked item output")
		}
	}
}

func TestAutomationItemExpectationsFailurePolicies(t *testing.T) {
	for _, policy := range []string{"stop", "continue"} {
		t.Run(policy, func(t *testing.T) {
			suite := itemExpectationFixture(t, policy, "    expect_items:\n      sync:\n        \"0\": {state: failed, attempts: [{outcome: failure, http_status: 400}]}\n")
			scenarios, err := loadAutomationScenarios(suite)
			if err != nil {
				t.Fatal(err)
			}
			status := 400
			scenarios[0].request.MockItemAttempts["sync"]["0"] = []api.AutomationSimulationMockAttempt{{Outcome: "failure", HTTPStatus: &status}}
			response, err := state.SimulateAutomation(context.Background(), scenarios[0].request, api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			if result := evaluateAutomationScenario(scenarios[0], response); !result.Passed {
				t.Fatal(result)
			}
			itemState := "skipped"
			output := `[]`
			if policy == "continue" {
				itemState = "mocked"
				output = `[null,null,null]`
			}
			scenarios[0].scenario.ExpectItems["sync"]["2"] = automationStepExpectation{State: itemState}
			scenarios[0].scenario.Expect = map[string]automationStepExpectation{"sync": {State: "failed"}}
			scenarios[0].outputs = map[string]json.RawMessage{"sync": json.RawMessage(output)}
			if result := evaluateAutomationScenario(scenarios[0], response); !result.Passed {
				t.Fatalf("policy=%s result=%+v", policy, result)
			}
		})
	}
}

func TestAutomationItemExpectationsRejectInvalidSuites(t *testing.T) {
	for _, expect := range []string{
		"missing: {\"0\": {state: mocked}}", "sync: {}", "sync: {\"-1\": {state: mocked}}", "sync: {\"01\": {state: mocked}}", "sync: {\"128\": {state: mocked}}", "sync: {\"0\": {}}", "sync: {\"0\": {state: invalid}}", "sync: {\"0\": {attempt_count: -1}}", "sync: {\"0\": {attempt_count: 26}}", "sync: {\"0\": {attempt_count: 1, attempts: []}}", "sync: {\"0\": {attempts: [{}]}}", "sync: {\"0\": {attempts: [{outcome: unknown}]}}", "sync: {\"0\": {attempts: [{outcome: success, http_status: 200}]}}", "sync: {\"0\": {attempts: [{outcome: failure, http_status: 200}]}}", "sync: {\"0\": {attempts: [{outcome: failure, unknown: true}]}}",
	} {
		suite := itemExpectationFixture(t, "continue", "    expect_items:\n      "+expect+"\n")
		if _, err := loadAutomationScenarios(suite); err == nil {
			t.Fatalf("accepted %s", expect)
		}
	}
	// Root action names cannot be used as loop item selectors.
	definition := writeAutomationFixture(t, automationDefinitionYAML)
	suite := filepath.Join(t.TempDir(), "suite.yaml")
	if err := os.WriteFile(suite, []byte("definition: "+definition+"\nscenarios:\n  - name: invalid\n    expect_items:\n      record: {\"0\": {state: mocked}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAutomationScenarios(suite); err == nil {
		t.Fatal("accepted non-loop selector")
	}
}

func TestAutomationItemExpectationsTraceMatching(t *testing.T) {
	suite := itemExpectationFixture(t, "continue", "    expect_items:\n      sync:\n        \"0\": {state: mocked, output: null, attempts: [{outcome: failure, http_status: 503}, {outcome: success}]}\n")
	loaded, err := loadAutomationScenarios(suite)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "missing", "wrong_parent", "duplicate", "missing_null", "wrong_attempt_order", "wrong_status"} {
		t.Run(mode, func(t *testing.T) {
			index := 0
			status := 503
			row := api.AutomationSimulationStep{StepName: "arbitrary-internal-name", ParentStep: "sync", ItemIndex: &index, State: "mocked", Output: json.RawMessage(`null`), Attempts: []api.AutomationSimulationAttempt{{Attempt: 1, Outcome: "failure", HTTPStatus: &status}, {Attempt: 2, Outcome: "success"}}}
			response := api.SimulateAutomationResponse{DefinitionValid: true, Complete: true, Trace: []api.AutomationSimulationStep{row}}
			switch mode {
			case "missing":
				response.Trace = nil
			case "wrong_parent":
				response.Trace[0].ParentStep = "other"
			case "duplicate":
				response.Trace = append(response.Trace, row)
			case "missing_null":
				response.Trace[0].Output = nil
			case "wrong_attempt_order":
				response.Trace[0].Attempts[0].Attempt = 2
			case "wrong_status":
				status = 400
			}
			result := evaluateAutomationScenario(loaded[0], response)
			if result.Passed != (mode == "success") {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestAutomationItemExpectationsBlockCheckedPublish(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	output := captureAutomationStdout(t)
	suite := itemExpectationFixture(t, "continue", "    expect_items:\n      sync:\n        \"0\": {state: mocked, output: {id: 9007199254740992}}\n")
	scenarios, err := loadAutomationScenarios(suite)
	if err != nil {
		t.Fatal(err)
	}
	published := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, ":publish-policy"):
			_ = json.NewEncoder(w).Encode(api.AutomationPublishPolicy{Mode: "optional"})

		case r.Method == "GET":
			_ = json.NewEncoder(w).Encode(api.AutomationResponse{Name: "batch", Version: 7, Draft: scenarios[0].request.Definition})
		case strings.HasSuffix(r.URL.Path, ":simulate"):
			response, err := state.SimulateAutomation(context.Background(), scenarios[0].request, api.PlanHobby)
			if err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(response)
		default:
			published = true
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	if code := publishAutomationWithScenarios(api.NewClient(server.URL, "token"), "billing", "batch", 7, false, scenarios); code == 0 || published {
		t.Fatalf("exit=%d published=%t", code, published)
	}
	var report automationCheckedPublishReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Checks.Passed || report.PublicationAttempted || !strings.Contains(output.String(), `item 0 output does not match`) {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
