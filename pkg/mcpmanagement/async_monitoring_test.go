// adr: 20261117232109
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// asyncFixture serves DLQ, workflow, monitoring and incident routes for app
// alpha on top of the operation fixture.
type asyncFixture struct {
	*operationFixture
	amu         sync.Mutex
	replays     []string
	replayKeys  []string
	resumes     []api.ResumeWorkflowRunRequest
	alerts      []api.CreateAlertRuleRequest
	notes       []string
	resumeCount int
}

func newAsyncFixture(t *testing.T, capabilities []string) (*management, *asyncFixture) {
	t.Helper()
	f := &asyncFixture{operationFixture: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}}
	backend := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(backend.Close)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, agentKey: true, Capabilities: capabilities, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func (f *asyncFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.amu.Lock()
	defer f.amu.Unlock()
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	appID := f.base.app.ID
	event := api.DeadLetterEvent{ID: "ev-1", AppID: appID, Source: "queue", Payload: json.RawMessage(`{"email":"a@example.com"}`), Headers: json.RawMessage(`{}`), ErrorKind: "handler_error", RetryCount: 5, LastFailedAt: time.Now()}
	switch {
	case r.URL.Path == "/v1/apps/alpha/dlq" && r.Method == http.MethodGet:
		// Newest first, across two pages: ev-old is the oldest.
		if r.URL.Query().Get("before") == "" {
			encode(api.DeadLetterEventsResponse{AppSlug: "alpha", Events: []api.DeadLetterEvent{event}, NextBefore: "page2"})
			return
		}
		old := event
		old.ID, old.LastFailedAt = "ev-old", event.LastFailedAt.Add(-time.Hour)
		encode(api.DeadLetterEventsResponse{AppSlug: "alpha", Events: []api.DeadLetterEvent{old}})
	case r.URL.Path == "/v1/apps/alpha/dlq/ev-1" && r.Method == http.MethodGet:
		encode(event)
	case strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/dlq/") && strings.HasSuffix(r.URL.Path, "/replay"):
		f.replays = append(f.replays, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/alpha/dlq/"), "/replay"))
		f.replayKeys = append(f.replayKeys, r.Header.Get("Idempotency-Key"))
		encode(event)
	case r.URL.Path == "/v1/workflows/runs/run-1" || r.URL.Path == "/v1/workflows/runs/run-other":
		owner := appID
		if strings.HasSuffix(r.URL.Path, "other") {
			owner = "another-app"
		}
		step := "charge"
		encode(api.WorkflowRunResponse{ID: strings.TrimPrefix(r.URL.Path, "/v1/workflows/runs/"), AppID: owner, WorkflowName: "orders", Status: "failed", CurrentStep: &step, ResumeCount: f.resumeCount, Input: json.RawMessage(`{"card":"4242"}`)})
	case r.URL.Path == "/v1/workflows/runs/run-1/resume":
		var req api.ResumeWorkflowRunRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.resumes = append(f.resumes, req)
		encode(api.WorkflowRunResponse{ID: "run-1", AppID: appID, Status: "running"})
	case r.URL.Path == "/v1/apps/alpha/alerts" && r.Method == http.MethodPost:
		var req api.CreateAlertRuleRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.alerts = append(f.alerts, req)
		encode(api.AlertRuleResponse{ID: "rule-1", AppID: appID, Name: req.Name})
	case r.URL.Path == "/v1/apps/alpha/workflow-attention":
		// apid refuses any empty query value and requires a scope
		// (operation.OperationWorkflowAttentionOptions, ValidateScope).
		for _, values := range r.URL.Query() {
			if len(values) != 1 || values[0] == "" {
				api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "Invalid", "operation input or contract is invalid"))
				return
			}
		}
		if r.URL.Query().Get("scope") != api.DefaultEnvScope {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "Invalid", "scope is required"))
			return
		}
		encode(api.OperationWorkflowAttentionResponse{})
	case r.URL.Path == "/v1/incidents/inc-1" || r.URL.Path == "/v1/incidents/inc-other":
		app := "alpha"
		if strings.HasSuffix(r.URL.Path, "other") {
			app = "beta"
		}
		encode(api.IncidentDetail{Incident: api.Incident{ID: strings.TrimPrefix(r.URL.Path, "/v1/incidents/"), App: app, Status: "open"}})
	case r.URL.Path == "/v1/incidents/inc-1/notes":
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.notes = append(f.notes, body.Body)
		encode(api.IncidentNote{})
	default:
		f.amu.Unlock()
		f.serveHTTP(w, r)
		f.amu.Lock()
	}
}

