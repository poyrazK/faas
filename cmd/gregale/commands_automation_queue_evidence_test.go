package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1271
func TestAutomationQueueEvidenceCLI(t *testing.T) {
	calls := 0
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/recovery-policy/queue" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryAutomaticQueue{Entries: []api.WorkflowRecoveryAutomaticQueueEntry{{WaitCategory: "policy", WaitReason: "automatic_paused", Evidence: &api.WorkflowRecoveryQueueEvidence{Diagnosis: api.WorkflowRetryEvidenceDiagnosis{EvidenceKind: "parent", RunID: "run", ResumeCount: 1, Reason: "terminal_outcome_not_saved", Repairable: true}, EvidenceURL: "/evidence/parent", CLICommand: "gregale automations recovery-retry-evidence --app consumer --parent"}}}})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomationsRecoveryPolicy([]string{"--app", "consumer", "--queue"}); code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"Wait: policy", "automatic_paused", "generation 1", "repairable true", "--parent", "/evidence/parent"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	if calls != 1 {
		t.Fatal("CLI ran an extra diagnosis or repair")
	}
}
