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
			// A week; it slides while used (ADR-20261028205920).
			if err != nil || time.Until(expiry) > api.MCPLoginKeyIdle || time.Until(expiry) < api.MCPLoginKeyIdle-time.Minute {
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

// The hosted MCP server asks for the browser back (ADR-20261028205920): an
// approved MCP code carrying return=mcp continues on the platform's own MCP
// server, never on a target the request names; a CLI code never does.
func TestMCPApprovalReturnsToTheHostedServer(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)
	approve := func(purpose, email string, ret bool) (*httptest.ResponseRecorder, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		body := `{}`
		if purpose != "" {
			body = `{"purpose":"` + purpose + `"}`
		}
		srv.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/cli-auth/code", strings.NewReader(body)))
		var code api.CliAuthCodeResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &code) != nil {
			t.Fatalf("mint=%d", rec.Code)
		}
		suffix := ""
		if ret {
			suffix = "&return=mcp"
		}
		cookie, csrf := renderCliAuthForTestAs(t, srv, code.Code+suffix, email)
		page := httptest.NewRecorder()
		get := httptest.NewRequest(http.MethodGet, "/cli-auth?code="+code.Code+suffix, nil)
		get.AddCookie(&http.Cookie{Name: sessionCookie, Value: srv.sessionCookies[cookie]})
		srv.ServeHTTP(page, get)
		form := "code=" + strings.ReplaceAll(code.Code, "-", "") + "&csrf_token=" + csrf
		if ret {
			form += "&return=mcp"
		}
		post := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(form))
		post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		post.AddCookie(&http.Cookie{Name: sessionCookie, Value: srv.sessionCookies[cookie]})
		post.AddCookie(&http.Cookie{Name: "faas_csrf", Value: cookie})
		out := httptest.NewRecorder()
		srv.ServeHTTP(out, post)
		return out, page.Body.String()
	}
	hosted, page := approve(api.CliAuthPurposeMCPRead, "hosted-mcp@example.com", true)
	if !strings.Contains(page, `name="return" value="mcp"`) {
		t.Fatal("approval page dropped the hosted return")
	}
	if hosted.Code != http.StatusSeeOther || hosted.Header().Get("Location") != "https://api.gregale.dev/mcp/oauth/continue" {
		t.Fatalf("hosted approval = %d %q", hosted.Code, hosted.Header().Get("Location"))
	}
	twoTab, _ := approve(api.CliAuthPurposeMCPRead, "self-hosted-mcp@example.com", false)
	if twoTab.Code != http.StatusFound || twoTab.Header().Get("Location") != cliAuthDashboard {
		t.Fatalf("approval without return = %d %q", twoTab.Code, twoTab.Header().Get("Location"))
	}
	cli, _ := approve("", "cli@example.com", true)
	if cli.Header().Get("Location") != cliAuthDashboard {
		t.Fatalf("a CLI code followed return=mcp to %q", cli.Header().Get("Location"))
	}
}
