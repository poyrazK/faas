package agents_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// adr: 20261027090016 amendment 2 — the seller lists a run's approvals and
// decides a pending one once; a closed one answers 409.
func TestRunApprovals(t *testing.T) {
	path := map[string]string{"id": "r1", "approval_id": "p1"}
	store := &fakeStore{approvals: []agentstore.Approval{
		{ID: "p1", RunID: "r1", Tool: "web_fetch", ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "p2", RunID: "r1", Tool: "web_fetch", ExpiresAt: time.Now().Add(-time.Second)},
		{ID: "p3", RunID: "r1", Tool: "web_fetch", Decision: "approve", ExpiresAt: time.Now().Add(time.Hour)},
	}}
	rec := serve(newHandler(store).ListRunApprovals, api.PlanPro, http.MethodGet, "/v1/agent-runs/r1/approvals", "", path)
	var list api.AgentRunApprovalList
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Items) != 3 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	for i, want := range []string{api.AgentRunApprovalPending, api.AgentRunApprovalExpired, api.AgentRunApprovalApproved} {
		if list.Items[i].State != want {
			t.Errorf("item %d state %s, want %s", i, list.Items[i].State, want)
		}
	}

	rec = serve(newHandler(store).DecideRunApproval, api.PlanPro, http.MethodPost, "/v1/agent-runs/r1/approvals/p1",
		`{"decision":"approve","note":"ok"}`, path)
	var got api.AgentRunApproval
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.State != api.AgentRunApprovalApproved {
		t.Fatalf("decide: %d %s", rec.Code, rec.Body)
	}
	if len(store.decided) != 1 || store.decided[0] != "r1/p1:approve:ok:account" {
		t.Errorf("decided %v", store.decided)
	}
	if len(store.audits) == 0 || store.audits[len(store.audits)-1] != "agent_run.approval_decided" {
		t.Errorf("audits %v", store.audits)
	}

	for _, tc := range []struct {
		name, body string
		err        error
		status     int
		code       string
	}{
		{"bad decision", `{"decision":"maybe"}`, nil, http.StatusBadRequest, api.CodeValidation},
		{"long note", `{"decision":"deny","note":"` + longNote() + `"}`, nil, http.StatusBadRequest, api.CodeValidation},
		{"closed", `{"decision":"approve"}`, agentstore.ErrApprovalClosed, http.StatusConflict, api.CodeAgentRunApprovalClosed},
		{"missing", `{"decision":"approve"}`, agentstore.ErrNotFound, http.StatusNotFound, api.CodeAgentRunApprovalNotFound},
	} {
		rec := serve(newHandler(&fakeStore{err: tc.err}).DecideRunApproval, api.PlanPro, http.MethodPost, "/", tc.body, path)
		if rec.Code != tc.status || problemCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, rec.Code, rec.Body, tc.status, tc.code)
		}
	}
	rec = serve(newHandler(&fakeStore{err: agentstore.ErrNotFound}).ListRunApprovals, api.PlanPro, http.MethodGet, "/", "", path)
	if rec.Code != http.StatusNotFound || problemCode(t, rec) != api.CodeAgentRunNotFound {
		t.Errorf("list of a missing run: %d %s", rec.Code, rec.Body)
	}
}

func longNote() string {
	b := make([]byte, api.AgentRunApprovalNoteMaxBytes+1)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
