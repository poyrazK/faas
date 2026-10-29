package outbound

// adr: 20261027090010 — agent runs reach their own integrations through the relay,
// attributed to their tenant and held to their model spend cap.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type agentRunAuthorizerStub struct {
	grant       AgentRunGrant
	allowed     bool
	err         error
	calls       int
	integration string
}

func (a *agentRunAuthorizerStub) AuthorizeAgentRun(_ context.Context, _ AgentRunIdentity, integrationID string) (AgentRunGrant, bool, error) {
	a.calls++
	a.integration = integrationID
	return a.grant, a.allowed, a.err
}

// agentRunLedgerStub is an in-memory spend ledger with the Postgres one's
// rules: settled spend plus live reservations plus the call must fit.
type agentRunLedgerStub struct {
	mu       sync.Mutex
	spent    int64
	held     map[string]int64
	reserves []int64
	settled  []int64
	released int
	err      error
}

func (l *agentRunLedgerStub) ReserveAgentRunSpend(_ context.Context, _, _, leaseID string, budget, reserve int64, _ time.Time) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return false, l.err
	}
	var held int64
	for _, v := range l.held {
		held += v
	}
	l.reserves = append(l.reserves, reserve)
	if l.spent >= budget || l.spent+held+reserve > budget {
		return false, nil
	}
	if l.held == nil {
		l.held = map[string]int64{}
	}
	l.held[leaseID] = reserve
	return true, nil
}

func (l *agentRunLedgerStub) SettleAgentRunSpend(_ context.Context, _, leaseID string, cost int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, leaseID)
	l.spent += cost
	l.settled = append(l.settled, cost)
	return nil
}

func (l *agentRunLedgerStub) ReleaseAgentRunSpend(_ context.Context, leaseID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, leaseID)
	l.released++
	return nil
}

func agentRunClaims() workloadidentity.AgentRunClaims {
	return workloadidentity.AgentRunClaims{AccountID: uuid.NewString(), RunID: uuid.NewString(), LeaseToken: uuid.NewString(),
		TenantID: uuid.NewString(), AgentID: uuid.NewString(), AgentVersion: 3}
}

func TestReadRelayPrincipalAcceptsAgentRuns(t *testing.T) {
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	claims := agentRunClaims()
	token, err := signer.MintAgentRun(time.Now(), claims, workloadidentity.OutboundRelayAudience)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := readRelayPrincipal(preambleConn(t, relaywire.Preamble{Assertion: token.BearerValue()}), verifier)
	if err != nil || principal.agentRun == nil || principal.agentRun.RunID != claims.RunID || principal.agentRun.TenantID != claims.TenantID ||
		principal.agentRun.AgentVersion != 3 || principal.accountID() != claims.AccountID || principal.execution != nil {
		t.Fatalf("agent run principal = %+v, %v", principal, err)
	}
	if _, err := readRelayPrincipal(preambleConn(t, relaywire.Preamble{Assertion: token.BearerValue(),
		ExecutionIntegrations: []string{uuid.NewString()}}), verifier); err == nil {
		t.Fatal("an agent run assertion passed as a Run with a grant list")
	}
	other, err := signer.MintAgentRun(time.Now(), claims, identityAudiencePrefix+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifyAgentRunRelay(other.BearerValue()); err == nil {
		t.Fatal("an agent run assertion for another audience was accepted on the relay")
	}
	execution, err := signer.MintExecution(time.Now(), claims.AccountID, claims.RunID, claims.LeaseToken, workloadidentity.OutboundRelayAudience)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.VerifyAgentRunRelay(execution.BearerValue()); err == nil {
		t.Fatal("a Run assertion passed as an agent run")
	}
	if _, err := verifier.VerifyExecutionRelay(token.BearerValue()); err == nil {
		t.Fatal("an agent run assertion passed as a Run")
	}
	if _, err := verifier.VerifyRelay(token.BearerValue()); err == nil {
		t.Fatal("an agent run assertion passed as an app")
	}
}

func agentRunHandler(t *testing.T, priced bool) (*Handler, *MemoryBackend, *agentRunAuthorizerStub, *agentRunLedgerStub) {
	t.Helper()
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","usage":{"prompt_tokens":3000,"completion_tokens":2000}}`))
	}))
	t.Cleanup(provider.Close)
	integration, err := NewIntegration("granted", provider.URL, "unused", nil, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AccountID = "account-1"
	integration.OwnerKind, integration.AccountPlan = IntegrationOwnerCustomer, api.PlanPro
	integration.ProviderAuthMode = ProviderAuthManaged
	integration.CredentialSource = CredentialSourceCustomerSealed
	integration.AllowedMethods = []string{http.MethodPost}
	integration.AllowedPathPrefixes = []string{"/v1"}
	integration.AIProvider = "openai"
	if priced {
		integration.AIPrices = []AIPrice{{ModelPrefix: "*", Input: 100_000, CachedInput: 10_000, CacheWrite: 100_000, Output: 400_000}}
	}
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	authorizer := &agentRunAuthorizerStub{allowed: true, grant: AgentRunGrant{TenantID: "tenant-7", AIBudgetMillicents: 200_000}}
	ledger := &agentRunLedgerStub{}
	handler.AgentRunAuthorizer, handler.AgentRunSpend = authorizer, ledger
	return handler, backend, authorizer, ledger
}

