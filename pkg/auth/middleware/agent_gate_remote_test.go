package middleware

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeRemoteGate struct {
	decision AgentGateDecision
	err      error
	pattern  string
	binding  state.AgentApprovalBinding
}

func (f *fakeRemoteGate) DecideAgentRequest(_ context.Context, _ state.Account, _ state.APIKey, pattern string, binding state.AgentApprovalBinding, _ string) (AgentGateDecision, error) {
	f.pattern, f.binding = pattern, binding
	return f.decision, f.err
}

// ADR-20261111150000: a gateway hands a gated agent request to apid and
// answers with apid's decision: through on an approval, the problem apid
// wrote, or 503 when apid cannot decide.
func TestGateAgentRequestAsksTheRemoteGate(t *testing.T) {
	const pattern = "GET /v1/apps/{slug}/debug"
	rule, ok := agentapproval.Classify(pattern)
	if !ok {
		t.Fatal("the debugger tunnel is not gated")
	}
	required := api.NewProblem(http.StatusPreconditionRequired, api.CodeApprovalRequired, "Approval required", "needs a person")
	cases := []struct {
		name     string
		gate     fakeRemoteGate
		answered bool
		status   int
		noted    string
	}{
		{name: "approved", gate: fakeRemoteGate{decision: AgentGateDecision{Allow: true, ApprovalID: "ap-1"}}, noted: "ap-1"},
		{name: "needs approval", gate: fakeRemoteGate{decision: AgentGateDecision{Problem: required}}, answered: true, status: http.StatusPreconditionRequired},
		{name: "apid unavailable", gate: fakeRemoteGate{err: errors.New("unimplemented")}, answered: true, status: http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gate := c.gate
			m := &Middleware{RemoteAgentGate: &gate}
			r := httptest.NewRequest(http.MethodGet, "/v1/apps/dev-svc/debug", nil)
			r.Pattern = pattern
			var note *agentActivityNote
			r = r.WithContext(withAgentActivityNote(r.Context(), &note))
			rec := httptest.NewRecorder()
			answered := m.gateAgentRequest(rec, r, state.Account{ID: "acct"}, state.APIKey{ID: "key", AccountID: "acct"}, rule)
			if answered != c.answered || (answered && rec.Code != c.status) {
				t.Fatalf("answered = %v, status %d; want %v, %d", answered, rec.Code, c.answered, c.status)
			}
			if note.approvalID != c.noted {
				t.Errorf("noted approval %q, want %q", note.approvalID, c.noted)
			}
			if gate.pattern != pattern || gate.binding.Method != http.MethodGet || gate.binding.Path != "/v1/apps/dev-svc/debug" || gate.binding.KeyID != "key" {
				t.Errorf("remote gate got %q %+v", gate.pattern, gate.binding)
			}
		})
	}
}

// A WebSocket handler returns only when its connection ends, so an agent's
// attach is recorded as 101 at the moment the handler takes the connection.
func TestActivityWriterRecordsAHijackedRequest(t *testing.T) {
	recorded := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		aw := &activityWriter{ResponseWriter: w}
		aw.onHijack = func() { recorded <- aw.status }
		conn, rw, err := http.NewResponseController(aw).Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: close\r\n\r\n")
		_ = rw.Flush()
		_ = conn.Close()
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err == nil {
		_, _ = bufio.NewReader(resp.Body).ReadString('\n')
		_ = resp.Body.Close()
	}
	if status := <-recorded; status != http.StatusSwitchingProtocols {
		t.Fatalf("recorded status %d, want 101", status)
	}
}
