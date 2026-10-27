package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func cliTestPreviewPolicy() api.WorkflowRecoveryPolicy {
	return api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{"recover"}, Triggers: []string{"saved_failure"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "incident"}
}
func TestAutomationRecoveryPolicyConfigure(t *testing.T) {
	for _, scenario := range []string{"get", "set", "automatic", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy.json")
			req := api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: 2, Policy: cliTestPreviewPolicy()}
			if scenario == "automatic" {
				req.Policy.Mode = "automatic"
			}
			raw, _ := json.Marshal(req)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/apps/consumer/recovery-policy" {
					t.Fatalf("unexpected path=%s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if scenario == "get" {
					if r.Method != "GET" {
						t.Fatal("inspection mutated policy")
					}
				} else {
					if r.Method != "PUT" {
						t.Fatal("wrong method")
					}
					var submitted api.SetWorkflowRecoveryPolicyRequest
					_ = json.NewDecoder(r.Body).Decode(&submitted)
					if submitted.ExpectedVersion != 2 || submitted.Policy.Mode != "preview" {
						t.Fatalf("request=%+v", submitted)
					}
				}
				if scenario == "stale" {
					w.WriteHeader(409)
					_ = json.NewEncoder(w).Encode(api.Problem{Status: 409, Code: api.CodeConflict})
					return
				}
				_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryPolicyResponse{AppID: inspectorAppID, Version: 3, Policy: req.Policy, Status: "monitoring"})
			})
			main.CaptureAutomationStdout(t)
			args := []string{"--app", "consumer"}
			if scenario != "get" {
				args = append(args, "--file", path)
			}
			code := main.CmdAutomationsRecoveryPolicy(args)
			if scenario == "automatic" {
				if code == 0 || calls != 0 {
					t.Fatal("automatic policy submitted")
				}
			} else if scenario == "stale" {
				if code == 0 || calls != 1 {
					t.Fatal("stale policy accepted")
				}
			} else if code != 0 || calls != 1 {
				t.Fatalf("code=%d calls=%d", code, calls)
			}
		})
	}
}
func TestAutomationRecoveryPolicyReviewExport(t *testing.T) {
	for _, scenario := range []string{"export", "suppressed", "scope"} {
		t.Run(scenario, func(t *testing.T) {
			p := responseTestPlan()
			review := api.WorkflowRecoveryPolicyReview{ID: recoverySecondRunID, AppID: p.AppID, JobID: p.JobID, PolicyVersion: 1, Policy: cliTestPreviewPolicy(), ObservedAt: p.CreatedAt, Plan: &p}
			if scenario == "suppressed" {
				review.Plan = nil
			}
			if scenario == "scope" {
				review.AppID = recoveryInvocationID
			}
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Fatalf("export submitted %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/consumer":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: inspectorAppID})
				case "/v1/apps/consumer/recovery-policy/reviews/" + review.ID:
					_ = json.NewEncoder(w).Encode(review)
				default:
					t.Fatalf("unexpected path=%s", r.URL.Path)
				}
			})
			main.CaptureAutomationStdout(t)
			path := filepath.Join(t.TempDir(), "prepared.json")
			code := main.CmdAutomationsRecoveryPolicyReviews([]string{"--app", "consumer", "--review", review.ID, "--plan-out", path})
			if scenario == "export" {
				if code != 0 {
					t.Fatalf("export=%d", code)
				}
				saved, err := main.ReadAutomationResponsePlan(path)
				if err != nil || saved.Actions[0].Cancellation.RequestID != p.Actions[0].Cancellation.RequestID {
					t.Fatalf("saved=%+v %v", saved, err)
				}
			} else if code == 0 {
				t.Fatal("exported unsafe or suppressed plan")
			}
		})
	}
}

func TestAutomationRecoveryPolicyReviewLifecycle(t *testing.T) {
	for _, operation := range []string{"approved", "dismissed", "status", "missing_reason", "missing_review"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			p := responseTestPlan()
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				suffix := "decision"
				if operation == "status" {
					suffix = "status"
				}
				if r.URL.Path != "/v1/apps/consumer/recovery-policy/reviews/"+p.JobID+"/"+suffix {
					t.Fatalf("path=%s", r.URL.Path)
				}
				if operation == "status" {
					if r.Method != "GET" {
						t.Fatal("status mutated plan")
					}
					_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryReviewStatus{ReviewID: p.JobID, Status: "stale"})
				} else {
					if r.Method != "POST" {
						t.Fatal("decision method")
					}
					var req api.WorkflowRecoveryReviewDecisionRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					if req.Decision != operation || req.Reason != "reviewed incident" {
						t.Fatalf("decision=%+v", req)
					}
					_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryPolicyReview{ID: p.JobID, ReviewDecision: &api.WorkflowRecoveryReviewDecision{Decision: req.Decision}})
				}
			})
			main.CaptureAutomationStdout(t)
			args := []string{"--app", "consumer", "--review", p.JobID}
			if operation == "status" {
				args = append(args, "--status")
			} else {
				decision := operation
				if operation == "missing_reason" || operation == "missing_review" {
					decision = "approved"
				}
				args = append(args, "--decision", decision)
				if operation != "missing_reason" {
					args = append(args, "--reason", "reviewed incident")
				}
				if operation == "missing_review" {
					args = []string{"--app", "consumer", "--decision", "approved", "--reason", "reviewed incident"}
				}
			}
			code := main.CmdAutomationsRecoveryPolicyReviews(args)
			invalid := operation == "missing_reason" || operation == "missing_review"
			if invalid {
				if code == 0 || calls != 0 {
					t.Fatal("invalid review submitted")
				}
			} else if code != 0 || calls != 1 {
				t.Fatalf("code=%d calls=%d", code, calls)
			}
		})
	}
}
