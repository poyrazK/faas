package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A browser must finish the native approval POST before navigating from the
// console to the API origin. Following a 303 here violates form-action 'self'.
func TestMCPNativeApprovalBrowserHandoff(t *testing.T) {
	for _, accept := range []string{"text/html,application/xhtml+xml", "*/*", "text/html;q=0"} {
		t.Run(accept, func(t *testing.T) {
			srv, store := apidserver.NewCLIAuthTestServerForTest(t)
			mint := httptest.NewRecorder()
			srv.ServeHTTP(mint, httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", strings.NewReader(`{"purpose":"mcp-read"}`)))
			var code api.CliAuthCodeResponse
			if mint.Code != http.StatusOK || json.Unmarshal(mint.Body.Bytes(), &code) != nil {
				t.Fatalf("mint = %d", mint.Code)
			}
			cookie, csrf := apidserver.RenderCLIAuthForTestAs(t, srv, code.Code+"&return=mcp", "native-handoff@example.test")
			approve := func(csrfValue string) *httptest.ResponseRecorder {
				t.Helper()
				form := url.Values{"code": {code.Code}, "csrf_token": {csrfValue}, "return": {"mcp"}, "continue": {"https://attacker.example/"}}
				request := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(form.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				request.Header.Set("Accept", accept)
				request.AddCookie(srv.SessionCookieForTest(cookie))
				request.AddCookie(&http.Cookie{Name: "faas_csrf", Value: cookie})
				out := httptest.NewRecorder()
				srv.ServeHTTP(out, request)
				return out
			}
			// A handoff must never be rendered before authenticated CSRF succeeds.
			rejected := approve("invalid")
			hash, _ := apidserver.NormalizeCliAuthCodeForTest(code.Code)
			status, _, err := store.PeekCliAuthCode(t.Context(), hash)
			if err != nil || status != api.CliAuthStatusPending || strings.Contains(rejected.Body.String(), "http-equiv=\"refresh\"") {
				t.Fatal("invalid CSRF approved or continued the connection")
			}
			out := approve(csrf)
			const target = "https://api.gregale.dev/mcp/oauth/continue"
			if accept != "text/html,application/xhtml+xml" {
				if out.Code != http.StatusSeeOther || out.Header().Get("Location") != target {
					t.Fatalf("non-browser approval = %d %q", out.Code, out.Header().Get("Location"))
				}
				return
			}
			if out.Code != http.StatusOK || out.Header().Get("Location") != "" {
				t.Fatalf("browser approval = %d %q", out.Code, out.Header().Get("Location"))
			}
			body := out.Body.String()
			for _, want := range []string{`http-equiv="refresh" content="0;url=` + target + `"`, `href="` + target + `"`, `name="referrer" content="no-referrer"`} {
				if !strings.Contains(body, want) {
					t.Errorf("handoff lacks %q", want)
				}
			}
			if strings.Contains(body, "attacker.example") || strings.Contains(body, "<script") || strings.Contains(body, "<form") {
				t.Fatal("handoff includes untrusted navigation, script or another approval form")
			}
			if out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(strings.Join(out.Header().Values("Vary"), ","), "Accept") {
				t.Fatal("handoff must remain private and vary on Accept")
			}
			csp := out.Header().Get("Content-Security-Policy")
			if !strings.Contains(csp, "form-action 'self'") || strings.Contains(csp, "unsafe-inline") {
				t.Fatalf("handoff weakened CSP: %q", csp)
			}
			// A second submission must explain recovery without claiming that an
			// earlier successful approval never happened, or suggesting CLI login.
			replay := approve(csrf).Body.String()
			if !strings.Contains(replay, "Start the connection again from your agent") || strings.Contains(replay, "gregale login") || strings.Contains(replay, "Nothing was authorized") {
				t.Fatal("MCP replay does not explain agent-specific recovery")
			}
		})
	}
}
