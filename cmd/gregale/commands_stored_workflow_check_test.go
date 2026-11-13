package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storedworkflowtemplates"
	"gopkg.in/yaml.v3"
)

func TestStoredWorkflowStarterScenarios(t *testing.T) {
	for _, name := range storedworkflowtemplates.Names {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if _, err := storedworkflowtemplates.Materialize(name, dir); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadStoredWorkflowScenarios(filepath.Join(dir, "scenarios.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			for _, scenario := range loaded {
				response, err := state.SimulateStoredWorkflow(context.Background(), scenario.request, api.PlanHobby)
				if err != nil {
					t.Fatal(err)
				}
				if result := evaluateStoredWorkflowScenario(scenario, response); !result.Passed {
					t.Fatalf("%s: %+v", scenario.scenario.Name, result)
				}
			}
		})
	}
}

func TestStoredWorkflowScenarioJSONEquality(t *testing.T) {
	for _, pair := range [][2]string{{`1`, `1.0`}, {`1e3`, `1000`}, {`1e1000000`, `10e999999`}, {`-0`, `0.0`}, {`{"n":9007199254740993,"x":null}`, `{"x":null,"n":9007199254740993}`}} {
		if !equalStoredWorkflowScenarioJSON(json.RawMessage(pair[0]), json.RawMessage(pair[1])) {
			t.Fatalf("not equal: %v", pair)
		}
	}
	for _, pair := range [][2]string{{`9007199254740993`, `9007199254740992`}, {`null`, ``}, {`[1,2]`, `[2,1]`}, {`null`, `false`}} {
		if equalStoredWorkflowScenarioJSON(json.RawMessage(pair[0]), json.RawMessage(pair[1])) {
			t.Fatalf("unexpected equality: %v", pair)
		}
	}
}

func TestStoredWorkflowScenarioExpectedOutputsPreserveLargeIntegers(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("{n: 18446744073709551617, empty: null}"), &node); err != nil {
		t.Fatal(err)
	}
	value, err := storedWorkflowScenarioExpectedValue(node.Content[0], 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil || !equalStoredWorkflowScenarioJSON(raw, json.RawMessage(`{"n":18446744073709551617,"empty":null}`)) {
		t.Fatalf("output=%s error=%v", raw, err)
	}
}

func TestStoredWorkflowScenarioCompletenessAndMissingNull(t *testing.T) {
	loaded := loadedStoredWorkflowScenario{scenario: storedWorkflowScenario{Name: "sample", Expect: map[string]storedWorkflowStepExpectation{"record": {State: "mocked"}}}, outputs: map[string]json.RawMessage{"record": json.RawMessage(`null`)}}
	response := api.SimulateStoredWorkflowResponse{DefinitionValid: true, Trace: []api.StoredWorkflowSimulationStep{{StepName: "record", State: "mocked", Output: json.RawMessage(`null`)}}}
	if evaluateStoredWorkflowScenario(loaded, response).Passed {
		t.Fatal("accepted incomplete trace by default")
	}
	partial := false
	loaded.scenario.RequireComplete = &partial
	if !evaluateStoredWorkflowScenario(loaded, response).Passed {
		t.Fatal("rejected deliberate partial trace")
	}
	response.Trace[0].Output = nil
	if evaluateStoredWorkflowScenario(loaded, response).Passed {
		t.Fatal("missing output matched explicit null")
	}
}

func TestCmdStoredWorkflowsCheckReportsAssertionFailure(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	authedFakeAPI(t, `{"definition_valid":true,"complete":true,"issues":[],"warnings":[],"trace":[{"step_name":"record","kind":"run","state":"mocked","output":null}]}`, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	dir := t.TempDir()
	definition := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	suite := filepath.Join(dir, "scenarios.yaml")
	if err := os.WriteFile(suite, []byte("definition: "+definition+"\nscenarios:\n  - name: passing\n    expect:\n      record: {state: mocked, output: null}\n  - name: failing\n    expect:\n      record: {state: skipped}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdStoredWorkflows([]string{"check", "--app", "billing", "--scenarios", suite}); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	var report storedWorkflowCheckReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Passed || len(report.Scenarios) != 2 || !report.Scenarios[0].Passed || report.Scenarios[1].Passed {
		t.Fatalf("report=%s err=%v", output.String(), err)
	}
}

func TestStoredWorkflowScenariosRejectMalformedSuites(t *testing.T) {
	definition := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	for _, body := range []string{
		"scenarios: []\n",
		"scenarios:\n  - name: sample\n    expect:\n      record: {state: typo}\n",
		"scenarios:\n  - name: sample\n    expect:\n      missing: {state: mocked}\n",
		"scenarios:\n  - name: sample\n    expect:\n      record: {unknown: true}\n",
		"scenarios:\n  - name: sample\n    expect: {}\n",
		"scenarios: []\n---\n{}\n",
	} {
		file := filepath.Join(t.TempDir(), "scenarios.yaml")
		if err := os.WriteFile(file, []byte("definition: "+definition+"\n"+body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadStoredWorkflowScenarios(file); err == nil {
			t.Fatalf("accepted %s", strings.TrimSpace(body))
		}
	}
}
