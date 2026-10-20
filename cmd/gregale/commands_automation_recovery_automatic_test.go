package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationRecoveryPolicyAutomaticMode(t *testing.T) {
	p := cliTestPreviewPolicy()
	p.Mode = "automatic_retry"
	p.FailureStatuses = []string{"dead"}
	p.HourlyExecutionBudget = 2
	p.PauseAfterFailures = 2
	req := api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: 1, Policy: p}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "automatic-policy.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/v1/apps/consumer/recovery-policy" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		var got api.SetWorkflowRecoveryPolicyRequest
		if json.NewDecoder(r.Body).Decode(&got) != nil || got.Policy.Mode != "automatic_retry" || got.Policy.HourlyExecutionBudget != 2 || got.Policy.PauseAfterFailures != 2 || got.ExpectedVersion != 1 {
			t.Fatalf("automatic settings=%+v", got)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryPolicyResponse{Version: 2, Policy: p, Status: "paused", AutomaticExecution: &api.WorkflowRecoveryPolicyAutomaticExecution{ConsecutiveFailures: 2, PausedAt: &now, PauseReason: "consecutive_failures"}})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomationsRecoveryPolicy([]string{"--app", "consumer", "--file", path}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{"automatic_retry", "Automatic execution budget: 2/hour", "pause after 2 consecutive failures", "failures 2", "Paused:", "consecutive_failures"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
}

func TestAutomationRecoveryPolicyControls(t *testing.T) {
	now := time.Now().UTC()
	p := api.WorkflowRecoveryPolicyResponse{Version: 4, UpdatedAt: now, Policy: cliTestPreviewPolicy(), AutomaticExecution: &api.WorkflowRecoveryPolicyAutomaticExecution{PausedAt: &now}}
	p.Policy.Mode = "automatic_retry"
	calls := 0
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/recovery-policy" {
				t.Fatal(r.Method, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/apps/consumer/recovery-policy/control" {
			t.Fatal(r.Method, r.URL.Path)
		}
		var req api.ControlWorkflowRecoveryPolicyRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedVersion != 4 || !req.ExpectedUpdatedAt.Equal(now) || req.ExpectedPausedAt == nil || !req.ExpectedPausedAt.Equal(now) || req.Operation != "resume" || req.Reason != "provider recovered" {
			t.Fatalf("control %+v", req)
		}
		p.Status = "monitoring"
		p.AutomaticExecution.PausedAt = nil
		_ = json.NewEncoder(w).Encode(p)
	})
	main.CaptureAutomationStdout(t)
	if code := main.CmdAutomationsRecoveryPolicy([]string{"--app", "consumer", "--control", "resume", "--reason", "provider recovered"}); code != 0 || calls != 2 {
		t.Fatalf("code %d calls %d", code, calls)
	}
}
func TestAutomationRecoveryPolicyQueue(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	age := int64(60)
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/recovery-policy/queue" {
			t.Fatal(r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryAutomaticQueue{Pending: 2, Outstanding: 1, RemainingBudget: 3, Policy: api.WorkflowRecoveryPolicyResponse{Version: 4, Status: "paused", Policy: api.WorkflowRecoveryPolicy{HourlyExecutionBudget: 5}}, Entries: []api.WorkflowRecoveryAutomaticQueueEntry{{QueuedAt: now, QueuedAgeSeconds: 120, WaitingAgeSeconds: &age, ReviewID: "review", PolicyVersion: 4, Action: api.WorkflowRecoveryAutomaticAction{ActionID: "action", Status: "pending", Reason: "evidence_unknown", LastEvaluation: &api.WorkflowRecoveryAutomaticEvaluation{EvaluatedAt: now, Reason: "evidence_unknown", WaitingSince: &now}}}}, Truncated: true})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomationsRecoveryPolicy([]string{"--app", "consumer", "--queue"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"pending 2", "outstanding 1", "budget remaining 3/5", "evidence_unknown", "truncated", "age 120s", "Current observed wait: 60s", "Last evaluation: 2026-10-11T12:00:00Z", "Waiting since:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
}
