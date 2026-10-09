// adr: 644
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdWorkflowsDiagnoseReadOnlyRouteAndJSON(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	id := "00000000-0000-4000-8000-000000000001"
	fake := authedFakeAPI(t, `{"run_id":"`+id+`","workflow_name":"invoice","status":"dead","state_reason":"dead","legacy_unpinned":true,"resume":{"eligible":false,"expected_resume_count":0,"reopened_steps":[],"preserved_steps":[],"blockers":[{"code":"unsafe_mutation","step_name":"charge"}]},"steps":[]}`, http.StatusOK)
	var output bytes.Buffer
	if code := captureStdoutSwap(t, &output, func() int { return cmdWorkflows([]string{"diagnose", id}) }); code != 0 {
		t.Fatalf("exit=%d output=%s", code, output.String())
	}
	if fake.sawMethod != http.MethodGet || fake.sawPath != "/v1/workflows/runs/"+id+"/diagnostics" {
		t.Fatalf("wrong request: %s %s", fake.sawMethod, fake.sawPath)
	}
	var result api.WorkflowRunDiagnosticsResponse
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Resume.Blockers[0].Code != "unsafe_mutation" || result.Resume.ExpectedResumeCount != 0 {
		t.Fatalf("JSON=%s", output.String())
	}
}

func TestWorkflowDiagnosticsRenderingAndInvalidArguments(t *testing.T) {
	var output bytes.Buffer
	renderWorkflowRunDiagnostics(&output, api.WorkflowRunDiagnosticsResponse{LegacyUnpinned: true, StateReason: "parked_wait", Resume: api.WorkflowResumePreview{Blockers: []api.WorkflowDiagnosticBlocker{{Code: "handler_executed", StepName: "undo"}}}, Steps: []api.WorkflowDiagnosticStep{{StepName: "approval", Kind: "callback", Status: "awaiting_event"}}})
	for _, text := range []string{"legacy (unpinned)", "parked_wait", "handler_executed", "step undo", "approval  callback  awaiting_event", "Preview only"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("missing %q in %s", text, output.String())
		}
	}
	for _, args := range [][]string{nil, {"bad-id"}, {"00000000-0000-4000-8000-000000000001", "extra"}} {
		code, _ := runWithStderr(t, func() int { return cmdWorkflowsDiagnose(args) })
		if code != 1 {
			t.Fatalf("invalid args exit=%d", code)
		}
	}
}
