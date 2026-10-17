// adr: 1286
package automationconsole

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type inspectionHost struct {
	responses map[string]Response
	calls     []Operation
}

func (h *inspectionHost) Scope(http.ResponseWriter, *http.Request) (Scope, bool) {
	return Scope{AppID: "app", AccountID: "account", Slug: "demo"}, true
}
func (h *inspectionHost) Request(_ *http.Request, _ Scope, operation Operation) Response {
	h.calls = append(h.calls, operation)
	if response, ok := h.responses[operation.Kind]; ok {
		return response
	}
	return Response{Status: http.StatusNotFound}
}
func (*inspectionHost) IssueCSRF(http.ResponseWriter, Scope) string { return "test-csrf" }
func (*inspectionHost) VerifyCSRF(http.ResponseWriter, *http.Request, Scope) bool {
	return true
}
func inspectionJSON(t *testing.T, value any) Response {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return Response{Status: http.StatusOK, Body: raw}
}
func inspectionFixture(t *testing.T) (*inspectionHost, string) {
	t.Helper()
	id := uuid.NewString()
	private := "PRIVATE-HISTORICAL-ERROR"
	host := &inspectionHost{responses: map[string]Response{
		"list":    inspectionJSON(t, api.ListAutomationsResponse{RuntimeEnabled: true}),
		"policy":  inspectionJSON(t, api.AutomationPublishPolicy{Mode: "optional"}),
		"get":     inspectionJSON(t, api.AutomationResponse{Name: "report", Version: 1, Draft: api.WorkflowSpec{Name: "report"}}),
		"get-run": inspectionJSON(t, api.WorkflowRunResponse{ID: id, AppID: "app", WorkflowName: "report", Status: "dead", ResumeCount: 1}),
		"steps":   inspectionJSON(t, api.ListWorkflowStepsResponse{Steps: []api.WorkflowStepResponse{{StepName: "send", Status: "dead", Error: &private}}}),
		"resumes": inspectionJSON(t, api.ListWorkflowResumesResponse{Resumes: []api.WorkflowResumeResponse{{RunID: id, ResumeNumber: 1,
			AccountID: "PRIVATE-ACTOR", PreviousError: &private, PreviousStatus: "dead", ResumedSteps: []string{"send"}}}}),
		"attempts": inspectionJSON(t, api.ListWorkflowStepAttemptsResponse{Attempts: []api.WorkflowStepAttemptResponse{{Attempt: 1, Status: "failed", Error: &private}}}),
	}}
	return host, id
}

func TestRecoveryInspectionPrivacyAndUnavailableReads(t *testing.T) {
	for _, test := range []struct {
		name, historyError string
		diagnosticsOK      bool
		blocked            bool
	}{
		{name: "eligible", diagnosticsOK: true},
		{name: "blocked", diagnosticsOK: true, blocked: true},
		{name: "unavailable", historyError: "Recovery history is temporarily unavailable."},
	} {
		t.Run(test.name, func(t *testing.T) {
			host, id := inspectionFixture(t)
			if test.diagnosticsOK {
				preview := api.WorkflowResumePreview{Eligible: !test.blocked, ExpectedResumeCount: 1, ReopenedSteps: []string{"send"}}
				if test.blocked {
					preview.Blockers = []api.WorkflowDiagnosticBlocker{api.WorkflowRecoveryBlocker("unsafe_mutation", "send")}
				}
				host.responses["diagnostics"] = inspectionJSON(t, api.WorkflowRunDiagnosticsResponse{RunID: id, WorkflowName: "report", StateReason: "dead", Resume: preview})
			} else {
				host.responses["resumes"] = Response{Status: http.StatusServiceUnavailable}
				host.responses["attempts"] = Response{Status: http.StatusServiceUnavailable}
			}
			service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			response := httptest.NewRecorder()
			service.Show(response, httptest.NewRequest(http.MethodGet, "/?name=report&run="+id, nil))
			body := response.Body.String()
			if response.Code != http.StatusOK {
				t.Fatalf("inspection: %d %s", response.Code, body)
			}
			if strings.Contains(body, "PRIVATE-") {
				t.Fatal("inspection exposed historical errors or account identifiers")
			}
			if offered := strings.Contains(body, ">Resume failed run</button>"); offered != (test.diagnosticsOK && !test.blocked) {
				t.Fatal("recovery control did not fail closed")
			}
			if test.historyError != "" && (!strings.Contains(body, test.historyError) || strings.Contains(body, "No continuations have been recorded")) {
				t.Fatal("failed history read looked empty")
			}
			for _, call := range host.calls {
				if call.Kind == "resume" || call.Kind == "cancel" || call.Kind == "attempts" {
					t.Fatal("GET mutated state or fetched unrequested attempts")
				}
			}
		})
	}
}

