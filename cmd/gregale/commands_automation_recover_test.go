package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const recoverySecondRunID = "bbbbbbbb-0000-4000-8000-000000000001"

type automationRecoverCall struct {
	Method, Path, Key string
	Expected          int
}
type automationRecoverFake struct {
	mu       sync.Mutex
	calls    []automationRecoverCall
	counts   map[string]int
	accepted map[string]int
	reads    map[string]int
	mode     string
}

func automationRecoverServer(t *testing.T, mode string) *automationRecoverFake {
	t.Helper()
	fake := &automationRecoverFake{mode: mode, counts: map[string]int{inspectorRunID: 2, recoverySecondRunID: 5}, accepted: map[string]int{}, reads: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		mode := fake.mode
		call := automationRecoverCall{Method: r.Method, Path: r.URL.Path, Key: r.Header.Get("Idempotency-Key")}
		fake.calls = append(fake.calls, call)
		if r.Header.Get("Authorization") != "Bearer recover-token" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.Path {
		case "/v1/apps/billing":
			write(api.AppResponse{ID: inspectorAppID})
			return
		case "/v1/apps/billing/automations/approval-flow":
			write(api.AutomationResponse{Name: "approval-flow", Enabled: false, FailurePaused: true})
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 5 || parts[2] != "workflows" || parts[3] != "runs" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		id := parts[4]
		count, ok := fake.counts[id]
		if !ok {
			t.Errorf("unexpected run %s", id)
			w.WriteHeader(404)
			return
		}
		run := api.WorkflowRunResponse{ID: id, AppID: inspectorAppID, WorkflowName: "approval-flow", Status: "failed", ResumeCount: count, Input: json.RawMessage(`"PRIVATE-INPUT"`), Output: json.RawMessage(`"PRIVATE-OUTPUT"`), PlatformTenantID: "PRIVATE-TENANT"}
		private := "PRIVATE-ERROR"
		run.LastError = &private
		if len(parts) == 5 && r.Method == http.MethodGet {
			fake.reads[id]++
			if mode == "foreign" && id == recoverySecondRunID {
				run.AppID = "other"
			}
			if mode == "changed" && id == inspectorRunID && fake.reads[id] >= 2 {
				fake.counts[id] = 3
				run.ResumeCount = 3
			}
			if mode == "scope_changed" && id == inspectorRunID && fake.reads[id] >= 2 {
				run.AppID = "other"
			}
			if fake.accepted[id] > 0 && mode != "failed_again" {
				run.Status = "pending"
			}
			write(run)
			return
		}
		if len(parts) == 6 && parts[5] == "diagnostics" && r.Method == http.MethodGet {
			d := api.WorkflowRunDiagnosticsResponse{RunID: id, WorkflowName: "approval-flow", Status: "failed", Resume: api.WorkflowResumePreview{Eligible: true, ExpectedResumeCount: count}}
			if mode == "mixed" && id == recoverySecondRunID {
				d.Resume.Eligible = false
				d.Resume.Blockers = []api.WorkflowDiagnosticBlocker{{Code: "unsafe_mutation", Message: "PRIVATE-BLOCKER"}}
			}
			if mode == "diagnostics_changed" && id == inspectorRunID {
				d.Resume.ExpectedResumeCount = 3
			}
			if mode == "wrong_diagnostics" && id == inspectorRunID {
				d.WorkflowName = "other"
			}
			if mode == "cas_conflict" && id == inspectorRunID {
				fake.counts[id]++
			}
			write(d)
			return
		}
		if len(parts) == 6 && parts[5] == "resume" && r.Method == http.MethodPost {
			var body api.ResumeWorkflowRunRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedResumeCount == nil {
				t.Error("missing expected count")
				w.WriteHeader(400)
				return
			}
			fake.calls[len(fake.calls)-1].Expected = *body.ExpectedResumeCount
			if call.Key == "" {
				t.Error("missing idempotency key")
			}
			if *body.ExpectedResumeCount != fake.counts[id] {
				w.WriteHeader(409)
				write(api.NewProblem(409, api.CodeWorkflowResumeConflict, "Conflict", "PRIVATE-CONFLICT"))
				return
			}
			if mode == "post_quota" && id == inspectorRunID {
				w.WriteHeader(403)
				write(api.NewProblem(403, api.CodePlanWorkflowsQuota, "Quota", "PRIVATE-QUOTA"))
				return
			}
			if mode == "post_unsafe" && id == inspectorRunID {
				w.WriteHeader(409)
				write(api.NewProblem(409, api.CodeWorkflowResumeUnsafe, "Unsafe", "PRIVATE-UNSAFE"))
				return
			}
			if mode == "post_error" && id == inspectorRunID {
				w.WriteHeader(503)
				write(api.NewProblem(503, "capacity", "Unavailable", "PRIVATE-SERVER-ERROR"))
				return
			}
			fake.counts[id]++
			fake.accepted[id]++
			if mode == "lost_reply" && id == inspectorRunID {
				w.WriteHeader(503)
				write(api.NewProblem(503, "capacity", "Unavailable", "PRIVATE-LOST-REPLY"))
				return
			}
			run.Status = "pending"
			run.ResumeCount = fake.counts[id]
			if mode == "wrong_resume" && id == inspectorRunID {
				run.WorkflowName = "other"
			}
			write(run)
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "recover-token")
	return fake
}
func (f *automationRecoverFake) snapshot() ([]automationRecoverCall, map[string]int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]int{}
	for k, v := range f.accepted {
		counts[k] = v
	}
	return append([]automationRecoverCall(nil), f.calls...), counts
}
func automationRecoverArgs() []string {
	return []string{"recover", "--app", "billing", "--name", "approval-flow", "--run", inspectorRunID + ":2", "--run", recoverySecondRunID + ":5", "--idempotency-key", "outage-batch-1"}
}

func TestAutomationRecoverScopedFencesAndMixedOutcomes(t *testing.T) {
	for _, tc := range []struct {
		mode, outcome, reason string
		accepted              int
	}{
		{"success", "resumed", "resume_accepted", 1},
		{"changed", "changed", "run_state_changed", 0},
		{"diagnostics_changed", "changed", "run_state_changed", 0},
		{"cas_conflict", "changed", "resume_conflict", 0},
		{"wrong_diagnostics", "failed", "diagnostics_scope_mismatch", 0},
		{"scope_changed", "failed", "run_scope_mismatch", 0},
		{"post_unsafe", "blocked", api.CodeWorkflowResumeUnsafe, 0},
		{"post_error", "failed", "resume_unconfirmed", 0},
		{"post_quota", "blocked", api.CodePlanWorkflowsQuota, 0},
		{"wrong_resume", "failed", "resume_response_unconfirmed", 1},
		{"mixed", "resumed", "resume_accepted", 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			main.ResetJSONOut(t)
			(*main.JsonOutput) = true
			out := main.CaptureAutomationStdout(t)
			fake := automationRecoverServer(t, tc.mode)
			code := main.CmdAutomations(automationRecoverArgs())
			want := 1
			if tc.mode == "success" {
				want = 0
			}
			if code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			var report main.AutomationRecoveryReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Outcomes) != 2 || report.Outcomes[0].Outcome != tc.outcome || report.Outcomes[0].Reason != tc.reason {
				t.Fatalf("wrong outcomes %+v", report.Outcomes)
			}
			if tc.mode == "mixed" {
				if report.Outcomes[1].Outcome != "blocked" || len(report.Outcomes[1].Blockers) != 1 || report.Outcomes[1].Blockers[0] != "unsafe_mutation" {
					t.Fatal("lost blocked outcome")
				}
			} else if report.Outcomes[1].Outcome != "resumed" {
				t.Fatal("did not continue independent run")
			}
			calls, accepted := fake.snapshot()
			if accepted[inspectorRunID] != tc.accepted {
				t.Fatal("wrong number of accepted resumes")
			}
			for i, call := range calls {
				if call.Method == http.MethodPost {
					if i < 4 || !strings.HasSuffix(call.Path, "/resume") {
						t.Fatalf("mutation before scope validation / unexpected mutation: %+v", calls)
					}
					expected := 2
					id := inspectorRunID
					if strings.Contains(call.Path, recoverySecondRunID) {
						expected = 5
						id = recoverySecondRunID
					}
					if call.Expected != expected || call.Key != main.AutomationRecoveryRunKey("outage-batch-1", inspectorAppID, "approval-flow", main.AutomationRecoverySelection{ID: id, ExpectedResumeCount: expected}) {
						t.Fatalf("lost original generation/key: %+v", call)
					}
				}
			}
			if strings.Contains(out.String(), "PRIVATE-") {
				t.Fatalf("private run/error values leaked: %s", out.String())
			}
		})
	}
}

