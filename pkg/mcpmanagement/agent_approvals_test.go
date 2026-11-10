// adr: 20261104140058
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// An approval_required answer reaches the agent with the request's review
// link and an await_approval recovery naming gregale_wait_for_approval; the
// wait tool reports the decision as retry_original or stop; the activity
// tool reads the agent's own write log (ADR-20261107220000).
func TestAgentApprovalTools(t *testing.T) {
	old := approvalPollInterval
	approvalPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { approvalPollInterval = old })

	var mu sync.Mutex
	appProblem := api.CodeApprovalRequired
	statuses := []string{api.AgentApprovalPending, api.AgentApprovalApproved}
	polls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/demo":
			p := api.NewProblem(http.StatusPreconditionRequired, appProblem, "Approval required", "a person must approve this request")
			info := &api.AgentApprovalPendingInfo{ID: "ap-1", Category: api.AgentApprovalDelete, ReviewURL: "https://gregale.dev/cli-auth?agent_approval=ap-1", ExpiresAt: "2026-11-09T12:30:00Z"}
			if appProblem == api.CodeApprovalDenied {
				p.Status, info.Status, info.Note = http.StatusForbidden, api.AgentApprovalDenied, "not on Friday"
			}
			p.AgentApproval = info
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(p.Status)
			_ = json.NewEncoder(w).Encode(p)
		case "/v1/agent-approvals/ap-1":
			status := statuses[min(polls, len(statuses)-1)]
			polls++
			_ = json.NewEncoder(w).Encode(api.AgentApprovalResponse{ID: "ap-1", Status: status, ReviewURL: "https://gregale.dev/cli-auth?agent_approval=ap-1"})
		case "/v1/agent-connections/self/activity":
			_ = json.NewEncoder(w).Encode(api.AgentActivityResponse{Activity: []api.AgentActivityEntry{{ID: "a1", Method: "DELETE", Path: "/v1/apps/demo", Status: 204, ApprovalID: "ap-1", CreatedAt: "2026-11-09T12:00:00Z"}}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	defer backend.Close()
	cs, ctx := connectTestServer(t, backend, 0, "2026-07-28")

	var problem ToolError
	decode := func(raw map[string]json.RawMessage, out any) {
		t.Helper()
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	decode(callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "demo"}, true), &problem)
	if problem.Approval == nil || problem.Approval.ID != "ap-1" || problem.Approval.ReviewURL == "" {
		t.Fatalf("approval_required error = %+v; want the request and its review link", problem)
	}
	if r := problem.Recovery; r.Action != "await_approval" || r.Tool != waitForApprovalTool || r.Arguments["approval_id"] != "ap-1" || !r.ReuseInputs {
		t.Fatalf("recovery = %+v; want await_approval via %s with reuse_original_inputs", r, waitForApprovalTool)
	}

	var waited approvalWaitResult
	decode(callTool(t, ctx, cs, waitForApprovalTool, map[string]any{"approval_id": "ap-1", "wait_seconds": 5}, false), &waited)
	if waited.Status != api.AgentApprovalApproved || !waited.Decided || waited.Recovery.Action != "retry_original" || !waited.Recovery.ReuseInputs {
		t.Fatalf("wait = %+v; want approved with retry_original", waited)
	}

	mu.Lock()
	statuses, polls = []string{api.AgentApprovalPending}, 0
	mu.Unlock()
	decode(callTool(t, ctx, cs, waitForApprovalTool, map[string]any{"approval_id": "ap-1", "wait_seconds": 1}, false), &waited)
	if waited.Status != api.AgentApprovalPending || waited.Decided || waited.Recovery.Action != "await_approval" || waited.Recovery.Tool != waitForApprovalTool {
		t.Fatalf("pending wait = %+v; want await_approval again", waited)
	}
	callTool(t, ctx, cs, waitForApprovalTool, map[string]any{"approval_id": "ap-1", "wait_seconds": 51}, true)

	mu.Lock()
	appProblem = api.CodeApprovalDenied
	mu.Unlock()
	decode(callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "demo"}, true), &problem)
	if problem.Recovery.Action != "stop" || problem.Approval == nil || problem.Approval.Note != "not on Friday" {
		t.Fatalf("approval_denied error = %+v; want stop with the note", problem)
	}

	var activity api.AgentActivityResponse
	decode(callTool(t, ctx, cs, agentActivityTool, map[string]any{}, false), &activity)
	if len(activity.Activity) != 1 || activity.Activity[0].ApprovalID != "ap-1" {
		t.Fatalf("activity = %+v", activity)
	}
}
