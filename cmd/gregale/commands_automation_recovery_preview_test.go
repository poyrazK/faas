package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func automationRecoveryServer(t *testing.T, mode string) func() []string {
	t.Helper()
	var calls []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer recovery-token" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) {
			t.Helper()
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.Path {
		case "/v1/apps/billing":
			write(api.AppResponse{ID: inspectorAppID})
		case "/v1/apps/billing/automations/approval-flow":
			kind := "event"
			if mode == "schedule" {
				kind = "schedule"
			}
			a := api.AutomationResponse{Name: "approval-flow", Version: 9, Enabled: true, Published: &api.WorkflowSpec{Trigger: &api.WorkflowTriggerSpec{Type: kind}}}
			if mode == "unpublished" {
				a.Published = nil
			}
			if mode == "manual" {
				a.Published.Trigger.Type = "manual"
			}
			if mode == "wrong_automation" {
				a.Name = "other"
			}
			write(a)
		case "/v1/apps/billing/automations/approval-flow/failure-policy":
			if mode == "policy_error" {
				w.WriteHeader(503)
				write(map[string]string{})
				return
			}
			write(api.AutomationFailurePolicyResponse{Paused: true, Generation: 3, RetainedEvents: 7})
		case "/v1/apps/billing/workflows/runs":
			q := r.URL.Query()
			status := q.Get("status")
			if q.Get("workflow_name") != "approval-flow" || q.Get("limit") != "2" || q.Get("offset") != "4" || q.Get("created_after") != "" {
				t.Errorf("wrong run selection %s", r.URL.RawQuery)
			}
			id := inspectorRunID
			if status == "dead" {
				id = "bbbbbbbb-0000-4000-8000-000000000001"
			}
			run := api.WorkflowRunResponse{ID: id, AppID: inspectorAppID, WorkflowName: "approval-flow", Status: status, Input: json.RawMessage(`"PRIVATE-INPUT"`), Output: json.RawMessage(`"PRIVATE-OUTPUT"`), PlatformTenantID: "PRIVATE-TENANT"}
			secret := "PRIVATE-ERROR"
			run.LastError = &secret
			if mode == "wrong_app" {
				run.AppID = "other"
			}
			if mode == "wrong_status" {
				run.Status = "running"
			}
			runs := []api.WorkflowRunResponse{run}
			if mode == "over_limit" {
				runs = append(runs, run, run)
			}
			if mode == "empty" {
				runs = []api.WorkflowRunResponse{}
			}
			write(api.ListWorkflowRunsResponse{Runs: runs, Total: 8})
		case "/v1/workflows/runs/" + inspectorRunID + "/diagnostics", "/v1/workflows/runs/bbbbbbbb-0000-4000-8000-000000000001/diagnostics":
			id := strings.Split(r.URL.Path, "/")[4]
			d := api.WorkflowRunDiagnosticsResponse{RunID: id, WorkflowName: "approval-flow", Status: "failed", Resume: api.WorkflowResumePreview{Eligible: true, ExpectedResumeCount: 2, ReopenedSteps: []string{"send"}, PreservedSteps: []string{"record"}}}
			if strings.HasPrefix(id, "bbbb") {
				d.Status = "dead"
				d.Resume.Eligible = false
				d.Resume.Blockers = []api.WorkflowDiagnosticBlocker{api.WorkflowRecoveryBlocker("unsafe_mutation", "send")}
			}
			if mode == "changed_status" {
				d.Status = "running"
				d.Resume.Eligible = false
				d.Resume.Blockers = []api.WorkflowDiagnosticBlocker{api.WorkflowRecoveryBlocker("run_not_failed", "")}
			}
			if mode == "wrong_diagnostics_id" {
				d.RunID = "bbbbbbbb-0000-4000-8000-000000000009"
			}
			if mode == "wrong_diagnostics" {
				d.WorkflowName = "other"
			}
			if mode == "diagnostics_error" {
				w.WriteHeader(503)
				write(map[string]string{})
				return
			}
			write(d)
		case "/v1/apps/billing/workflow-event-replay-preview":
			q := r.URL.Query()
			if q.Get("from") != "2026-10-01T00:00:00Z" || q.Get("until") != "2026-10-02T00:00:00Z" || q.Get("limit") != "2" || q.Get("after") != "cursor" {
				t.Errorf("wrong event selection %s", r.URL.RawQuery)
			}
			e := api.WorkflowEventReplayPreviewResponse{AppSlug: "billing", WorkflowName: "approval-flow", ScannedCount: 2, PotentialAdmissionCount: 1, AlreadyAdmittedCount: 1, Coverage: "retained_only", NextAfter: "next-cursor", Matches: []api.WorkflowEventReplayPreviewMatch{{EventID: "event-1", FilterMatched: true, RoutingState: "pending"}}}
			if mode == "over_events" {
				e.ScannedCount = 3
			}
			if mode == "wrong_events" {
				e.AppSlug = "other"
			}
			write(e)
		case "/v1/apps/billing/workflows/schedules/approval-flow/preview":
			q := r.URL.Query()
			if q.Get("since") != "2026-10-01T00:00:00Z" || q.Get("at") != "2026-10-02T00:00:00Z" || q.Get("count") != "1" {
				t.Errorf("wrong schedule selection %s", r.URL.RawQuery)
			}
			write(api.WorkflowSchedulePreviewResponse{WorkflowName: "approval-flow", Enabled: false, CatchUp: api.WorkflowScheduleCatchUpPreview{Policy: "latest", Outcome: "disabled"}})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "recovery-token")
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), calls...) }
}

