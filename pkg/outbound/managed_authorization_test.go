package outbound

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagedAuthorizationOverridesGuestOnlyForConfiguredIntegration(t *testing.T) {
	var received []string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(WorkloadIdentityHeader) != "" || r.Header.Get(TokenHeader) != "" || r.Header.Get(AppHeader) != "" {
			t.Error("internal identity headers reached provider")
		}
		received = append(received, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("ok"))
	}))
	defer provider.Close()

	managed, err := NewIntegration("managed", provider.URL, "managed-token", []string{"app-1"}, 100, 3, 3, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	managed.ProviderAuthMode = ProviderAuthManaged
	legacy, err := NewIntegration("legacy", provider.URL, "legacy-token", []string{"app-1"}, 100, 3, 3, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewStaticResolver([]Integration{managed, legacy})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	authorizations := map[string]string{"managed": "Bearer provider-secret"}
	if err := handler.SetManagedAuthorizations(authorizations); err != nil {
		t.Fatal(err)
	}
	signer, jwks := testWorkloadSigner(t, "https://identity.gregale.dev")
	verifier, err := NewWorkloadIdentityVerifier(jwks, "https://identity.gregale.dev")
	if err != nil {
		t.Fatal(err)
	}
	handler.IdentityVerifier = verifier
	identityToken := testWorkloadToken(t, signer, time.Now(), "app-1", managed.ID)
	authorizations["managed"] = "Bearer changed-after-configuration"

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, gatewayRequest(Prefix+managed.ID+"/v1/items", "wrong-token", "app-1", http.MethodGet, nil))
	if unauthorized.Code != http.StatusUnauthorized || len(received) != 0 {
		t.Fatalf("unauthorized request status=%d provider calls=%d", unauthorized.Code, len(received))
	}
	noVerifier, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := noVerifier.SetManagedAuthorizations(map[string]string{"managed": "Bearer provider-secret"}); err != nil {
		t.Fatal(err)
	}
	noVerifierRequest := gatewayRequest(Prefix+managed.ID+"/v1/items", "managed-token", "app-1", http.MethodGet, nil)
	noVerifierRequest.Header.Set(WorkloadIdentityHeader, identityToken)
	noVerifierResponse := httptest.NewRecorder()
	noVerifier.ServeHTTP(noVerifierResponse, noVerifierRequest)
	if noVerifierResponse.Code != http.StatusServiceUnavailable || len(received) != 0 {
		t.Fatalf("missing verifier status=%d provider calls=%d", noVerifierResponse.Code, len(received))
	}
	unconfiguredHandler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	unconfigured := httptest.NewRecorder()
	unconfiguredHandler.IdentityVerifier = verifier
	unconfiguredRequest := gatewayRequest(Prefix+managed.ID+"/v1/items", "managed-token", "app-1", http.MethodGet, nil)
	unconfiguredRequest.Header.Set(WorkloadIdentityHeader, identityToken)
	unconfiguredHandler.ServeHTTP(unconfigured, unconfiguredRequest)
	if unconfigured.Code != http.StatusServiceUnavailable || len(received) != 0 {
		t.Fatalf("unconfigured replica status=%d provider calls=%d", unconfigured.Code, len(received))
	}

	managedRequest := gatewayRequest(Prefix+managed.ID+"/v1/items", "managed-token", "app-1", http.MethodGet, nil)
	managedRequest.Header.Del(TokenHeader)
	managedRequest.Header.Set(AppHeader, "spoofed-app")
	managedRequest.Header.Set(WorkloadIdentityHeader, identityToken)
	managedRequest.Header.Set("Authorization", "Bearer guest-supplied")
	managedResponse := httptest.NewRecorder()
	handler.ServeHTTP(managedResponse, managedRequest)
	if managedResponse.Code != http.StatusOK || len(received) != 1 || received[0] != "Bearer provider-secret" {
		t.Fatalf("managed request status=%d provider authorization=%q", managedResponse.Code, received)
	}
	if strings.Contains(managedResponse.Body.String(), "provider-secret") || strings.Contains(managedResponse.Header().Get("Authorization"), "provider-secret") {
		t.Fatal("provider credential leaked into gateway response")
	}
	otherApp := gatewayRequest(Prefix+managed.ID+"/v1/items", "managed-token", "app-1", http.MethodGet, nil)
	otherApp.Header.Set(WorkloadIdentityHeader, testWorkloadToken(t, signer, time.Now(), "other-app", managed.ID))
	forbidden := httptest.NewRecorder()
	handler.ServeHTTP(forbidden, otherApp)
	if forbidden.Code != http.StatusForbidden || len(received) != 1 {
		t.Fatalf("unattached app status=%d provider calls=%d", forbidden.Code, len(received))
	}

	legacyRequest := gatewayRequest(Prefix+legacy.ID+"/v1/items", "legacy-token", "app-1", http.MethodGet, nil)
	legacyRequest.Header.Set("Authorization", "Bearer guest-supplied")
	legacyResponse := httptest.NewRecorder()
	handler.ServeHTTP(legacyResponse, legacyRequest)
	if legacyResponse.Code != http.StatusOK || len(received) != 2 || received[1] != "Bearer guest-supplied" {
		t.Fatalf("legacy request status=%d provider authorization=%q", legacyResponse.Code, received)
	}
}

func TestManagedAuthorizationRejectsUnsafeValuesWithoutReplacingConfiguration(t *testing.T) {
	handler, err := NewHandler(&StaticResolver{}, NewMemoryBackend(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.SetManagedAuthorizations(map[string]string{"one": "Bearer original"}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", " Bearer key", "Bearer key\nInjected: yes", "Bearer\tkey", "Bearer café", strings.Repeat("a", 8193)} {
		if err := handler.SetManagedAuthorizations(map[string]string{"one": value}); err == nil {
			t.Errorf("unsafe Authorization value was accepted")
		}
		if got := handler.managedAuthorization["one"]; got != "Bearer original" {
			t.Fatalf("failed update replaced active credential: %q", got)
		}
	}
}
