package outbound

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type executionAuthorizerStub struct {
	allowed     bool
	err         error
	identity    ExecutionIdentity
	integration string
	calls       int
}

func (a *executionAuthorizerStub) AuthorizeExecution(_ context.Context, identity ExecutionIdentity, integrationID string) (bool, error) {
	a.calls++
	a.identity = identity
	a.integration = integrationID
	return a.allowed, a.err
}

func TestRunIdentityUsesLiveAuthorizationAndCustomerRouteCeiling(t *testing.T) {
	var received int
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received++
		if r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Errorf("provider authorization = %q", r.Header.Get("Authorization"))
		}
		for _, name := range []string{ExecutionIdentityHeader, WorkloadIdentityHeader, TokenHeader, AppHeader} {
			if r.Header.Get(name) != "" {
				t.Errorf("internal caller header %s reached provider", name)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer provider.Close()

	const integrationID = "customer-integration"
	integration, err := NewIntegration(integrationID, provider.URL, "unused-token", nil, 100, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AccountID = uuid.NewString()
	integration.OwnerKind = IntegrationOwnerCustomer
	integration.ProviderAuthMode = ProviderAuthManaged
	integration.CredentialSource = CredentialSourceCustomerSealed
	integration.AllowedMethods = []string{http.MethodGet}
	integration.AllowedPathPrefixes = []string{"/v1/items"}
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	accountID, executionID, leaseToken := integration.AccountID, uuid.NewString(), uuid.NewString()
	authorizer := &executionAuthorizerStub{allowed: true}
	handler.ExecutionIdentityVerifier = verifier
	handler.ExecutionAuthorizer = authorizer
	handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	request := httptest.NewRequest(http.MethodGet, Prefix+integrationID+"/v1/items/42", nil)
	request.Header.Set(ExecutionIdentityHeader, testExecutionToken(t, signer, time.Now(), accountID, executionID, leaseToken, integrationID))
	request.Header.Set(AppHeader, "spoofed-app")
	request.Header.Set(TokenHeader, "guest-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || received != 1 {
		t.Fatalf("authorized Run request status=%d provider calls=%d body=%s", response.Code, received, response.Body.String())
	}
	if authorizer.calls != 1 || authorizer.integration != integrationID || authorizer.identity.AccountID != accountID || authorizer.identity.ExecutionID != executionID || authorizer.identity.LeaseToken != leaseToken {
		t.Fatalf("authorizer received identity=%+v integration=%q calls=%d", authorizer.identity, authorizer.integration, authorizer.calls)
	}

	// Recheck the live grant on every call. A signed, not-yet-expired token does
	// not survive a grant revocation.
	authorizer.allowed = false
	revoked := httptest.NewRequest(http.MethodGet, Prefix+integrationID+"/v1/items/42", nil)
	revoked.Header.Set(ExecutionIdentityHeader, testExecutionToken(t, signer, time.Now(), accountID, executionID, leaseToken, integrationID))
	revokedResponse := httptest.NewRecorder()
	handler.ServeHTTP(revokedResponse, revoked)
	if revokedResponse.Code != http.StatusForbidden || received != 1 || authorizer.calls != 2 {
		t.Fatalf("revoked Run request status=%d provider calls=%d authorization checks=%d", revokedResponse.Code, received, authorizer.calls)
	}

	// The integration route ceiling is applied to Runs even though no app is
	// attached and there is no app-specific narrowing policy.
	authorizer.allowed = true
	outside := httptest.NewRequest(http.MethodGet, Prefix+integrationID+"/admin", nil)
	outside.Header.Set(ExecutionIdentityHeader, testExecutionToken(t, signer, time.Now(), accountID, executionID, leaseToken, integrationID))
	outsideResponse := httptest.NewRecorder()
	handler.ServeHTTP(outsideResponse, outside)
	if outsideResponse.Code != http.StatusForbidden || received != 1 {
		t.Fatalf("out-of-policy Run route status=%d provider calls=%d", outsideResponse.Code, received)
	}
}

func TestRunIdentityFailsClosedWithoutAuthorizerAndOnStoreError(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer provider.Close()
	integration, err := NewIntegration("customer-integration", provider.URL, "unused-token", nil, 100, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.OwnerKind = IntegrationOwnerCustomer
	integration.ProviderAuthMode = ProviderAuthManaged
	integration.CredentialSource = CredentialSourceCustomerSealed
	integration.AllowedMethods = []string{http.MethodGet}
	integration.AllowedPathPrefixes = []string{"/v1"}
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	handler.ExecutionIdentityVerifier = verifier
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, Prefix+integration.ID+"/v1", nil)
		r.Header.Set(ExecutionIdentityHeader, testExecutionToken(t, signer, time.Now(), uuid.NewString(), uuid.NewString(), uuid.NewString(), integration.ID))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		return response
	}
	if got := request(); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("request without live authorizer status=%d body=%s", got.Code, got.Body.String())
	}
	handler.ExecutionAuthorizer = &executionAuthorizerStub{err: errors.New("database unavailable")}
	if got := request(); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("request with authorizer error status=%d body=%s", got.Code, got.Body.String())
	}
}