func recoveryPreviewArgs() []string {
	return []string{"recovery-preview", "--app", "billing", "--name", "approval-flow", "--from", "2026-10-01T03:00:00+03:00", "--until", "2026-10-02T00:00:00Z", "--limit", "2", "--run-offset", "4", "--event-after", "cursor"}
}

func TestAutomationRecoveryPreviewReadOnlyBoundedAndScoped(t *testing.T) {
	for _, mode := range []string{"event", "human", "schedule", "manual", "empty", "changed_status", "wrong_app", "wrong_status", "wrong_automation", "wrong_diagnostics", "wrong_events", "over_limit", "diagnostics_error", "policy_error", "unpublished", "wrong_diagnostics_id", "over_events"} {
		t.Run(mode, func(t *testing.T) {
			main.ResetJSONOut(t)
			(*main.JsonOutput) = mode != "human"
			out := main.CaptureAutomationStdout(t)
			calls := automationRecoveryServer(t, mode)
			code := main.CmdAutomations(recoveryPreviewArgs())
			bad := strings.HasPrefix(mode, "wrong_") || mode == "over_limit" || mode == "diagnostics_error" || mode == "policy_error" || mode == "over_events"
			if bad {
				if code == 0 {
					t.Fatalf("scope/read error accepted: %s", out.String())
				}
				if strings.Contains(out.String(), `"runs"`) {
					t.Fatal("published partial report")
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out.String())
			}
			for _, secret := range []string{"PRIVATE-INPUT", "PRIVATE-OUTPUT", "PRIVATE-ERROR", "PRIVATE-TENANT"} {
				if strings.Contains(out.String(), secret) {
					t.Fatalf("leaked %s", secret)
				}
			}
			if mode == "human" {
				for _, want := range []string{"unsafe_mutation", "resume-eligible=true", "--run-offset 5", "--event-after next-cursor", "advisory", "not an atomic snapshot"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("missing %q in %s", want, out.String())
					}
				}
				return
			}
			var report main.AutomationRecoveryPreview
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.FailureGeneration != 3 || len(report.Runs) != 2 || report.RetainedEvents != 7 || report.Limit != 2 || report.CompletedAt.Before(report.StartedAt) {
				t.Fatalf("bad summary %+v", report)
			}
			if mode != "empty" {
				if report.Runs[0].NextOffset == nil || *report.Runs[0].NextOffset != 5 || report.Runs[0].Runs[0].Resume.ExpectedResumeCount != 2 {
					t.Fatal("lost page/resume evidence")
				}
				if mode == "changed_status" {
					if report.Runs[0].Runs[0].Status != "running" || report.Runs[0].Runs[0].Resume.Eligible {
						t.Fatal("used stale run-list eligibility")
					}
				}
			}
			if mode == "schedule" {
				if report.Events != nil || report.Schedule == nil || report.TriggerReason != "failure_resume_rearms_without_catch_up" {
					t.Fatal("misrepresented paused schedule recovery")
				}
			}
			if mode == "manual" {
				if report.Events != nil || report.Schedule != nil {
					t.Fatal("unexpected trigger preview")
				}
			}
			if mode == "event" {
				if report.Events == nil || report.Events.Retention.HistoryComplete || report.Events.NextAfter != "next-cursor" || len(report.EventDecisions) != 1 || report.EventDecisions[0].Reason != "automatic_admission_failure_paused" {
					t.Fatal("lost retained-only coverage/cursor")
				}
			}
			for _, call := range calls() {
				if !strings.HasPrefix(call, "GET ") {
					t.Fatalf("mutation %s", call)
				}
			}
		})
	}
}

