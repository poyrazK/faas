package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const inspectorRunID = "aaaaaaaa-0000-4000-8000-000000000001"
const inspectorAppID = "00000000-0000-4000-8000-000000000002"

func automationInspectorServer(t *testing.T, mode string) func() []string {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer inspector-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		run := api.WorkflowRunResponse{ID: inspectorRunID, AppID: inspectorAppID, WorkflowName: "approval-flow", Status: "failed", Input: json.RawMessage(`{"secret":"private-run-payload"}`), Output: json.RawMessage(`"private-run-output"`), PlatformTenantID: "private-tenant"}
		privateError := "private-run-error"
		run.LastError = &privateError
		if mode == "wrong_app" {
			run.AppID = "other-app"
		}
		if mode == "wrong_name" {
			run.WorkflowName = "another-automation"
		}
		if mode == "wrong_run" {
			run.ID = "bbbbbbbb-0000-4000-8000-000000000001"
		}
		switch {
		case r.URL.Path == "/v1/apps/billing":
			appID := inspectorAppID
			if mode == "missing_app_id" {
				appID = ""
			}
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID})
		case r.URL.Path == "/v1/apps/billing/automations/approval-flow":
			name := "approval-flow"
			if mode == "wrong_lookup" {
				name = "other"
			}
			if mode == "deleted" {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_ = json.NewEncoder(w).Encode(api.AutomationResponse{Name: name})
		case r.URL.Path == "/v1/apps/billing/workflows/runs":
			q := r.URL.Query()
			if q.Get("workflow_name") != "approval-flow" || q.Get("status") != "failed" || q.Get("limit") != "25" || q.Get("offset") != "50" || q.Get("created_after") != "2026-10-01T00:00:00.123Z" || q.Get("created_before") != "2026-10-05T00:00:00Z" {
				t.Errorf("wrong history filters: %s", r.URL.RawQuery)
			}
			if mode == "list_error" {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			runs := []api.WorkflowRunResponse{run}
			if mode == "empty" {
				runs = []api.WorkflowRunResponse{}
			}
			_ = json.NewEncoder(w).Encode(api.ListWorkflowRunsResponse{Runs: runs, Total: 80})
		case strings.EqualFold(r.URL.Path, "/v1/workflows/runs/"+inspectorRunID):
			if mode == "run_error" {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_ = json.NewEncoder(w).Encode(run)
		case strings.EqualFold(r.URL.Path, "/v1/workflows/runs/"+inspectorRunID+"/diagnostics"):
			if mode == "diagnostics_error" {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			result := api.WorkflowRunDiagnosticsResponse{RunID: inspectorRunID, WorkflowName: "approval-flow", Status: "failed", StateReason: "failed", Resume: api.WorkflowResumePreview{Eligible: mode != "blocked", ExpectedResumeCount: 2}}
			if mode == "blocked" {
				result.Resume.Blockers = []api.WorkflowDiagnosticBlocker{{Code: "unsafe_mutation"}}
			}
			if mode == "wrong_diagnostics" {
				result.WorkflowName = "other"
			}
			if mode == "wrong_diagnostics_run" {
				result.RunID = "bbbbbbbb-0000-4000-8000-000000000001"
			}
			_ = json.NewEncoder(w).Encode(result)
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "inspector-token")
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), calls...) }
}

func TestAutomationsRunHistoryScopeFiltersAndSummaries(t *testing.T) {
	for _, mode := range []string{"success", "human", "empty", "wrong_app", "wrong_name", "missing_app_id", "wrong_lookup", "deleted", "list_error"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = mode != "human"
			output := captureAutomationStdout(t)
			calls := automationInspectorServer(t, mode)
			code := cmdAutomations([]string{"runs", "--app", "billing", "--name", "approval-flow", "--status", "failed", "--limit", "25", "--offset", "50", "--created-after", "2026-10-01T03:00:00.123+03:00", "--created-before", "2026-10-05T00:00:00Z"})
			want := 1
			if mode == "success" || mode == "human" || mode == "empty" {
				want = 0
			}
			if mode == "deleted" {
				want = 4
			}
			if mode == "list_error" {
				want = 3
			}
			if code != want {
				t.Fatalf("exit=%d output=%s", code, output.String())
			}
			for _, secret := range []string{"private-run", "private-tenant", `"input"`, `"output"`, `"last_error"`, `"platform_tenant_id"`} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("summary leaked %s", secret)
				}
			}
			if want == 0 && jsonOutput {
				var report struct {
					App, Name            string
					Total, Limit, Offset int
					Runs                 []automationRunSummary
				}
				if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.App != "billing" || report.Name != "approval-flow" || report.Total != 80 || report.Limit != 25 || report.Offset != 50 {
					t.Fatalf("report=%s error=%v", output.String(), err)
				}
				if mode == "empty" && !strings.Contains(output.String(), `"runs": []`) {
					t.Fatalf("empty page=%s", output.String())
				}
			} else if want != 0 && output.Len() != 0 {
				t.Fatalf("printed rejected history: %s", output.String())
			}
			if mode == "human" && (!strings.Contains(output.String(), inspectorRunID) || !strings.Contains(output.String(), "80 matching runs")) {
				t.Fatalf("history output=%s", output.String())
			}
			expected := []string{"GET /v1/apps/billing"}
			if mode != "missing_app_id" {
				expected = append(expected, "GET /v1/apps/billing/automations/approval-flow")
			}
			if mode != "missing_app_id" && mode != "wrong_lookup" && mode != "deleted" {
				expected = append(expected, "GET /v1/apps/billing/workflows/runs")
			}
			if !reflect.DeepEqual(calls(), expected) {
				t.Fatalf("calls=%v want=%v", calls(), expected)
			}
		})
	}
}

