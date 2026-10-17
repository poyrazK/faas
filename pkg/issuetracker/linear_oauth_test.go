package issuetracker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLinearOAuthProtocolUsesPKCEFormsRotatingTokensAndSanitizedErrors(t *testing.T) {
	cfg := LinearOAuthConfig{"client", "client_secret", "https://api.gregale.test/oauth/linear/callback"}
	authorize, _ := url.Parse(cfg.Authorize("nonce", "challenge"))
	if authorize.Host != "linear.app" || authorize.Query().Get("scope") != "read,write" || authorize.Query().Get("code_challenge_method") != "S256" {
		t.Fatal(authorize)
	}
	for _, kind := range []string{"exchange", "refresh", "missing refresh", "missing scope", "legacy scopes", "expired", "rejected", "failure body", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.linear.app" || r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Fatal("unsafe OAuth destination or encoding")
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if kind == "revoke" {
					if r.URL.Path != "/oauth/revoke" || r.Form.Get("token") != "refresh" || r.Form.Get("token_type_hint") != "refresh_token" {
						t.Fatal("revocation contract", r.Form)
					}
					return response(200, ""), nil
				}
				if r.URL.Path != "/oauth/token" || r.Form.Get("client_id") != "client" || r.Form.Get("client_secret") != "client_secret" {
					t.Fatal("token exchange contract")
				}
				if kind == "refresh" {
					if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" {
						t.Fatal("refresh contract")
					}
				} else if r.Form.Get("code_verifier") != "verifier" || r.Form.Get("redirect_uri") != cfg.RedirectURI {
					t.Fatal("PKCE or redirect not pinned")
				}
				if kind == "rejected" {
					return response(400, `{"error":"invalid_grant","error_description":"secret provider diagnostic"}`), nil
				}
				if kind == "failure body" {
					return response(500, `{"error_description":"secret provider diagnostic"}`), nil
				}
				payload := map[string]any{"access_token": "access", "refresh_token": "rotated", "token_type": "Bearer", "expires_in": 86399, "scope": "read write"}
				switch kind {
				case "missing refresh":
					delete(payload, "refresh_token")
				case "missing scope":
					payload["scope"] = "read"
				case "legacy scopes":
					payload["scope"] = []string{"read", "write"}
				case "expired":
					payload["expires_in"] = 0
				}
				raw, _ := json.Marshal(payload)
				return response(200, string(raw)), nil
			})})
			if kind == "revoke" {
				if err := client.RevokeLinearOAuth(t.Context(), "refresh"); err != nil {
					t.Fatal(err)
				}
				return
			}
			var got LinearOAuthTokens
			var err error
			if kind == "refresh" {
				got, err = client.RefreshLinearOAuth(t.Context(), cfg, "refresh")
			} else {
				got, err = client.ExchangeLinearOAuth(t.Context(), cfg, "code", "verifier")
			}
			valid := kind == "exchange" || kind == "refresh" || kind == "legacy scopes"
			if valid {
				if err != nil || got.AccessToken != "access" || got.RefreshToken != "rotated" || !got.ExpiresAt.After(time.Now().Add(23*time.Hour)) {
					t.Fatal(got, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret provider") {
				t.Fatal("unsafe token failure", err)
			}
			if kind == "rejected" && !errors.Is(err, ErrLinearOAuthRejected) {
				t.Fatal("revoked grant was not classified", err)
			}
		})
	}
}
func TestLinearOAuthConfigFailsClosedAndKeepsSignInSeparate(t *testing.T) {
	for _, values := range []map[string]string{{}, {"FAAS_LINEAR_OAUTH_CLIENT_ID": "id"}, {"FAAS_LINEAR_OAUTH_CLIENT_ID": "id", "FAAS_LINEAR_OAUTH_CLIENT_SECRET": "secret", "FAAS_LINEAR_OAUTH_REDIRECT_URI": "http://evil.test/oauth/linear/callback"}, {"FAAS_LINEAR_OAUTH_CLIENT_ID": "id", "FAAS_LINEAR_OAUTH_CLIENT_SECRET": "secret", "FAAS_LINEAR_OAUTH_REDIRECT_URI": "https://api.test/oauth/linear/callback"}} {
		cfg, err := LoadLinearOAuthConfig(func(key string) string { return values[key] })
		want := len(values) == 3 && strings.HasPrefix(values["FAAS_LINEAR_OAUTH_REDIRECT_URI"], "https:")
		if cfg.Enabled() != want || (len(values) > 0 && !want && err == nil) {
			t.Fatal("unsafe OAuth configuration", cfg.Enabled(), err)
		}
	}
}
func TestLinearOAuthBearerReadsRetainUnauthorizedEvidence(t *testing.T) {
	for _, body := range []string{`{"errors":[{"extensions":{"code":"UNAUTHENTICATED"}}]}`, `{"errors":[{"extensions":{"code":"AUTHENTICATION_ERROR"}}]}`} {
		client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "Bearer oauth_access" {
				t.Fatal("wrong OAuth header")
			}
			return response(200, body), nil
		})})
		_, err := client.VerifyLinearConnection(context.Background(), OAuthAuthorization("oauth_access"), "11111111-1111-4111-8111-111111111111")
		var unauthorized *ProviderUnauthorized
		if !errors.As(err, &unauthorized) {
			t.Fatal("revocation evidence lost", err)
		}
	}
}
func TestLinearOAuthTeamsRejectIncompleteOrChangingScope(t *testing.T) {
	for _, kind := range []string{"valid", "missing paging", "foreign page", "duplicate team", "too many pages"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer access" {
					t.Fatal("wrong team authentication")
				}
				workspace := "11111111-1111-4111-8111-111111111111"
				if kind == "foreign page" && calls > 1 {
					workspace = "22222222-2222-4222-8222-222222222222"
				}
				page := map[string]any{"hasNextPage": kind != "valid", "endCursor": "cursor"}
				if calls > 1 {
					page["hasNextPage"] = false
				}
				if kind == "too many pages" {
					page["hasNextPage"] = true
				}
				team := map[string]any{"id": "33333333-3333-4333-8333-333333333333", "name": "Doing <script>", "archivedAt": nil}
				nodes := []any{team}
				if kind == "duplicate team" {
					nodes = append(nodes, team)
				}
				teams := map[string]any{"nodes": nodes, "pageInfo": page}
				if kind == "missing paging" {
					delete(teams, "pageInfo")
				}
				data, _ := json.Marshal(map[string]any{"data": map[string]any{"organization": map[string]string{"id": workspace}, "viewer": map[string]string{"id": "44444444-4444-4444-8444-444444444444"}, "teams": teams}})
				return response(200, string(data)), nil
			})})
			got, err := client.ListLinearOAuthTeams(t.Context(), "access")
			if kind == "valid" {
				if err != nil || len(got.Teams) != 1 {
					t.Fatal(got, err)
				}
			} else if err == nil {
				t.Fatal("unsafe catalog accepted", kind)
			}
		})
	}
}

func TestLinearOAuthCreationAcceptsInternalBearerAndPreservesStableOperation(t *testing.T) {
	r := requestFixture()
	r.Provider = "linear"
	r.Target = "22222222-2222-4222-8222-222222222222"
	writes := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		writes++
		if req.Header.Get("Authorization") != "Bearer oauth_access" {
			t.Fatal("creation lost OAuth authorization")
		}
		return response(200, linearResponseBody(r, true)), nil
	})})
	ticket, err := client.Create(t.Context(), r, OAuthAuthorization("oauth_access"))
	if err != nil || ticket.ProviderID != r.RequestID || writes != 1 {
		t.Fatal("OAuth creation rejected or changed operation identity", ticket, err)
	}
	if _, err = client.Create(t.Context(), r, "Bearer oauth_access\nsecret"); err == nil || writes != 1 {
		t.Fatal("malformed Bearer reached provider")
	}
}
