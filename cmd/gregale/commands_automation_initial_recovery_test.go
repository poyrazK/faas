package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationInitialRecoveryPlanReadOnly(t *testing.T) {
	for _, scenario := range []string{"eligible", "changed", "capacity", "wrong_scope", "wrong_target", "policy_eligible", "policy_excluded"} {
		t.Run(scenario, func(t *testing.T) {
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("plan preparation mutated state: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/apps/consumer" {
					if err := json.NewEncoder(w).Encode(api.AppResponse{ID: inspectorAppID}); err != nil {
						t.Error(err)
					}
					return
				}
				if r.URL.Path != "/v1/apps/consumer/workflow-failures/"+recoveryInvocationID+"/recovery-preview" {
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				out := api.WorkflowInitialRecoveryPreview{Failure: api.WorkflowInitialFailure{ID: recoveryInvocationID, AppID: inspectorAppID, RunID: inspectorRunID, Workflow: "paid", FailureStatus: "dead", ObservedAt: time.Now().UTC()}, ObservedAt: time.Now().UTC(), Reason: "eligible", Blockers: []string{}, Target: &api.WorkflowRunRecoveryTarget{RunID: inspectorRunID, FailureEvidenceID: recoveryInvocationID}}
				switch scenario {
				case "changed", "capacity":
					out.Reason = scenario
					out.Target = nil
				case "wrong_scope":
					out.Failure.AppID = recoverySecondRunID
				case "wrong_target":
					out.Target.ExpectedResumeCount = 1
				}
				if err := json.NewEncoder(w).Encode(out); err != nil {
					t.Error(err)
				}
			})
			main.CaptureAutomationStdout(t)
			path := filepath.Join(t.TempDir(), "initial.json")
			args := []string{"failure-recovery-plan", "--app", "consumer", "--failure", recoveryInvocationID, "--key", "initial-1", "--plan-out", path}
			if scenario == "policy_eligible" || scenario == "policy_excluded" {
				name := "paid"
				if scenario == "policy_excluded" {
					name = "other"
				}
				candidate := api.SetWorkflowRecoveryPolicyRequest{Policy: api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{name}, Triggers: []string{"initial_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "review"}}
				raw, err := json.Marshal(candidate)
				if err != nil {
					t.Fatal(err)
				}
				policyPath := filepath.Join(t.TempDir(), "policy.json")
				if err := os.WriteFile(policyPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--policy", policyPath)
			}
			code := main.CmdAutomations(args)
			if (code == 0) != (scenario == "eligible" || scenario == "policy_eligible") {
				t.Fatalf("exit %d", code)
			}
			if scenario != "eligible" && scenario != "policy_eligible" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("saved unsafe plan")
				}
				return
			}
			p, err := main.ReadAutomationRecoveryPlan(path)
			if err != nil {
				t.Fatal(err)
			}
			req := main.AutomationRunJobRequest(p, 2, "reviewed")
			if req.ParentJobID != "" || req.WorkflowRuns.Runs[0].FailureEvidenceID != recoveryInvocationID || req.WorkflowRuns.Runs[0].ExpectedResumeCount != 0 {
				t.Fatalf("request lost evidence %+v", req)
			}
			if err = req.Validate(); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(t.TempDir(), "journal.jsonl")
			if code = main.CmdAutomations([]string{"recovery-plan", "--plan", path, "--journal", journal}); code == 0 {
				t.Fatal("bypassed evidence-backed job admission")
			}
			if _, err = os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("created direct execution journal")
			}
		})
	}
}
func TestAutomationInitialFailureInbox(t *testing.T) {
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/workflow-failures" || r.URL.Query().Get("workflow") != "paid" || r.URL.Query().Get("limit") != "1" {
			t.Errorf("unexpected inbox request %s", r.URL)
		}
		if err := json.NewEncoder(w).Encode(api.WorkflowInitialFailures{Coverage: "partial", ObservedAt: time.Now().UTC(), Items: []api.WorkflowInitialFailure{{ID: recoveryInvocationID, Workflow: "paid", RunID: inspectorRunID, FailureStatus: "dead"}}}); err != nil {
			t.Error(err)
		}
	})
	main.CaptureAutomationStdout(t)
	if code := main.CmdAutomations([]string{"failures", "--app", "consumer", "--name", "paid", "--limit", "1"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
