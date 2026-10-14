package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationRecoveryPolicyChangePreview(t *testing.T) {
	req := api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: 4, Policy: cliTestPreviewPolicy()}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/apps/consumer/recovery-policy/change-preview" {
			t.Fatalf("mutation or wrong endpoint %s %s", r.Method, r.URL.Path)
		}
		var got api.SetWorkflowRecoveryPolicyRequest
		if json.NewDecoder(r.Body).Decode(&got) != nil || got.ExpectedVersion != 4 {
			t.Fatalf("request %+v", got)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryPolicyChangePreview{Current: api.WorkflowRecoveryPolicyResponse{Version: 4}, Proposed: got.Policy, NextVersion: 5, PendingSuppressed: 2, OutstandingRetained: 1, HourlyExecutions: 1, RemainingExecutionBudget: 1, InspectedJobs: 5, JobsTruncated: true, InitialFailures: &api.WorkflowRecoveryInitialPolicyChangePreview{Coverage: "partial", InspectedFailures: 1, Eligible: 1, WouldPrepare: 1, WouldSubmit: 0, Truncated: true, Candidates: []api.WorkflowRecoveryInitialPolicyChangeCandidate{{Failure: api.WorkflowInitialFailure{ID: "failure", Workflow: "recover", RunID: "run", FailureStatus: "dead"}, Eligibility: "eligible", Preparation: "would_prepare", Execution: "outstanding_recovery", Blockers: []string{}}}}})
	})
	out := captureAutomationStdout(t)
	if code := cmdAutomationsRecoveryPolicy([]string{"--app", "consumer", "--file", path, "--preview"}); code != 0 || calls != 1 {
		t.Fatalf("code %d calls %d", code, calls)
	}
	for _, want := range []string{"version 4 -> 5", "Pending recoveries suppressed: 2", "execution slots retained: 1", "spent 1/hour", "Inspected jobs: 5", "jobs truncated true", "Advisory only", "Initial failures: 1 inspected", "would prepare 1", "would submit 0", "execution outstanding_recovery", "Initial sample: partial coverage | truncated true"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
}
func TestAutomationRecoveryPolicyChangePreviewArguments(t *testing.T) {
	for _, args := range [][]string{{"--app", "consumer", "--preview"}, {"--app", "consumer", "--preview", "--queue", "--file", "unused"}, {"--app", "consumer", "--preview", "--file", "unused", "--control", "pause", "--reason", "incident"}} {
		if code := cmdAutomationsRecoveryPolicy(args); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