func TestAutomationRecoveryPreviewRejectsInvalidOptionsBeforeRequests(t *testing.T) {
	for _, args := range [][]string{
		{"--app", "billing", "--name", "flow"},
		{"--app", "billing", "--name", "flow", "--from", "bad"},
		{"--app", "billing", "--name", "flow", "--from", "2026-10-02T00:00:00Z", "--until", "2026-10-01T00:00:00Z"},
		{"--app", "billing", "--name", "flow", "--from", "2026-10-01T00:00:00Z", "--limit", "26"},
		{"--app", "billing", "--name", "flow", "--from", "2026-10-01T00:00:00Z", "--run-offset", "-1"},
		{"--app", "billing", "--name", "flow", "--from", "2026-10-01T00:00:00Z", "unexpected"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := automationRecoveryServer(t, "event")
			if main.CmdAutomationsRecoveryPreview(args) == 0 || len(calls()) != 0 {
				t.Fatal("invalid options made a request")
			}
		})
	}
}

func TestAutomationRecoveryPreviewUsesFailureScheduleWarning(t *testing.T) {
	main.ResetJSONOut(t)
	out := main.CaptureAutomationStdout(t)
	automationRecoveryServer(t, "schedule")
	if main.CmdAutomations(recoveryPreviewArgs()) != 0 || !strings.Contains(out.String(), "without catch-up") {
		t.Fatal("missing failure-pause catch-up warning")
	}
}

func TestAutomationRecoveryPreviewDiscovery(t *testing.T) {
	command, ok := main.LookupCliCommand("automations")
	if !ok {
		t.Fatal("missing automations")
	}
	var found bool
	for _, sub := range command.Subcommands {
		if sub.Name == "recovery-preview" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing preview discovery")
	}
}

func TestAutomationRecoveryEventReasons(t *testing.T) {
	for _, tc := range []struct {
		name  string
		out   main.AutomationRecoveryPreview
		match api.WorkflowEventReplayPreviewMatch
		want  string
	}{
		{"existing", main.AutomationRecoveryPreview{FailurePaused: true}, api.WorkflowEventReplayPreviewMatch{AdmissionRecorded: true}, "already_admitted"},
		{"filter", main.AutomationRecoveryPreview{Enabled: true}, api.WorkflowEventReplayPreviewMatch{}, "filter_mismatch"},
		{"failure pause", main.AutomationRecoveryPreview{Enabled: true, FailurePaused: true}, api.WorkflowEventReplayPreviewMatch{FilterMatched: true}, "automatic_admission_failure_paused"},
		{"manual pause", main.AutomationRecoveryPreview{}, api.WorkflowEventReplayPreviewMatch{FilterMatched: true}, "automatic_admission_disabled"},
		{"advisory", main.AutomationRecoveryPreview{Enabled: true}, api.WorkflowEventReplayPreviewMatch{FilterMatched: true}, "potential_admission_requires_current_gates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.AutomationRecoveryEventReason(tc.out, tc.match); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
