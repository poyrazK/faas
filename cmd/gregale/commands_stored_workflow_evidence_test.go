package main_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestStoredWorkflowPublicationEvidencePrivacyAndCoverage(t *testing.T) {
	report := main.StoredWorkflowCheckedPublishReport{CheckedVersion: 7, DefinitionHash: strings.Repeat("a", 64), Checks: main.StoredWorkflowCheckReport{Scenarios: []main.StoredWorkflowScenarioResult{{Name: "success", Passed: true, DefinitionValid: true, Complete: true, Failures: []string{"private-error"}}}, CoverageHints: []main.StoredWorkflowCoverageHint{{Step: "send", Code: "guard_skip_missing", Message: "private-message"}}, Coverage: &main.StoredWorkflowCoverageGate{Required: true, Passed: true, Exclusions: []main.StoredWorkflowCoverageExclusion{{Step: "send", Code: "guard_skip_missing", Reason: "approved"}}}}}
	evidence := main.StoredWorkflowPublicationEvidence(report, time.Now().UTC())
	raw, _ := json.Marshal(evidence)
	if strings.Contains(string(raw), "private") || evidence.CoverageRemaining != 0 || !evidence.CoveragePassed || !evidence.CoverageRequired || len(evidence.Exclusions) != 1 {
		t.Fatalf("unsafe or incorrect evidence: %s", raw)
	}
	report.Checks.Coverage = nil
	evidence = main.StoredWorkflowPublicationEvidence(report, time.Now().UTC())
	if evidence.CoverageRemaining != 1 || evidence.CoveragePassed || evidence.CoverageRequired {
		t.Fatalf("advisory coverage lost: %+v", evidence)
	}
}

func TestStoredWorkflowRevisionEvidenceDisplay(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[jsonMode], func(t *testing.T) {
			main.ResetJSONOut(t)
			(*main.JsonOutput) = jsonMode
			output := main.CaptureStoredWorkflowStdout(t)
			revision := api.StoredWorkflowRevisionResponse{Version: 9, DefinitionHash: strings.Repeat("a", 64), RecordedAt: time.Now(), Definition: api.WorkflowSpec{Name: "test", Steps: []api.WorkflowStepSpec{{Name: "send", Path: "/send", Input: json.RawMessage(`{"secret":"private-payload"}`)}}}, CheckEvidence: &api.StoredWorkflowCheckEvidence{CheckedVersion: 7, DefinitionHash: strings.Repeat("a", 64), CheckedAt: time.Now(), CoverageRequired: true, CoveragePassed: true, Scenarios: []api.StoredWorkflowCheckScenario{{Name: "success", Passed: true, DefinitionValid: true, Complete: true}}, Exclusions: []api.StoredWorkflowCheckExclusion{{Step: "send", Code: "guard_skip_missing", Reason: "reviewed"}}}}
			if main.PrintStoredWorkflowRevisionDetails(revision, "") != 0 {
				t.Fatal("display failed")
			}
			if strings.Contains(output.String(), "private-payload") || !strings.Contains(output.String(), "success") || !strings.Contains(output.String(), "reviewed") {
				t.Fatalf("incorrect revision output: %s", output.String())
			}
			if !jsonMode && !strings.Contains(output.String(), "Client-reported") {
				t.Fatal("missing provenance label")
			}
		})
	}
}
