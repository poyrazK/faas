package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"strings"
	"testing"
)

func TestAutomationRecoveryCancellationSubmission(t *testing.T) {
	for _, scenario := range []string{"preview", "cancel", "confirmation", "scope", "changed"} {
		t.Run(scenario, func(t *testing.T) {
			posts := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/consumer":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: inspectorAppID})
				case "/v1/event-recoveries/" + recoveryInvocationID:
					app := inspectorAppID
					if scenario == "scope" {
						app = recoverySecondRunID
					}
					_ = json.NewEncoder(w).Encode(api.EventRecoveryJob{ID: recoveryInvocationID, AppID: app, Selection: api.EventRecoveryRequest{WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover"}}})
				default:
					posts++
					var req api.WorkflowRecoveryCancellationRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					if req.Position != 2 || req.ExpectedResumeCount == nil || *req.ExpectedResumeCount != 3 || req.Reason != "incident" {
						t.Fatalf("request=%+v", req)
					}
					suffix := "workflow-cancel"
					outcome := "cancelled"
					preview := scenario == "preview"
					if preview {
						suffix = "workflow-cancellation-preview"
						outcome = "eligible"
					}
					if scenario == "changed" {
						outcome = "changed"
					}
					if r.Method != http.MethodPost || r.URL.Path != "/v1/event-recoveries/"+recoveryInvocationID+"/"+suffix {
						t.Fatalf("route=%s %s", r.Method, r.URL.Path)
					}
					_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryCancellationResponse{JobID: recoveryInvocationID, Position: 2, RunID: inspectorRunID, ResumeCount: 3, Outcome: outcome, Preview: preview})
				}
			})
			command := "recovery-cancel"
			if scenario == "preview" {
				command = "recovery-cancel-preview"
			}
			args := []string{command, "--app", "consumer", "--job", recoveryInvocationID, "--position", "2", "--resume-count", "3", "--reason", "incident"}
			if scenario != "preview" && scenario != "confirmation" {
				args = append(args, "--yes")
			}
			main.CaptureAutomationStdout(t)
			code := main.CmdAutomations(args)
			want := 0
			wantPosts := 1
			if scenario == "scope" || scenario == "confirmation" {
				want = 1
				wantPosts = 0
			}
			if scenario == "changed" {
				want = 5
			}
			if code != want || posts != wantPosts {
				t.Fatalf("exit=%d posts=%d", code, posts)
			}
		})
	}
}
func TestAutomationRecoveryHealthActionHints(t *testing.T) {
	var out bytes.Buffer
	main.WriteAutomationRecoveryAttention(&out, "consumer", recoveryInvocationID, api.WorkflowRecoveryExecutionAttention{StalledCount: 1, Targets: []api.WorkflowRecoveryExecutionAttentionTarget{{Position: 2, RunID: inspectorRunID, ResumeCount: 3, Status: "stalled", DiagnosticsURL: "/diagnostics", RunURL: "/run"}}})
	for _, want := range []string{"recovery-cancel-preview", "--position 2 --resume-count 3", "/diagnostics", "recovery-retry-plan", "recovery-history"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "--yes") {
		t.Fatal("health hints confirmed cancellation")
	}
}