func TestRecoveryInspectionScopesRunAndStepBeforeFurtherReads(t *testing.T) {
	for _, invalid := range []string{"app", "workflow", "step"} {
		t.Run(invalid, func(t *testing.T) {
			host, id := inspectionFixture(t)
			query := "/?name=report&run=" + id
			if invalid == "step" {
				query += "&step=not-a-step"
			} else {
				run := api.WorkflowRunResponse{ID: id, AppID: "app", WorkflowName: "report"}
				if invalid == "app" {
					run.AppID = "other"
				} else {
					run.WorkflowName = "other"
				}
				host.responses["get-run"] = inspectionJSON(t, run)
			}
			service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			response := httptest.NewRecorder()
			service.Show(response, httptest.NewRequest(http.MethodGet, query, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("invalid identity accepted: %d", response.Code)
			}
			for _, call := range host.calls {
				if call.Kind == "attempts" || invalid != "step" && (call.Kind == "diagnostics" || call.Kind == "resumes" || call.Kind == "steps") {
					t.Fatal("run-specific data was requested before scope validation")
				}
			}
		})
	}
}

func TestSelectedAttemptHistoryOmitsErrors(t *testing.T) {
	for _, available := range []bool{true, false} {
		host, id := inspectionFixture(t)
		if !available {
			host.responses["attempts"] = Response{Status: http.StatusServiceUnavailable}
		}
		service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		response := httptest.NewRecorder()
		service.Show(response, httptest.NewRequest(http.MethodGet, "/?name=report&run="+id+"&step=send", nil))
		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(body, "Step attempts: send") || strings.Contains(body, "PRIVATE-") {
			t.Fatalf("attempt inspection: %d %s", response.Code, body)
		}
		if !available && (!strings.Contains(body, "Step attempt history is temporarily unavailable") || strings.Contains(body, "No executor attempts have been recorded")) {
			t.Fatal("a failed attempt read looked empty")
		}
	}
}

func TestRunControlsRequireConfirmationKeyAndObservedCount(t *testing.T) {
	key := uuid.NewString()
	for _, test := range []struct {
		name, action, confirmation, key, count string
		valid                                  bool
	}{
		{"confirmed resume", "resume", "yes", key, "1", true},
		{"confirmed cancel", "cancel", "yes", key, "", true},
		{"missing resume confirmation", "resume", "", key, "1", false},
		{"missing cancel confirmation", "cancel", "", key, "", false},
		{"invalid key", "resume", "yes", "not-a-key", "1", false},
		{"missing count", "resume", "yes", key, "", false},
		{"negative count", "resume", "yes", key, "-1", false},
		{"count past limit", "resume", "yes", key, "17", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{"confirm_" + test.action: {test.confirmation}, "action_key": {test.key}, "expected_resume_count": {test.count}}
			request := httptest.NewRequest(http.MethodPost, "/"+test.action, strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetPathValue("action", test.action)
			operation, message := controlOperation(request, api.WorkflowRunResponse{ID: "selected-run"})
			if (message == "") != test.valid {
				t.Fatalf("validation: %s", message)
			}
			if !test.valid {
				return
			}
			if operation.Kind != test.action || operation.Key != key || operation.RunID != "selected-run" {
				t.Fatal("run action did not retain its identity and idempotency key")
			}
			if test.action == "resume" {
				body, ok := operation.Body.(api.ResumeWorkflowRunRequest)
				if !ok || body.ExpectedResumeCount == nil || *body.ExpectedResumeCount != 1 {
					t.Fatal("resume did not submit the observed count")
				}
			} else if operation.Body != nil {
				t.Fatal("cancel supplied an unexpected request body")
			}
		})
	}
}
