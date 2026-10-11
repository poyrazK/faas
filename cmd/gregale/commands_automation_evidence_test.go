package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationPublicationEvidencePrivacyAndCoverage(t *testing.T) {
	report := automationCheckedPublishReport{CheckedVersion: 7, DefinitionHash: strings.Repeat("a", 64), Checks: automationCheckReport{Scenarios: []automationScenarioResult{{Name: "success", Passed: true, DefinitionValid: true, Complete: true, Failures: []string{"private-error"}}}, CoverageHints: []automationCoverageHint{{Step: "send", Code: "guard_skip_missing", Message: "private-message"}}, Coverage: &automationCoverageGate{Required: true, Passed: true, Exclusions: []automationCoverageExclusion{{Step: "send", Code: "guard_skip_missing", Reason: "approved"}}}}}
	evidence := automationPublicationEvidence(report, time.Now().UTC())
	raw, _ := json.Marshal(evidence)
	if strings.Contains(string(raw), "private") || evidence.CoverageRemaining != 0 || !evidence.CoveragePassed || !evidence.CoverageRequired || len(evidence.Exclusions) != 1 {
		t.Fatalf("unsafe or incorrect evidence: %s", raw)
	}
	report.Checks.Coverage = nil
	evidence = automationPublicationEvidence(report, time.Now().UTC())
	if evidence.CoverageRemaining != 1 || evidence.CoveragePassed || evidence.CoverageRequired {
		t.Fatalf("advisory coverage lost: %+v", evidence)
	}
}

func TestAutomationRevisionEvidenceDisplay(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[jsonMode], func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = jsonMode
			output := captureAutomationStdout(t)
			revision := api.AutomationRevisionResponse{Version: 9, DefinitionHash: strings.Repeat("a", 64), RecordedAt: time.Now(), Definition: api.WorkflowSpec{Name: "test", Steps: []api.WorkflowStepSpec{{Name: "send", Path: "/send", Input: json.RawMessage(`{"secret":"private-payload"}`)}}}, CheckEvidence: &api.AutomationCheckEvidence{CheckedVersion: 7, DefinitionHash: strings.Repeat("a", 64), CheckedAt: time.Now(), CoverageRequired: true, CoveragePassed: true, Scenarios: []api.AutomationCheckScenario{{Name: "success", Passed: true, DefinitionValid: true, Complete: true}}, Exclusions: []api.AutomationCheckExclusion{{Step: "send", Code: "guard_skip_missing", Reason: "reviewed"}}}}
			if printAutomationRevisionDetails(revision, "") != 0 {
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
