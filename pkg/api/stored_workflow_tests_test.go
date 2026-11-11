package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStoredWorkflowTestInputsHash(t *testing.T) {
	def := WorkflowSpec{Name: "tests", Steps: []WorkflowStepSpec{{Name: "main", Path: "/handler"}}}
	suite := StoredWorkflowTestSuite{Sample: StoredWorkflowTestInputs{Input: json.RawMessage(`{"b":2,"a":9007199254740993}`)}}
	original, err := StoredWorkflowTestInputsHash(def, suite)
	if err != nil {
		t.Fatal(err)
	}
	suite.Sample.Input = json.RawMessage(`{ "a":9007199254740993, "b":2 }`)
	reordered, _ := StoredWorkflowTestInputsHash(def, suite)
	if original != reordered {
		t.Fatal("object ordering changed hash")
	}
	suite.Sample.Input = json.RawMessage(`{"a":9007199254740992,"b":2}`)
	changed, _ := StoredWorkflowTestInputsHash(def, suite)
	if changed == original {
		t.Fatal("large integer precision lost")
	}
	inputs := TestInputsFromSimulation(SimulateStoredWorkflowRequest{Definition: def, Input: json.RawMessage(`{}`)})
	raw, _ := json.Marshal(inputs)
	if strings.Contains(string(raw), "definition") {
		t.Fatal("saved test carries a definition")
	}
}