func TestAutomationsRunDiagnosisScopeAndGuidance(t *testing.T) {
	for _, mode := range []string{"success", "blocked", "json", "uppercase", "wrong_app", "wrong_name", "wrong_run", "run_error", "wrong_diagnostics", "wrong_diagnostics_run", "diagnostics_error"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = mode == "json"
			output := captureAutomationStdout(t)
			calls := automationInspectorServer(t, mode)
			id := inspectorRunID
			if mode == "uppercase" {
				id = strings.ToUpper(id)
			}
			code := cmdAutomations([]string{"diagnose", "--app", "billing", "--name", "approval-flow", "--run", id})
			passed := mode == "success" || mode == "blocked" || mode == "json" || mode == "uppercase"
			if (code == 0) != passed {
				t.Fatalf("exit=%d output=%s", code, output.String())
			}
			if strings.Contains(output.String(), "private-") {
				t.Fatalf("diagnostics leaked run payloads: %s", output.String())
			}
			if !passed && output.Len() != 0 {
				t.Fatalf("printed rejected diagnostics: %s", output.String())
			}
			if mode == "success" || mode == "uppercase" {
				if !strings.Contains(output.String(), "gregale workflows resume "+inspectorRunID+" --expected-resume-count 2") {
					t.Fatalf("missing guidance: %s", output.String())
				}
			}
			if mode == "blocked" && (strings.Contains(output.String(), "Resume command:") || !strings.Contains(output.String(), "unsafe_mutation")) {
				t.Fatalf("unsafe guidance: %s", output.String())
			}
			if mode == "json" {
				var report struct {
					App, Name   string
					Diagnostics api.WorkflowRunDiagnosticsResponse
				}
				if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.App != "billing" || report.Name != "approval-flow" || report.Diagnostics.RunID != inspectorRunID {
					t.Fatalf("JSON=%s error=%v", output.String(), err)
				}
			}
			expected := []string{"GET /v1/apps/billing", "GET /v1/apps/billing/automations/approval-flow", "GET /v1/workflows/runs/" + id}
			if mode != "wrong_app" && mode != "wrong_name" && mode != "wrong_run" && mode != "run_error" {
				expected = append(expected, "GET /v1/workflows/runs/"+id+"/diagnostics")
			}
			if !reflect.DeepEqual(calls(), expected) {
				t.Fatalf("calls=%v want=%v", calls(), expected)
			}
		})
	}
}

func TestAutomationsRunInspectorRejectsArgumentsBeforeRequests(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{}`, 200)
	for _, args := range [][]string{
		{"runs"}, {"runs", "--app", "billing", "--name", "../other"},
		{"runs", "--app", "billing", "--name", "approval-flow", "--status", "cancelled"},
		{"runs", "--app", "billing", "--name", "approval-flow", "--limit", "101"},
		{"runs", "--app", "billing", "--name", "approval-flow", "--offset", "-1"},
		{"runs", "--app", "billing", "--name", "approval-flow", "--created-after", "invalid"},
		{"runs", "--app", "billing", "--name", "approval-flow", "--created-after", "2026-10-05T00:00:00Z", "--created-before", "2026-10-01T00:00:00Z"},
		{"diagnose", "--app", "billing", "--name", "approval-flow", "--run", "bad"},
		{"diagnose", "--name", "approval-flow", "--run", inspectorRunID},
		{"diagnose", "--app", "billing", "--name", "approval-flow", "--run", inspectorRunID, "extra"},
	} {
		if code := cmdAutomations(args); code != 1 || fake.sawMethod != "" {
			t.Fatalf("args=%v exit=%d request=%s", args, code, fake.sawMethod)
		}
	}
}
