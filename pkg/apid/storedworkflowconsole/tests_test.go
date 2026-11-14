package storedworkflowconsole

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestSavedTestsTolerateJSONBNumericNormalization(t *testing.T) {
	def := api.WorkflowSpec{Name: "numbers", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/handler", Input: json.RawMessage(`{"amount":100}`)}}}
	suite := api.StoredWorkflowTestSuite{Sample: api.StoredWorkflowTestInputs{Input: json.RawMessage(`{"id":9007199254740993,"amount":100}`)}, Cases: []api.StoredWorkflowTestCase{}}
	hash, err := api.StoredWorkflowTestInputsHash(def, suite)
	if err != nil {
		t.Fatal(err)
	}
	saved := api.StoredWorkflowResponse{Name: def.Name, Draft: def, TestSuite: &suite, TestReport: &api.StoredWorkflowTestReport{InputsHash: hash}}
	data := dashboard.StoredWorkflowConsoleData{Name: def.Name, Definition: `{"name":"numbers","steps":[{"name":"main","path":"/handler","input":{"amount":1e2}}]}`, Sample: `{"input":{"amount":1e2,"id":9007199254740993}}`, Scenarios: `[]`}
	testMetadata(&data, saved)
	if !savedTestInputsMatch(data, saved) || !data.TestReportFresh {
		t.Fatal("stored numeric normalization was treated as an unsaved edit")
	}
	data.Sample = `{"input":{"amount":100,"id":9007199254740992}}`
	testMetadata(&data, saved)
	if savedTestInputsMatch(data, saved) || data.TestReportFresh {
		t.Fatal("large integer precision was lost")
	}
	data.Sample = `{"input":{"amount":100,"id":9007199254740993}}`
	data.Definition = `{"name":"numbers","steps":[{"name":"main","path":"/other","input":{"amount":100}}]}`
	testMetadata(&data, saved)
	if data.TestReportFresh {
		t.Fatal("changed definition kept a current report")
	}
}
