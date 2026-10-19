package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutomationRecoveryPolicyEvaluationCLI(t *testing.T) {
	policy := api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: 4, Policy: cliTestPreviewPolicy()}
	raw, _ := json.Marshal(policy)
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/apps/consumer/recovery-policy/evaluate" {
			t.Fatalf("unexpected operation %s %s", r.Method, r.URL)
		}
		var req api.WorkflowRecoveryPolicyEvaluationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedVersion != 4 || req.CreatedBefore.Sub(req.CreatedAfter) != 7*24*time.Hour {
			t.Fatalf("request %+v %v", req, err)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryPolicyEvaluation{FailureCoverage: api.WorkflowRecoveryFailureCoverage{Coverage: "partial", CapturedFailures: 2, ReviewedFailures: 1, UnreviewedFailures: 1, MissingEligibilityEvidence: 1}, Current: api.WorkflowRecoveryPolicyResponse{Version: 4}, CurrentTotals: api.WorkflowRecoveryPolicyEvaluationTotals{WouldPrepare: 2}, ProposedTotals: api.WorkflowRecoveryPolicyEvaluationTotals{Unknown: 1}, Candidates: []api.WorkflowRecoveryPolicyEvaluationCandidate{{ReviewID: "review", Workflow: "orders", Proposed: api.WorkflowRecoveryPolicyAssessment{Eligibility: "unknown", Reason: "failure_status_ambiguous"}}, {LaterEligibility: &api.WorkflowRecoveryFailureEligibilityAssessment{Observation: api.WorkflowRecoveryFailureEligibility{ObservedAt: time.Now(), Reason: "resume_ineligible", Blockers: []string{"operation_owned"}}, Freshness: "fresh", Proposed: api.WorkflowRecoveryPolicyAssessment{Eligibility: "excluded", Reason: "later_preflight_resume_ineligible"}}, EvidenceSource: "failure_inventory", FailureEvidenceID: "failure:proof", Workflow: "orders", Proposed: api.WorkflowRecoveryPolicyAssessment{Eligibility: "unknown", Reason: "historical_preflight_not_captured"}}}, CoverageNotes: []string{"conditional preparation replay"}})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomations([]string{"recovery-policy-evaluate", "--app", "consumer", "--file", path, "--since", "7d"}); code != 0 || calls != 1 || !strings.Contains(out.String(), "2 captured, 1 reviewed, 1 unreviewed, 1 missing eligibility checks") || !strings.Contains(out.String(), "Later retry checks:") || !strings.Contains(out.String(), "operation_owned") || !strings.Contains(out.String(), "Failure evidence: failure:proof") || !strings.Contains(out.String(), "conditional preparations 2") || !strings.Contains(out.String(), "failure_status_ambiguous") || !strings.Contains(out.String(), "/v1/apps/consumer/recovery-policy/reviews/review") {
		t.Fatalf("evaluate %d %s", code, out)
	}
	for _, args := range [][]string{{}, {"--app", "consumer"}, {"--app", "consumer", "--file", path, "--since", "31d"}, {"--app", "consumer", "--file", path, "--since", "7d", "--created-after", "2026-10-01T00:00:00Z"}} {
		if code := main.CmdAutomationsRecoveryPolicyEvaluate(args); code != 1 || calls != 1 {
			t.Fatalf("accepted %+v code %d calls %d", args, code, calls)
		}
	}
	previous := (*main.JsonOutput)
	(*main.JsonOutput) = true
	t.Cleanup(func() { (*main.JsonOutput) = previous })
	out.Reset()
	if code := main.CmdAutomationsRecoveryPolicyEvaluate([]string{"--app", "consumer", "--file", path}); code != 0 {
		t.Fatalf("JSON %d", code)
	}
	var exported api.WorkflowRecoveryPolicyEvaluation
	if err := json.Unmarshal(out.Bytes(), &exported); err != nil || exported.ProposedTotals.Unknown != 1 || exported.Candidates[1].LaterEligibility == nil {
		t.Fatalf("export %s %v", out, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("evaluation modified policy file")
	}
}