func TestDeadLetterReadsRedactPayloadsWithoutDataConsent(t *testing.T) {
	m, _ := newAsyncFixture(t, nil)
	out, err := m.listDeadLetters(t.Context(), appPageArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "a@example.com") || !strings.Contains(string(raw), "data:read consent required") || !strings.Contains(string(raw), "handler_error") {
		t.Fatalf("redacted list = %s", raw)
	}
	m, _ = newAsyncFixture(t, []string{api.ScopeDataRead})
	out, err = m.listDeadLetters(t.Context(), appPageArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(out); !strings.Contains(string(raw), "a@example.com") {
		t.Fatalf("data:read list = %s", raw)
	}
}

func TestDeadLetterReplayPlanReplaysEachEventOnce(t *testing.T) {
	m, f := newAsyncFixture(t, nil)
	plan, err := m.planDeadLetterReplay(t.Context(), planReplayArgs{App: "alpha", EventIDs: []string{"ev-1"}, Reason: "handler fixed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.replays) != 0 {
		t.Fatal("planning replayed")
	}
	id := plan.(releasePlan).ID
	for range 2 {
		if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.replays) != 1 || f.replayKeys[0] != "mcp-operation-"+id+":ev-1" {
		t.Fatalf("replays=%v keys=%v", f.replays, f.replayKeys)
	}
	for _, a := range []planReplayArgs{{App: "alpha", Reason: "none"}, {App: "alpha", All: true, EventIDs: []string{"ev-1"}, Reason: "both"}, {App: "alpha", All: true, Limit: 201, Reason: "big"}} {
		if _, err := m.planDeadLetterReplay(t.Context(), a); err == nil {
			t.Errorf("accepted %+v", a)
		}
	}
}

func TestWorkflowResumePinsTheResumeCount(t *testing.T) {
	m, f := newAsyncFixture(t, nil)
	f.resumeCount = 2
	plan, err := m.planWorkflowRecovery(t.Context(), planWorkflowRecoveryArgs{App: "alpha", Action: "resume", RunID: "run-1", Reason: "dependency back"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.resumes) != 1 || *f.resumes[0].ExpectedResumeCount != 2 {
		t.Fatalf("resumes = %+v", f.resumes)
	}
	if _, err := m.planWorkflowRecovery(t.Context(), planWorkflowRecoveryArgs{App: "alpha", Action: "cancel", RunID: "run-other", Reason: "x"}); err == nil {
		t.Fatal("another app's run accepted")
	}
	out, err := m.getWorkflowRun(t.Context(), workflowRunArgs{App: "alpha", RunID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "4242") {
		t.Fatalf("workflow input leaked: %s", raw)
	}
}

func TestAgentAlertRulesNotifyExistingChannelsOnly(t *testing.T) {
	m, f := newAsyncFixture(t, nil)
	name, cmp, window, threshold := "errors", "gt", "5m", 0.05
	if _, err := m.planMonitoringChange(t.Context(), planMonitoringArgs{App: "alpha", Kind: "alert", Op: "create", Alert: &alertSpec{Name: &name, Metric: "error_rate", Comparison: &cmp, Threshold: &threshold, WindowSpec: &window}, Reason: "no channel"}); err == nil {
		t.Fatal("alert without a channel accepted")
	}
	channels := []string{"ch-1"}
	plan, err := m.planMonitoringChange(t.Context(), planMonitoringArgs{App: "alpha", Kind: "alert", Op: "create", Alert: &alertSpec{Name: &name, Metric: "error_rate", Comparison: &cmp, Threshold: &threshold, WindowSpec: &window, ChannelIDs: &channels}, Reason: "watch errors"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
		t.Fatal(err)
	}
	got := f.alerts[0]
	if len(f.alerts) != 1 || got.WebhookURL != "" || got.WebhookSecret != "" || got.Action != nil || got.ChannelIDs[0] != "ch-1" {
		t.Fatalf("alert = %+v", got)
	}
	if _, err := m.planMonitoringChange(t.Context(), planMonitoringArgs{App: "alpha", Kind: "alert", Op: "delete", ID: "rule-1", Reason: "x"}); err == nil {
		t.Fatal("delete accepted")
	}
}

func TestReplayAllPreviewShowsTheOldest(t *testing.T) {
	m, _ := newAsyncFixture(t, nil)
	plan, err := m.planDeadLetterReplay(t.Context(), planReplayArgs{App: "alpha", All: true, Limit: 1, Reason: "handler fixed"})
	if err != nil {
		t.Fatal(err)
	}
	changes := string(plan.(releasePlan).Operation.Changes)
	if !strings.Contains(changes, `"id":"ev-old"`) || strings.Contains(changes, `"id":"ev-1"`) {
		t.Fatalf("preview = %s", changes)
	}
	if _, err := m.planDeadLetterReplay(t.Context(), planReplayArgs{App: "alpha", All: true, Limit: maxReplayAll + 1, Reason: "x"}); err == nil {
		t.Fatal("limit above apid's accepted")
	}
}

func TestIncidentNotesStayInsideAppConsent(t *testing.T) {
	m, f := newAsyncFixture(t, nil)
	m.opts.AllowedApps = map[string]string{"alpha": f.base.app.ID}
	if _, err := m.addIncidentNote(t.Context(), incidentNoteArgs{ID: "inc-1", Body: "rolled back the bad deploy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.addIncidentNote(t.Context(), incidentNoteArgs{ID: "inc-other", Body: "x"}); err == nil {
		t.Fatal("note on another app's incident accepted")
	}
	if len(f.notes) != 1 {
		t.Fatalf("notes = %v", f.notes)
	}
}

// gregale_list_workflow_attention sent `scope=` empty, and apid refuses an
// empty query value, so the tool failed on every call on production.
func TestWorkflowAttentionSendsTheDefaultScope(t *testing.T) {
	m, _ := newAsyncFixture(t, nil)
	if _, err := m.listWorkflowAttention(t.Context(), appArgs{App: "alpha"}); err != nil {
		t.Fatal(err)
	}
}
