package outbound

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261027090010 — on Postgres, an agent run's relayed call admits through the
// account's own AI integration with no app binding: its run grant
// authorized it. An app admission without its binding is still refused.
func TestPostgresAdmitsAgentRunCallsWithoutABinding(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	if err := f.store.SetOutboundAIPrices(ctx, f.account.ID, f.offer.ID, []api.OutboundAIPriceEntry{
		{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: 250_000, OutputMillicentsPerMTok: 1_000_000}}); err != nil {
		t.Fatal(err)
	}
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	unbound := f.spec()
	unbound.BindingAppID = ""
	if _, err := backend.Admit(ctx, unbound); !errors.Is(err, ErrInvalidIntegration) {
		t.Fatalf("an app admission without its binding = %v, want refused", err)
	}

	handler := f.handler(nil, backend)
	ledger := &agentRunLedgerStub{}
	handler.AgentRunAuthorizer = &agentRunAuthorizerStub{allowed: true, grant: AgentRunGrant{AIBudgetMillicents: 200_000}}
	handler.AgentRunSpend = ledger
	run := AgentRunIdentity{AccountID: f.account.ID, RunID: uuid.NewString(), LeaseToken: "lease-1", AgentID: uuid.NewString(), AgentVersion: 1}
	request := httptest.NewRequestWithContext(withRelayPrincipal(ctx, relayPrincipal{agentRun: &run}), http.MethodPost,
		"http://outboundd"+Prefix+f.offer.ID+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","max_tokens":10,"messages":[{"role":"user","content":"ping"}]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("agent run call = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(ledger.reserves) != 1 || len(ledger.settled) != 1 {
		t.Fatalf("run spend reserves=%v settled=%v, want one call held and settled", ledger.reserves, ledger.settled)
	}
}
