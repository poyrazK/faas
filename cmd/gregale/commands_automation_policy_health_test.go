package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationPolicyHealthCLI(t *testing.T) {
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/recovery-policy/health" || r.URL.Query().Get("stall_after_seconds") != "120" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryPolicyHealth{Status: "blocked", Reason: "execution_quota", StallAfterSeconds: 120, Queue: api.WorkflowRecoveryAutomaticQueue{Pending: 1, Policy: api.WorkflowRecoveryPolicyResponse{Policy: api.WorkflowRecoveryPolicy{HealthAlerts: &api.WorkflowRecoveryHealthAlertPolicy{Enabled: true, StallAfterSeconds: 300, AlertAfterSeconds: 60}}}, HealthIncident: &api.WorkflowRecoveryHealthIncident{EpisodeID: "incident", Status: "blocked", AlertNotification: &api.WorkflowRecoveryHealthNotification{Status: "queued"}}}})
	})
	out := main.CaptureAutomationStdout(t)
	code := main.CmdAutomationsHealth([]string{"--app", "consumer", "--stall-after-seconds", "120"})
	if code != 0 || !strings.Contains(out.String(), "blocked | execution_quota") {
		t.Fatalf("output %d %s", code, out)
	}
	if !strings.Contains(out.String(), "monitor stall threshold 300s | alert after 60s") || !strings.Contains(out.String(), "Health incident: incident | blocked | closed false | alert queued") {
		t.Fatalf("missing alert state %s", out)
	}
	for _, args := range [][]string{{"--app", "consumer", "--stall-after-seconds", "0"}, {"--app", "consumer", "--created-after", "2026-10-01T00:00:00Z"}} {
		if code := main.CmdAutomationsHealth(args); code != 1 {
			t.Fatal("accepted invalid health options")
		}
	}
}
