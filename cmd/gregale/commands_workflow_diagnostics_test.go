// adr: 644
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdWorkflowsDiagnoseReadOnlyRouteAndJSON(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	id := "00000000-0000-4000-8000-000000000001"
	fake := main.AuthedFakeAPI(t, `{"run_id":"`+id+`","workflow_name":"invoice","status":"dead","state_reason":"dead","legacy_unpinned":true,"resume":{"eligible":false,"expected_resume_count":0,"reopened_steps":[],"preserved_steps":[],"blockers":[{"code":"unsafe_mutation","step_name":"charge"}]},"steps":[]}`, http.StatusOK)
	var output bytes.Buffer
	if code := main.CaptureStdoutSwap(t, &output, func() int { return main.CmdWorkflows([]string{"diagnose", id}) }); code != 0 {
		t.Fatalf("exit=%d output=%s", code, output.String())
	}
	if (*fake.SawMethod()) != http.MethodGet || (*fake.SawPath()) != "/v1/workflows/runs/"+id+"/diagnostics" {
		t.Fatalf("wrong request: %s %s", (*fake.SawMethod()), (*fake.SawPath()))
	}
	var result api.WorkflowRunDiagnosticsResponse
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Resume.Blockers[0].Code != "unsafe_mutation" || result.Resume.ExpectedResumeCount != 0 {
		t.Fatalf("JSON=%s", output.String())
	}
}

func TestWorkflowDiagnosticsRenderingAndInvalidArguments(t *testing.T) {
	var output bytes.Buffer
	main.RenderWorkflowRunDiagnostics(&output, api.WorkflowRunDiagnosticsResponse{LegacyUnpinned: true, StateReason: "parked_wait", Resume: api.WorkflowResumePreview{Blockers: []api.WorkflowDiagnosticBlocker{{Code: "handler_executed", StepName: "undo"}}}, Steps: []api.WorkflowDiagnosticStep{{StepName: "approval", Kind: "callback", Status: "awaiting_event"}}})
	for _, text := range []string{"legacy (unpinned)", "parked_wait", "handler_executed", "step undo", "approval  callback  awaiting_event", "Preview only"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("missing %q in %s", text, output.String())
		}
	}
	for _, args := range [][]string{nil, {"bad-id"}, {"00000000-0000-4000-8000-000000000001", "extra"}} {
		code, _ := main.RunWithStderr(t, func() int { return main.CmdWorkflowsDiagnose(args) })
		if code != 1 {
			t.Fatalf("invalid args exit=%d", code)
		}
	}
}

func TestWorkflowDiagnosticsOffersEligibleResumeCommand(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	result := api.WorkflowRunDiagnosticsResponse{RunID: id, Resume: api.WorkflowResumePreview{Eligible: true, ExpectedResumeCount: 2}}
	var output bytes.Buffer
	main.RenderWorkflowRunDiagnostics(&output, result)
	if !strings.Contains(output.String(), "gregale workflow resume "+id+" --expected-resume-count 2") {
		t.Fatalf("missing command: %s", output.String())
	}
	for _, mode := range []string{"ineligible", "blocked", "bad-id", "bad-count"} {
		copy := result
		switch mode {
		case "ineligible":
			copy.Resume.Eligible = false
		case "blocked":
			copy.Resume.Blockers = []api.WorkflowDiagnosticBlocker{{Code: "unsafe_mutation"}}
		case "bad-id":
			copy.RunID = "invalid"
		case "bad-count":
			copy.Resume.ExpectedResumeCount = -1
		}
		output.Reset()
		main.RenderWorkflowRunDiagnostics(&output, copy)
		if strings.Contains(output.String(), "Resume command:") {
			t.Fatalf("offered unsafe command for %s", mode)
		}
	}
}
