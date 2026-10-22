package mcphosting

import (
	"context"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOAuthProviderQualification(t *testing.T) {
	for _, scenario := range []string{"healthy", "oauth_metadata", "partial_metadata", "issuer_mismatch", "jwks_mismatch", "malformed", "oversized", "private_key", "invalid_key", "redirect", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			var server *httptest.Server
			redirects := 0
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("credentials leaked to provider")
				}
				if r.URL.Path == "/redirect-target" {
					redirects++
					return
				}
				if scenario == "deadline" {
					<-r.Context().Done()
					return
				}
				if scenario == "redirect" {
					http.Redirect(w, r, server.URL+"/redirect-target", 302)
					return
				}
				if scenario == "malformed" {
					_, _ = w.Write([]byte("private sentinel invalid JSON"))
					return
				}
				if scenario == "oversized" {
					_, _ = w.Write([]byte(strings.Repeat("x", 65537)))
					return
				}
				if scenario == "partial_metadata" {
					if strings.HasSuffix(r.URL.Path, "openid-configuration") {
						body, _ := json.Marshal(map[string]string{"issuer": server.URL + "/tenant", "jwks_uri": server.URL + "/keys"})
						_, _ = w.Write(append(body[:len(body)-1], []byte(",\"issuer\":7}")...))
						return
					}
					if r.URL.Path == "/.well-known/oauth-authorization-server/tenant" {
						_, _ = w.Write([]byte("null"))
						return
					}
				}
				if scenario == "oauth_metadata" && strings.HasSuffix(r.URL.Path, "openid-configuration") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if strings.HasSuffix(r.URL.Path, "openid-configuration") || r.URL.Path == "/.well-known/oauth-authorization-server/tenant" {
					issuer, jwks := server.URL+"/tenant", server.URL+"/keys"
					if scenario == "issuer_mismatch" {
						issuer += "/wrong"
					}
					if scenario == "jwks_mismatch" {
						jwks += "/untrusted"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": jwks})
					return
				}
				if r.URL.Path != "/keys" {
					t.Errorf("followed unreviewed provider URL: %s", r.URL.Path)
				}
				curve := elliptic.P256().Params()
				key := map[string]string{"kty": "EC", "crv": "P-256", "alg": "ES256", "x": base64.RawURLEncoding.EncodeToString(curve.Gx.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(curve.Gy.FillBytes(make([]byte, 32)))}
				if scenario == "private_key" {
					key["d"] = "PRIVATE_SENTINEL"
				}
				if scenario == "invalid_key" {
					key["x"] = "invalid"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{key}})
			}))
			defer server.Close()
			client := server.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			timeout := 3 * time.Second
			if scenario == "deadline" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			checks := checkOAuthProvider(ctx, client, AuthConfig{Mode: "external-oauth", Issuer: server.URL + "/tenant", JWKSURL: server.URL + "/keys"})
			passed := true
			for _, check := range checks {
				passed = passed && check.Status == "passed"
				if strings.Contains(check.Detail, "SENTINEL") {
					t.Fatal("provider body leaked")
				}
			}
			if passed != (scenario == "healthy" || scenario == "oauth_metadata") || redirects != 0 {
				t.Fatalf("unexpected result: %+v redirects=%d", checks, redirects)
			}
		})
	}
}

func TestOAuthProviderRejectsUnsafeConfiguration(t *testing.T) {
	for _, issuer := range []string{"http://issuer.example", "https://user:password@issuer.example", "https://issuer.example?token=secret"} {
		checks := CheckOAuthProvider(context.Background(), AuthConfig{Issuer: issuer, JWKSURL: "https://issuer.example/keys"})
		if len(checks) != 1 || checks[0].Status != "failed" {
			t.Fatal(checks)
		}
	}
}
