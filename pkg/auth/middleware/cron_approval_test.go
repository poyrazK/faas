package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWindowedCronApprovalRefusesAnUnboundBody(t *testing.T) {
	const pattern = "POST /v1/apps/{slug}/cron-deletions"
	rule, _ := agentapproval.Classify(pattern)
	remote := &fakeRemoteGate{decision: AgentGateDecision{Allow: true}}
	m := &Middleware{RemoteAgentGate: remote}
	r := httptest.NewRequest(http.MethodPost, "/v1/apps/alpha/cron-deletions", strings.NewReader(strings.Repeat(" ", api.AgentApprovalBodyBindBytes+1)))
	r.Pattern = pattern
	w := httptest.NewRecorder()
	if !m.gateAgentRequest(w, r, state.Account{}, state.APIKey{ID: "key"}, rule) || w.Code != 400 || remote.pattern != "" {
		t.Fatalf("unbound request reached approval: %d %s pattern=%s", w.Code, w.Body, remote.pattern)
	}
	if windowedMutationBound(rule, state.AgentApprovalBinding{Method: http.MethodPost}) {
		t.Fatal("unbound mutation reused approval window")
	}
	if !windowedMutationBound(rule, state.AgentApprovalBinding{Method: http.MethodPost, BodySHA256: make([]byte, 32)}) {
		t.Fatal("bound mutation refused")
	}
}