func agentRunCall(handler *Handler, guestUser string) *httptest.ResponseRecorder {
	run := AgentRunIdentity{AccountID: "account-1", RunID: "run-1", LeaseToken: "lease-1", AgentID: "agent-1", AgentVersion: 1}
	ctx := withRelayPrincipal(context.Background(), relayPrincipal{agentRun: &run})
	req := httptest.NewRequest(http.MethodPost, Prefix+"granted/v1/chat/completions", bytes.NewReader([]byte(`{}`))).WithContext(ctx)
	if guestUser != "" {
		req.Header.Set(api.OutboundAIUserHeader, guestUser)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAgentRunRelayCallsAreAuthorizedAttributedAndCapped(t *testing.T) {
	handler, backend, authorizer, ledger := agentRunHandler(t, true)
	if resp := agentRunCall(handler, "guest-picked"); resp.Code != http.StatusOK || authorizer.calls != 1 || authorizer.integration != "granted" {
		t.Fatalf("agent run call = %d %s, authorizer calls=%d", resp.Code, resp.Body.String(), authorizer.calls)
	}
	if len(ledger.reserves) != 1 || ledger.reserves[0] < 1 || len(ledger.settled) != 1 || ledger.settled[0] < 1 || len(ledger.held) != 0 {
		t.Fatalf("ledger reserves=%v settled=%v held=%v", ledger.reserves, ledger.settled, ledger.held)
	}
	if _, ok := backend.AIUserUsage("granted", "tenant-7"); !ok {
		t.Fatal("the call was not attributed to the run's tenant")
	}
	if _, ok := backend.AIUserUsage("granted", "guest-picked"); ok {
		t.Fatal("the guest chose its own end user")
	}

	ledger.spent = 200_000
	resp := agentRunCall(handler, "")
	if resp.Code != http.StatusForbidden || !bytes.Contains(resp.Body.Bytes(), []byte("outbound_agent_run_budget_exhausted")) {
		t.Fatalf("over-cap call = %d %s", resp.Code, resp.Body.String())
	}
	if len(ledger.settled) != 1 {
		t.Fatalf("a refused call settled: %v", ledger.settled)
	}

	authorizer.allowed = false
	if resp := agentRunCall(handler, ""); resp.Code != http.StatusForbidden || !bytes.Contains(resp.Body.Bytes(), []byte("outbound_agent_run_not_authorized")) {
		t.Fatalf("unauthorized call = %d %s", resp.Code, resp.Body.String())
	}
	authorizer.allowed, authorizer.err = true, context.DeadlineExceeded
	if resp := agentRunCall(handler, ""); resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("authorizer error = %d, want 503", resp.Code)
	}
	handler.AgentRunAuthorizer = nil
	if resp := agentRunCall(handler, ""); resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("agent runs off = %d, want 503", resp.Code)
	}
}

func TestAgentRunRelayRefusesUnpricedCalls(t *testing.T) {
	handler, _, _, ledger := agentRunHandler(t, false)
	resp := agentRunCall(handler, "")
	if resp.Code != http.StatusForbidden || !bytes.Contains(resp.Body.Bytes(), []byte("outbound_agent_run_unpriced")) {
		t.Fatalf("unpriced call = %d %s", resp.Code, resp.Body.String())
	}
	if len(ledger.reserves) != 0 {
		t.Fatalf("an unpriced call reserved spend: %v", ledger.reserves)
	}
}
