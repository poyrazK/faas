// adr: 012 — GitHub API responses must remain bounded at the daemon boundary.
package githubd

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExchangeInstallationTokenRejectsOversizedSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_test","expires_at":"2026-09-17T00:00:00Z"}` + strings.Repeat(" ", githubResponseMaxBytes)))
	}))
	defer server.Close()

	a := &AppAuth{
		AppID:      "1",
		PrivateKey: newTestKey(t),
		HTTPClient: &singleHostClient{base: server.Client(), api: server.URL},
	}
	_, _, err := a.ExchangeInstallationToken(context.Background(), 1)
	if !errors.Is(err, ErrGitHubResponseTooLarge) {
		t.Fatalf("ExchangeInstallationToken() error = %v, want ErrGitHubResponseTooLarge", err)
	}
}

func TestNewHTTPClientHasTimeout(t *testing.T) {
	client := NewHTTPClient()
	if client.Timeout != githubHTTPTimeout {
		t.Fatalf("NewHTTPClient().Timeout = %s, want %s", client.Timeout, githubHTTPTimeout)
	}
}

func TestNewAppAuthUsesBoundedDefaultClient(t *testing.T) {
	key := newTestKey(t)
	keyPEM := marshalTestPrivateKey(t, key)
	a, err := NewAppAuth("1", keyPEM, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	client, ok := a.HTTPClient.(*http.Client)
	if !ok {
		t.Fatalf("HTTPClient type = %T, want *http.Client", a.HTTPClient)
	}
	if client.Timeout != githubHTTPTimeout {
		t.Fatalf("default client timeout = %s, want %s", client.Timeout, githubHTTPTimeout)
	}
}

func marshalTestPrivateKey(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}
