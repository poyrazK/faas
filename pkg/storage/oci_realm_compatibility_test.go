// adr: 054
package storage

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOCIBearerRealmCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		registry string
		realm    string
		allowed  bool
	}{
		{"same hostname different ports", "https://registry.example:5443", "https://registry.example:8443/token", true},
		{"explicit default port", "https://ghcr.io:443", "https://ghcr.io/token", true},
		{"case insensitive hostname", "https://GHCR.IO", "https://ghcr.io/token", true},
		{"subdomain with mixed case", "https://ghcr.io", "https://AUTH.GHCR.IO:8443/token", true},
		{"Docker Hub", "https://registry-1.docker.io", "https://auth.docker.io/token", true},
		{"Docker Hub mixed case and port", "https://REGISTRY-1.DOCKER.IO:443", "https://AUTH.DOCKER.IO:443/token", true},
		{"unrelated host", "https://ghcr.io", "https://attacker.example/token", false},
		{"unrecognized sibling", "https://registry.example.com", "https://auth.example.com/token", false},
		{"Docker hostname prefix", "https://registry-1.docker.io.evil.example", "https://auth.docker.io/token", false},
		{"Docker realm prefix", "https://registry-1.docker.io", "https://auth.docker.io.evil.example/token", false},
		{"different Docker sibling", "https://registry-1.docker.io", "https://other.docker.io/token", false},
		{"cleartext Docker realm", "https://registry-1.docker.io", "http://auth.docker.io/token", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: realmRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if !tc.allowed {
					t.Error("credentials sent to a rejected realm")
				}
				user, password, ok := req.BasicAuth()
				if !ok || user != "test-user" || password != "test-password" {
					t.Error("token request omitted configured Basic credentials")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"token":"test-bearer","expires_in":3600}`)),
					Request:    req,
				}, nil
			})}
			backend, err := NewOCIRegistryStorageBackend(WithRegistry(tc.registry), WithCredentials("test-user", "test-password"), WithHTTPClient(client))
			if err != nil {
				t.Fatal(err)
			}
			token, err := backend.bearerForChallenge(t.Context(), fmt.Sprintf(`Bearer realm=%q,service="registry"`, tc.realm), "repository:faas/apps:pull")
			if tc.allowed {
				if err != nil || token != "test-bearer" || calls != 1 {
					t.Errorf("token exchange = %q, %v (%d requests); want successful exchange", token, err, calls)
				}
			} else if err == nil || token != "" || calls != 0 {
				t.Errorf("untrusted token exchange = %q, %v (%d requests); want rejection before sending credentials", token, err, calls)
			}
		})
	}
}

type realmRoundTripper func(*http.Request) (*http.Response, error)

func (fn realmRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
