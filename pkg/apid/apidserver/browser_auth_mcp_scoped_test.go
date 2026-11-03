// adr: 20261026195952
package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPScopedBrowserAuthGrantsExactlyTheConsentedScopes(t *testing.T) {
	srv, store := newCliAuthTestServer(t)
	body, _ := json.Marshal(api.CliAuthCodeRequest{Purpose: api.CliAuthPurposeMCP, Scopes: []string{api.ScopeManagedPostgresManage, api.ScopeDeployWrite, api.ScopeSecretsRequest}})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/cli-auth/code", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("mint=%d %s", rec.Code, rec.Body.String())
	}
	var code api.CliAuthCodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &code); err != nil {
		t.Fatal(err)
	}
	// deploy:write (1) + secrets:request (2) + postgres:manage (8) = B.
	if !strings.HasPrefix(code.Code, "MCPB-") || len(code.Code) != 69 {
		t.Fatalf("code=%q", code.Code)
	}
	// No other scope digit, nor the legacy write purpose, finds the code.
	for _, digit := range []string{"F", "D", "W", "R"} {
		changed, ok := normalizeCliAuthCode("MCP" + digit + code.Code[4:])
		if !ok {
			t.Fatalf("digit %s not an MCP code", digit)
		}
		if _, _, err := store.PeekCliAuthCode(t.Context(), changed); err == nil {
			t.Fatalf("digit %s reused the issued code", digit)
		}
	}
	cookie, csrf := renderCliAuthForTestAs(t, srv, code.Code, "scoped-mcp@example.com")
	if claim := postCliAuthForTest(t, srv, code.Code, "scoped-mcp@example.com", cookie, csrf); claim.Code != http.StatusFound {
		t.Fatalf("claim=%d", claim.Code)
	}
	exchangeBody, _ := json.Marshal(api.CliAuthExchangeRequest{Code: code.Code})
	result := httptest.NewRecorder()
	srv.ServeHTTP(result, httptest.NewRequest("POST", "/v1/cli-auth/exchange", bytes.NewReader(exchangeBody)))
	if result.Code != 200 {
		t.Fatalf("exchange=%d", result.Code)
	}
	var exchange api.CliAuthExchangeResponse
	if err := json.Unmarshal(result.Body.Bytes(), &exchange); err != nil {
		t.Fatal(err)
	}
	key, err := store.APIKeyByHash(t.Context(), api.HashToken([]byte(exchange.Plaintext)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{api.ScopeAppsRead, api.ScopeDeployWrite, api.ScopeSecretsRequest, api.ScopeManagedPostgresManage, api.ScopeManagedPostgresRead}
	got := slices.Clone(key.Scopes)
	slices.Sort(got)
	slices.Sort(want)
	if key.Label != "mcp-login" || !slices.Equal(got, want) {
		t.Fatalf("scopes=%v label=%s", key.Scopes, key.Label)
	}
}

func TestMCPScopedBrowserAuthDescribesItsCapabilities(t *testing.T) {
	raw := "MCPB-" + strings.Repeat("ab", 32)
	got := strings.Join(mcpCapabilities(raw), "|")
	for _, want := range []string{"deploy, promote", "enter secrets", "PostgreSQL"} {
		if !strings.Contains(got, want) {
			t.Fatalf("capabilities %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "object storage") {
		t.Fatalf("capabilities %q name an ungranted scope", got)
	}
	if len(mcpCapabilities("MCPR-"+strings.Repeat("ab", 32))) != 0 {
		t.Fatal("a read code describes extra capabilities")
	}
}

func TestMCPScopedBrowserAuthRejectsUngrantableScopes(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)
	for _, body := range []string{
		`{"purpose":"mcp","scopes":["admin"]}`,
		`{"purpose":"mcp","scopes":["secrets:write"]}`,
		`{"purpose":"mcp","scopes":["deploy:write","deploy:write"]}`,
		`{"purpose":"mcp-write","scopes":["secrets:request"]}`,
		`{"scopes":["deploy:write"]}`,
	} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/cli-auth/code", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("%s minted: %d", body, rec.Code)
		}
	}
}

func TestSecretRequestsAcceptTheNarrowScope(t *testing.T) {
	t.Cleanup(withTestRecipient(t))
	srv, store := newCliAuthTestServer(t)
	acct, err := store.CreateAccount(t.Context(), "narrow@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "billing-api", Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		scopes []string
		want   int
	}{
		{[]string{api.ScopeAppsRead, api.ScopeSecretsRequest}, 201},
		{[]string{api.ScopeAppsRead, api.ScopeSecretsWrite}, 201},
		{[]string{api.ScopeAppsRead, api.ScopeDeployWrite}, 403},
	} {
		key, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "bridge", tc.scopes); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(api.SecretEntryRequest{Key: "STRIPE_KEY", Reason: "payments"})
		r := httptest.NewRequest("POST", "/v1/apps/billing-api/secret-requests", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("scopes %v: %d %s", tc.scopes, w.Code, w.Body.String())
		}
	}
	// secrets:request can never set a value directly.
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "bridge", []string{api.ScopeAppsRead, api.ScopeSecretsRequest}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PUT", "/v1/apps/billing-api/secrets/STRIPE_KEY", strings.NewReader(`{"value":"sk_live_x"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("secrets:request set a secret: %d", w.Code)
	}
}
