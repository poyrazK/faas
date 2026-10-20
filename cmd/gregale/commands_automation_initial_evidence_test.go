package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"testing"
)

func TestAutomationInitialEvidenceCLI(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(map[bool]string{false: "diagnose", true: "repair"}[repair], func(t *testing.T) {
			calls := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				path := "/v1/apps/consumer/initial-recovery-reviews/" + inspectorRunID + "/evidence"
				method := "GET"
				if repair {
					path += "/repair"
					method = "POST"
				}
				if r.Method != method || r.URL.Path != path {
					t.Errorf("request %s %s", r.Method, r.URL)
				}
				if repair {
					var req api.WorkflowInitialEvidenceRepairRequest
					if json.NewDecoder(r.Body).Decode(&req) != nil || req.RequestID != recoveryInvocationID || req.Reason != "verified" {
						t.Errorf("repair body %+v", req)
					}
				}
				if err := json.NewEncoder(w).Encode(api.WorkflowInitialEvidenceDiagnosis{ReviewID: inspectorRunID, RunID: inspectorRunID, ResumeCount: 1, Reason: "already_recorded"}); err != nil {
					t.Error(err)
				}
			})
			main.CaptureAutomationStdout(t)
			args := []string{"failure-recovery-evidence", "--app", "consumer", "--review", inspectorRunID}
			if repair {
				args = append(args, "--repair", "--request-id", recoveryInvocationID, "--reason", "verified")
			}
			if code := main.CmdAutomations(args); code != 0 || calls != 1 {
				t.Fatalf("exit %d calls %d", code, calls)
			}
		})
	}
}
func TestAutomationInitialEvidenceValidation(t *testing.T) {
	for _, args := range [][]string{
		{"failure-recovery-evidence", "--app", "consumer"},
		{"failure-recovery-evidence", "--app", "consumer", "--review", inspectorRunID, "--reason", "bad"},
		{"failure-recovery-evidence", "--app", "consumer", "--review", inspectorRunID, "--repair", "--reason", "missing id"},
		{"failure-recovery-evidence", "--app", "consumer", "--review", inspectorRunID, "--repair", "--request-id", recoveryInvocationID},
	} {
		if code := main.CmdAutomations(args); code != 1 {
			t.Fatalf("invalid accepted %v", args)
		}
	}
}
