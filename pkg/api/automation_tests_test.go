package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAutomationTestInputsHash(t *testing.T) {
	def := WorkflowSpec{Name: "tests", Steps: []WorkflowStepSpec{{Name: "main", Path: "/handler"}}}
	suite := AutomationTestSuite{Sample: AutomationTestInputs{Input: json.RawMessage(`{"b":2,"a":9007199254740993}`)}}
	original, err := AutomationTestInputsHash(def, suite)
	if err != nil {
		t.Fatal(err)
	}
	suite.Sample.Input = json.RawMessage(`{ "a":9007199254740993, "b":2 }`)
	reordered, _ := AutomationTestInputsHash(def, suite)
	if original != reordered {
		t.Fatal("object ordering changed hash")
	}
	suite.Sample.Input = json.RawMessage(`{"a":9007199254740992,"b":2}`)
	changed, _ := AutomationTestInputsHash(def, suite)
	if changed == original {
		t.Fatal("large integer precision lost")
	}
	inputs := TestInputsFromSimulation(SimulateAutomationRequest{Definition: def, Input: json.RawMessage(`{}`)})
	raw, _ := json.Marshal(inputs)
	if strings.Contains(string(raw), "definition") {
		t.Fatal("saved test carries a definition")
	}
}
