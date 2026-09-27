package outbound

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type changingCredential struct {
	value string
}

func (c *changingCredential) Authorization(_ context.Context, _ string) (string, error) {
	if c.value == "" {
		return "", errors.New("credential missing")
	}
	return c.value, nil
}

func TestCustomerSealedCredentialRotationAndRevocation(t *testing.T) {
	var received []string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer provider.Close()
	integration, err := NewIntegration("managed", provider.URL, "unused-token", []string{"app-1"}, 100, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
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
	signer, jwks := testWorkloadSigner(t, "https://identity.gregale.dev")
	handler.IdentityVerifier, err = NewWorkloadIdentityVerifier(jwks, "https://identity.gregale.dev")
	if err != nil {
		t.Fatal(err)
	}
	credential := &changingCredential{}
	handler.CredentialResolver = credential
	call := func() int {
		t.Helper()
		r := gatewayRequest(Prefix+integration.ID+"/v1/items", "unused-token", "app-1", http.MethodGet, nil)
		r.Header.Set(WorkloadIdentityHeader, testWorkloadToken(t, signer, time.Now(), "app-1", integration.ID))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if status := call(); status != http.StatusServiceUnavailable || len(received) != 0 {
		t.Fatalf("unconfigured credential: status=%d provider calls=%d", status, len(received))
	}
	credential.value = "Bearer first"
	if status := call(); status != http.StatusNoContent || len(received) != 1 || received[0] != "Bearer first" {
		t.Fatalf("first credential: status=%d received=%v", status, received)
	}
	credential.value = "Bearer second"
	if status := call(); status != http.StatusNoContent || len(received) != 2 || received[1] != "Bearer second" {
		t.Fatalf("rotated credential: status=%d received=%v", status, received)
	}
	credential.value = ""
	if status := call(); status != http.StatusServiceUnavailable || len(received) != 2 {
		t.Fatalf("revoked credential: status=%d received=%v", status, received)
	}
}
