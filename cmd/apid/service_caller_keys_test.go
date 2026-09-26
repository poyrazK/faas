package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/servicecaller"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceCallerKeysEndpointIsPublicAndPublishesOnlyVerifierKeys(t *testing.T) {
	store := state.NewMemStore()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	if err := store.PublishServiceCallerKey(t.Context(), state.ServiceCallerKey{
		NodeID: "node-1", KeyID: servicecaller.KeyID(publicKey), PublicKeyPEM: publicPEM,
	}); err != nil {
		t.Fatal(err)
	}

	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "apps.example.test", noopNotifier{})
	req := httptest.NewRequest(http.MethodGet, "/v1/service-caller-keys", nil)
	response := httptest.NewRecorder()
	srv.handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/jwk-set+json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != serviceCallerKeysCacheControl {
		t.Errorf("Cache-Control = %q", got)
	}
	if strings.Contains(response.Body.String(), privatePEM) {
		t.Fatal("public key response contains the private signing key")
	}
	var responseSet api.ServiceCallerJWKSet
	if err := json.Unmarshal(response.Body.Bytes(), &responseSet); err != nil {
		t.Fatal(err)
	}
	if len(responseSet.Keys) != 1 || responseSet.Keys[0].Kid != servicecaller.KeyID(publicKey) {
		t.Fatalf("key set = %+v", responseSet)
	}
	serialized, err := json.Marshal(responseSet)
	if err != nil {
		t.Fatal(err)
	}
	trustedKeys, err := servicecaller.TrustedKeysFromJWKS(serialized)
	if err != nil {
		t.Fatalf("public endpoint emitted an unverifiable key set: %v", err)
	}
	if len(trustedKeys) != 1 {
		t.Fatalf("trusted key count = %d", len(trustedKeys))
	}
}

func TestServiceCallerKeysEndpointReturnsEmptySetAndFailsClosedOnCorruptStore(t *testing.T) {
	store := state.NewMemStore()
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "apps.example.test", noopNotifier{})
	request := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		srv.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/service-caller-keys", nil))
		return response
	}
	response := request()
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"keys":[]}` {
		t.Fatalf("empty set = %d %s", response.Code, response.Body.String())
	}
	if err := store.PublishServiceCallerKey(t.Context(), state.ServiceCallerKey{
		NodeID: "bad-node", KeyID: "not-a-real-key-id", PublicKeyPEM: "not PEM",
	}); err != nil {
		t.Fatal(err)
	}
	response = request()
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt key status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "not PEM") {
		t.Fatal("corrupt key response leaked the stored value")
	}
}
