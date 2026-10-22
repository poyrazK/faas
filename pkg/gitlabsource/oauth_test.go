package gitlabsource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGitLabOAuthAuthorizationExchangeAndRefresh(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/oauth/token" || r.Method != http.MethodPost {
			t.Errorf("unexpected OAuth endpoint %s %s", r.Method, r.URL.Path)
			return
		}
		if r.FormValue("client_id") != "id" || r.FormValue("client_secret") != "secret" || r.FormValue("redirect_uri") != "https://gregale.example/v1/gitlab/callback" {
			t.Error("OAuth request did not bind application and redirect URI")
		}
		switch r.FormValue("grant_type") {
		case "authorization_code":
			if r.FormValue("code") != "code" || r.FormValue("code_verifier") != strings.Repeat("v", 43) {
				t.Error("OAuth code or PKCE verifier missing")
			}
		case "refresh_token":
			if r.FormValue("refresh_token") != "refresh-one" {
				t.Error("refresh token missing")
			}
		default:
			t.Error("unknown OAuth grant")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh-one", "expires_in": 7200, "token_type": "bearer"})
	}))
	defer server.Close()
	config := OAuthConfig{ClientID: "id", ClientSecret: "secret", RedirectURI: "https://gregale.example/v1/gitlab/callback", OAuthURL: server.URL + "/oauth", HTTP: server.Client()}
	authorize, err := config.AuthorizationURL(strings.Repeat("a", 64), strings.Repeat("b", 43))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authorize)
	if u.Query().Get("scope") != "read_api read_repository" || u.Query().Get("state") != strings.Repeat("a", 64) || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("OAuth authorization URL incomplete: %s", authorize)
	}
	tokens, err := config.Exchange(context.Background(), "code", strings.Repeat("v", 43))
	if err != nil || tokens.Access != "access" {
		t.Fatalf("code exchange = %+v, %v", tokens, err)
	}
	refreshed, err := config.Refresh(context.Background(), tokens.Refresh)
	if err != nil || refreshed.Refresh != "refresh-one" {
		t.Fatalf("refresh = %+v, %v", refreshed, err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestGitLabOAuthRejectsMissingReadScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 7200, "token_type": "bearer", "scope": "api"})
	}))
	defer server.Close()
	config := OAuthConfig{ClientID: "id", ClientSecret: "secret", RedirectURI: "https://gregale.example/v1/gitlab/callback", OAuthURL: server.URL, HTTP: server.Client()}
	if _, err := config.Exchange(context.Background(), "code", strings.Repeat("v", 43)); err == nil {
		t.Fatal("wider OAuth scope accepted")
	}
}