func TestAutomationRecoverValidatesAllScopeBeforeWrites(t *testing.T) {
	main.ResetJSONOut(t)
	out := main.CaptureAutomationStdout(t)
	fake := automationRecoverServer(t, "foreign")
	if main.CmdAutomations(automationRecoverArgs()) == 0 {
		t.Fatal("accepted foreign run")
	}
	calls, accepted := fake.snapshot()
	if len(accepted) != 0 {
		t.Fatal("partial mutation before complete scope validation")
	}
	for _, call := range calls {
		if call.Method != http.MethodGet {
			t.Fatalf("unexpected mutation %+v", call)
		}
	}
	if strings.Contains(out.String(), "Run recovery:") {
		t.Fatal("emitted partial success report")
	}
}

func TestAutomationRecoverLostReplyCannotResumeTwice(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	out := main.CaptureAutomationStdout(t)
	fake := automationRecoverServer(t, "lost_reply")
	if main.CmdAutomations(automationRecoverArgs()) != 1 {
		t.Fatal("lost reply should have an unconfirmed outcome")
	}
	var first main.AutomationRecoveryReport
	if err := json.Unmarshal(out.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Outcomes[0].Reason != "resume_unconfirmed" {
		t.Fatal("claimed lost reply was confirmed")
	}
	fake.mu.Lock()
	fake.mode = "failed_again"
	fake.mu.Unlock()
	out.Reset()
	if main.CmdAutomations(automationRecoverArgs()) != 1 {
		t.Fatal("retry should report changed runs")
	}
	var second main.AutomationRecoveryReport
	if err := json.Unmarshal(out.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	for _, result := range second.Outcomes {
		if result.Outcome != "changed" {
			t.Fatalf("retry outcome %+v", result)
		}
	}
	_, accepted := fake.snapshot()
	if accepted[inspectorRunID] != 1 || accepted[recoverySecondRunID] != 1 {
		t.Fatalf("duplicate resume: %+v", accepted)
	}
}

func TestAutomationRecoverUncommittedRetryUsesSameKey(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	out := main.CaptureAutomationStdout(t)
	fake := automationRecoverServer(t, "post_error")
	if main.CmdAutomations(automationRecoverArgs()) != 1 {
		t.Fatal("first should fail")
	}
	fake.mu.Lock()
	fake.mode = "success"
	fake.mu.Unlock()
	// Keep the first selection's original count; the second already advanced.
	out.Reset()
	if main.CmdAutomations(automationRecoverArgs()) != 1 {
		t.Fatal("mixed resumed/changed retry should be nonzero")
	}
	calls, accepted := fake.snapshot()
	var keys []string
	for _, call := range calls {
		if call.Method == http.MethodPost && strings.Contains(call.Path, inspectorRunID) {
			keys = append(keys, call.Key)
		}
	}
	if len(keys) != 2 || keys[0] != keys[1] || accepted[inspectorRunID] != 1 || accepted[recoverySecondRunID] != 1 {
		t.Fatalf("unstable retry or duplicate admission: %v %+v", keys, accepted)
	}
}

func TestAutomationRecoverSelectionValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--app", "billing", "--name", "flow"},
		{"--app", "billing", "--name", "flow", "--idempotency-key", "key", "--run", "bad:0"},
		{"--app", "billing", "--name", "flow", "--idempotency-key", "key", "--run", inspectorRunID + ":-1"},
		{"--app", "billing", "--name", "flow", "--idempotency-key", "key", "--run", inspectorRunID + ":17"},
		{"--app", "billing", "--name", "flow", "--idempotency-key", "key", "--run", inspectorRunID + ":0", "--run", strings.ToUpper(inspectorRunID) + ":1"},
		{"--app", "billing", "--name", "flow", "--run", inspectorRunID + ":0"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fake := automationRecoverServer(t, "success")
			if main.CmdAutomationsRecover(args) == 0 {
				t.Fatal("accepted invalid options")
			}
			calls, _ := fake.snapshot()
			if len(calls) != 0 {
				t.Fatal("requested before validating")
			}
		})
	}
	args := []string{"--app", "billing", "--name", "flow", "--idempotency-key", "key"}
	for i := 0; i <= api.AutomationRecoveryBatchMaxRuns; i++ {
		args = append(args, "--run", fmt.Sprintf("aaaaaaaa-0000-4000-8000-%012d:0", i))
	}
	fake := automationRecoverServer(t, "success")
	if main.CmdAutomationsRecover(args) == 0 {
		t.Fatal("accepted oversized batch")
	}
	calls, _ := fake.snapshot()
	if len(calls) != 0 {
		t.Fatal("oversized batch requested API")
	}
}

func TestAutomationRecoverKeyIndependentOfSelectionOrder(t *testing.T) {
	item := main.AutomationRecoverySelection{ID: inspectorRunID, ExpectedResumeCount: 2}
	key := main.AutomationRecoveryRunKey("batch", inspectorAppID, "flow", item)
	if err := main.ValidateDeployIdempotencyKey(key); err != nil {
		t.Fatal(err)
	}
	item.ExpectedResumeCount++
	if main.AutomationRecoveryRunKey("batch", inspectorAppID, "flow", item) == key {
		t.Fatal("later continuation reused logical request key")
	}
}

func TestAutomationRecoverHumanReport(t *testing.T) {
	main.ResetJSONOut(t)
	out := main.CaptureAutomationStdout(t)
	automationRecoverServer(t, "mixed")
	if main.CmdAutomations(automationRecoverArgs()) != 1 {
		t.Fatal("partial batch should be nonzero")
	}
	for _, want := range []string{"resumed", "blocked", "unsafe_mutation", "independently", "original counts", "resume history"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
}
