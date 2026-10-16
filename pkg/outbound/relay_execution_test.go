package outbound

// adr: 990 — Runs stream through the relay and stay bound to their grant.

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// pipeConn feeds a preamble through readRelayPrincipal without a listener.
type pipeConn struct {
	net.Conn
	reader *bytes.Reader
}

func (c pipeConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c pipeConn) SetDeadline(time.Time) error { return nil }
func (c pipeConn) RemoteAddr() net.Addr        { return &net.TCPAddr{} }
func preambleConn(t *testing.T, p relaywire.Preamble) pipeConn {
	t.Helper()
	var buf bytes.Buffer
	if err := relaywire.Write(&buf, p); err != nil {
		t.Fatal(err)
	}
	return pipeConn{reader: bytes.NewReader(buf.Bytes())}
}

func TestReadRelayPrincipalAcceptsRunsWithTheirGrant(t *testing.T) {
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	accountID, executionID, lease, granted := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	token, err := signer.MintExecution(time.Now(), accountID, executionID, lease, workloadidentity.OutboundRelayAudience)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := readRelayPrincipal(preambleConn(t, relaywire.Preamble{Assertion: token.BearerValue(), ExecutionIntegrations: []string{granted}}), verifier)
	if err != nil || principal.execution == nil || principal.execution.ExecutionID != executionID || principal.accountID() != accountID {
		t.Fatalf("Run principal = %+v, %v", principal, err)
	}
	if _, ok := principal.executionIntegrations[granted]; !ok {
		t.Fatalf("grant list = %v", principal.executionIntegrations)
	}
	if _, err := readRelayPrincipal(preambleConn(t, relaywire.Preamble{Assertion: token.BearerValue()}), verifier); err == nil {
		t.Fatal("Run relay without a grant list was accepted")
	}
	perIntegration, err := signer.MintExecution(time.Now(), accountID, executionID, lease, identityAudiencePrefix+granted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readRelayPrincipal(preambleConn(t, relaywire.Preamble{Assertion: perIntegration.BearerValue(), ExecutionIntegrations: []string{granted}}), verifier); err == nil {
		t.Fatal("per-integration Run assertion was accepted on the relay")
	}
}

func TestRunRelayRequestsUseTheGrantAndAuthorizer(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	defer provider.Close()
	customer := func(id string) Integration {
		integration, err := NewIntegration(id, provider.URL, "unused", nil, 100, 10, 10, 5*time.Second)
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
		return integration
	}
	resolver, err := NewStaticResolver([]Integration{customer("granted"), customer("other")})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &executionAuthorizerStub{allowed: true}
	handler.ExecutionAuthorizer = authorizer
	handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	execution := ExecutionIdentity{AccountID: "account-1", ExecutionID: "run-1", LeaseToken: "lease-1"}
	ctx := withRelayPrincipal(context.Background(), relayPrincipal{execution: &execution, executionIntegrations: map[string]struct{}{"granted": {}}})
	call := func(integration string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, Prefix+integration+"/v1/chat/completions", bytes.NewReader([]byte(`{}`))).WithContext(ctx)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	if resp := call("granted"); resp.Code != http.StatusOK || authorizer.calls != 1 || authorizer.integration != "granted" {
		t.Fatalf("granted Run call = %d, authorizer calls=%d integration=%q", resp.Code, authorizer.calls, authorizer.integration)
	}
	if row, ok := backend.AIUsageRollup("granted", AIUsageCallerRuns, "m"); !ok || row.Requests != 1 {
		t.Fatalf("Run usage was not attributed to runs: %+v %v", row, ok)
	}
	if resp := call("other"); resp.Code != http.StatusForbidden || authorizer.calls != 1 {
		t.Fatalf("ungranted Run call = %d, authorizer calls=%d", resp.Code, authorizer.calls)
	}
	authorizer.allowed = false
	if resp := call("granted"); resp.Code != http.StatusForbidden {
		t.Fatalf("revoked grant call = %d", resp.Code)
	}
}
