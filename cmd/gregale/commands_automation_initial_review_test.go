package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"testing"
)

func TestAutomationInitialReviewCommands(t *testing.T) {
	for _, action := range []string{"prepare", "get", "submit", "cancel", "list", "stale"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				base := "/v1/apps/consumer/initial-recovery-reviews"
				if action == "list" {
					if r.Method != "GET" || r.URL.Path != base || r.URL.Query().Get("limit") != "1" {
						t.Errorf("list request %s %s", r.Method, r.URL)
					}
					if err := json.NewEncoder(w).Encode(api.WorkflowInitialRecoveryReviews{Reviews: []api.WorkflowInitialRecoveryReview{}}); err != nil {
						t.Error(err)
					}
					return
				}
				if action == "prepare" {
					if r.Method != "POST" || r.URL.Path != "/v1/apps/consumer/workflow-failures/"+recoveryInvocationID+"/recovery-reviews" {
						t.Errorf("prepare request %s %s", r.Method, r.URL)
					}
					var req api.WorkflowInitialRecoveryReviewRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedPolicyVersion != 3 {
						t.Errorf("request %+v %v", req, err)
					}
				} else if action == "get" {
					if r.Method != "GET" || r.URL.Path != base+"/"+inspectorRunID {
						t.Errorf("get request %s %s", r.Method, r.URL)
					}
				} else {
					if r.Method != "POST" || r.URL.Path != base+"/"+inspectorRunID+"/decision" {
						t.Errorf("decision request %s %s", r.Method, r.URL)
					}
					var req api.WorkflowInitialRecoveryReviewDecisionRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Decision != func() string {
						if action == "stale" {
							return "submit"
						}
						return action
					}() || req.Reason != "reviewed" {
						t.Errorf("decision %+v %v", req, err)
					}
				}
				status := "prepared"
				if action == "submit" {
					status = "submitted"
				}
				if action == "cancel" {
					status = "cancelled"
				}
				if action == "stale" {
					status = "stale"
				}
				if err := json.NewEncoder(w).Encode(api.WorkflowInitialRecoveryReview{ID: inspectorRunID, AppID: inspectorAppID, Status: status, Reason: "eligible", PolicyVersion: 3}); err != nil {
					t.Error(err)
				}
			})
			main.CaptureAutomationStdout(t)
			args := []string{"failure-recovery-review", "--app", "consumer"}
			switch action {
			case "prepare":
				args = append(args, "--failure", recoveryInvocationID, "--expected-policy-version", "3")
			case "list":
				args = []string{"failure-recovery-reviews", "--app", "consumer", "--limit", "1"}
			default:
				args = append(args, "--review", inspectorRunID)
				if action != "get" {
					decision := action
					if action == "stale" {
						decision = "submit"
					}
					args = append(args, "--decision", decision, "--reason", "reviewed")
				}
			}
			want := 0
			if action == "stale" {
				want = 1
			}
			if code := main.CmdAutomations(args); code != want || calls != 1 {
				t.Fatalf("exit %d calls %d", code, calls)
			}
		})
	}
}
func TestAutomationInitialReviewValidation(t *testing.T) {
	for _, args := range [][]string{
		{"failure-recovery-review", "--app", "consumer"},
		{"failure-recovery-review", "--app", "consumer", "--failure", recoveryInvocationID, "--expected-policy-version", "0"},
		{"failure-recovery-review", "--app", "consumer", "--failure", recoveryInvocationID, "--expected-policy-version", "1", "--review", inspectorRunID},
		{"failure-recovery-review", "--app", "consumer", "--review", inspectorRunID, "--decision", "submit"},
		{"failure-recovery-review", "--app", "consumer", "--review", inspectorRunID, "--decision", "automatic", "--reason", "unsafe"},
		{"failure-recovery-reviews", "--app", "consumer", "--limit", "101"},
	} {
		eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("invalid arguments made HTTP request %s", r.URL)
		})
		if code := main.CmdAutomations(args); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
