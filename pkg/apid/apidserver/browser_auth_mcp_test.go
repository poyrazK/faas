package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPBrowserAuthLimitedCredentialAndPurposeBinding(t *testing.T) {
	for _, writes := range []bool{false, true} {
		t.Run(map[bool]string{false: "read", true: "write"}[writes], func(t *testing.T) {
			srv, store := newCliAuthTestServer(t)
			purpose, prefix := api.CliAuthPurposeMCPRead, "MCPR"
			if writes {
				purpose, prefix = api.CliAuthPurposeMCPWrite, "MCPW"
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/cli-auth/code", strings.NewReader(`{"purpose":"`+purpose+`"}`)))
			if rec.Code != 200 {
				t.Fatalf("mint=%d", rec.Code)
			}
			var code api.CliAuthCodeResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &code); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(code.Code, prefix+"-") || len(code.Code) != 69 {
				t.Fatal("missing purpose/entropy")
			}
			// A claimed read code cannot be changed into write or CLI login.
			other := "MCPW"
			if writes {
				other = "MCPR"
			}
			changed, ok := normalizeCliAuthCode(other + code.Code[4:])
			if !ok {
				t.Fatal("invalid alternate purpose")
			}
			if _, _, err := store.PeekCliAuthCode(t.Context(), changed); err == nil {
				t.Fatal("purpose change reused issued code")
			}
			cookie, csrf := renderCliAuthForTestAs(t, srv, code.Code, "native-mcp@example.com")
			claim := postCliAuthForTest(t, srv, code.Code, "attacker@example.com", cookie, csrf)
			if claim.Code != http.StatusFound {
				t.Fatalf("claim=%d", claim.Code)
			}
			body, _ := json.Marshal(api.CliAuthExchangeRequest{Code: code.Code})
			result := httptest.NewRecorder()
			srv.ServeHTTP(result, httptest.NewRequest("POST", "/v1/cli-auth/exchange", bytes.NewReader(body)))
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
			if key.Label != "mcp-login" || !slices.Contains(key.Scopes, api.ScopeAppsRead) || slices.Contains(key.Scopes, api.ScopeAdmin) || slices.Contains(key.Scopes, api.ScopeDeployWrite) != writes {
				t.Fatalf("unexpected credential scope=%v", key.Scopes)
			}
			if exchange.Account.Email != "native-mcp@example.com" {
				t.Fatal("credential followed posted identity")
			}
			expiry, err := time.Parse(time.RFC3339, exchange.ExpiresAt)
			if err != nil || time.Until(expiry) > 8*time.Hour || time.Until(expiry) < 8*time.Hour-time.Minute {
				t.Fatal("unexpected credential expiry")
			}
			replay := httptest.NewRecorder()
			srv.ServeHTTP(replay, httptest.NewRequest("POST", "/v1/cli-auth/exchange", bytes.NewReader(body)))
			if replay.Code != 410 {
				t.Fatal("code minted a second credential")
			}
		})
	}
}

func TestMCPBrowserAuthRejectsUnrecognizedPurpose(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)
	for _, body := range []string{`{"purpose":"admin"}`, `{"purpose":"mcp-read"`, `{"purpose":"mcp-read","scopes":["admin"]}`} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/cli-auth/code", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("invalid mint=%d", rec.Code)
		}
	}
}
