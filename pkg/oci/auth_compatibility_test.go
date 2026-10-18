// adr: 062
// adr: 054
package oci

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryPullAcceptsCaseInsensitiveBearerChallenge(t *testing.T) {
	for _, tc := range []struct {
		name, scheme, realm, service, scope string
	}{
		{"lowercase scheme", "bearer", "realm", "service", "scope"},
		{"uppercase scheme", "BEARER", "realm", "service", "scope"},
		{"mixed case parameters", "Bearer", "ReAlM", "SERVICE", "Scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, password, ok := r.BasicAuth()
				if !ok || user != "user" || password != "password" {
					t.Error("token endpoint did not receive registry credentials")
				}
				if r.URL.Query().Get("service") != "Registry" || r.URL.Query().Get("scope") != "repository:Org/App:pull" {
					t.Errorf("challenge values changed: %v", r.URL.Query())
				}
				_, _ = io.WriteString(w, `{"token":"registry-token"}`)
			}))
			t.Cleanup(tokenServer.Close)
			const digest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer registry-token" {
					challenge := fmt.Sprintf(`%s %s=%q,%s="Registry",%s="repository:Org/App:pull"`, tc.scheme, tc.realm, tokenServer.URL, tc.service, tc.scope)
					w.Header().Set("WWW-Authenticate", challenge)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
				w.Header().Set("Docker-Content-Digest", digest)
				_, _ = io.WriteString(w, `{"schemaVersion":2}`)
			}))
			t.Cleanup(registry.Close)
			client := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(registry.URL, "http://")))
			got, err := client.PullDigestWithAuth(t.Context(), "example.com/org/app:latest", &BasicAuth{Username: "user", Password: "password"})
			if err != nil || got != digest {
				t.Fatalf("registry pull = %q, %v; want %s", got, err, digest)
			}
		})
	}
}

func TestRefreshTokenRetainsGrantWhenProviderDoesNotRotateIt(t *testing.T) {
	for _, replacement := range []string{"", "rotated-grant"} {
		t.Run("replacement="+replacement, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Method != http.MethodPost || r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "original-grant" {
					t.Errorf("refresh request = %s %v", r.Method, r.PostForm)
				}
				if replacement == "" {
					_, _ = io.WriteString(w, `{"access_token":"fresh-access","expires_in":3600}`)
				} else {
					_, _ = fmt.Fprintf(w, `{"access_token":"fresh-access","refresh_token":%q,"expires_in":3600}`, replacement)
				}
			}))
			t.Cleanup(server.Close)
			token, err := RefreshToken(t.Context(), server.Client(), "", server.URL, "original-grant", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := replacement
			if want == "" {
				want = "original-grant"
			}
			if token.AccessToken != "fresh-access" || token.RefreshToken != want {
				t.Fatalf("token = %+v; want fresh access token and refresh grant %q", token, want)
			}
		})
	}
}
