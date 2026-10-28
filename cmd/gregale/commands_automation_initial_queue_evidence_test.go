package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1272
func TestAutomationInitialQueueEvidenceCLI(t *testing.T) {
	calls := 0
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/recovery-policy/queue" {
			t.Fatal("extra action during queue read")
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryAutomaticQueue{Entries: []api.WorkflowRecoveryAutomaticQueueEntry{{InitialEvidence: &api.WorkflowRecoveryInitialQueueEvidence{Stage: "prepared", Diagnosis: api.WorkflowInitialEvidenceDiagnosis{ResumeCount: 1, Reason: "not_submitted_automatic_initial"}, CLICommand: "gregale automation failure-recovery-evidence --app consumer --review review", EvidenceURL: "/initial-recovery-reviews/review/evidence"}}}})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomationsRecoveryPolicy([]string{"--app", "consumer", "--queue"}); code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"Initial evidence: prepared", "target generation 1", "repairable false", "no recovery job submitted", "failure-recovery-evidence", "/initial-recovery-reviews/review/evidence"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	if calls != 1 {
		t.Fatal("extra inspection or repair")
	}
}
