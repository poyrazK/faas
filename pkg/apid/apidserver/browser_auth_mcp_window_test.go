package apidserver_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/pquerna/otp/totp"
)

// adr: 20261117173000. Only purpose-bound MCP approval codes cover the
// longer MFA/review window; terminal pairing keeps its five-minute limit.
func TestMCPBrowserApprovalCodeWindow(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		window     time.Duration
	}{
		{"legacy CLI", `{}`, 5 * time.Minute},
		{"device CLI", `{"device":true}`, 5 * time.Minute},
		{"read-only MCP", `{"purpose":"mcp-read"}`, time.Hour},
		{"write MCP", `{"purpose":"mcp-write"}`, time.Hour},
		{"scoped MCP", `{"purpose":"mcp","scopes":["secrets:request"]}`, time.Hour},
		{"extended MCP", `{"purpose":"mcp","scopes":["usage:read"]}`, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, store := apidserver.NewCLIAuthTestServerForTest(t)
			out := httptest.NewRecorder()
			srv.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", strings.NewReader(tc.body)))
			var response api.CliAuthCodeResponse
			if out.Code != http.StatusOK || json.Unmarshal(out.Body.Bytes(), &response) != nil {
				t.Fatalf("mint code = %d", out.Code)
			}
			expires, err := time.Parse(time.RFC3339, response.ExpiresAt)
			remaining := time.Until(expires)
			if err != nil || remaining > tc.window || remaining < tc.window-5*time.Second {
				t.Fatalf("approval code expires in %v, want %v (parse error %v)", remaining, tc.window, err)
			}
			hash, ok := apidserver.NormalizeCliAuthCodeForTest(response.Code)
			if !ok {
				t.Fatal("minted code cannot be normalized")
			}
			status, _, err := store.PeekCliAuthCode(t.Context(), hash)
			if err != nil || status != api.CliAuthStatusPending {
				t.Fatal("minted approval code was not stored pending")
			}
		})
	}
}

// A long MFA challenge must still accept a current TOTP and return to the
// original MCP approval. The sealed token's age is advanced without sleeping.
func TestMCPBrowserMFAAndApprovalFormsShareTheSetupWindow(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	_, secret, _ := e.GenerateEnrolledAccount(t)
	if err := (*e.Store()).MarkAccountEmailVerified(t.Context(), e.Acct().ID); err != nil {
		t.Fatal(err)
	}
	pending := e.MfaIssueWithPending(t, true)
	handler := *e.H()
	mgr := *(*e.Srv()).Sessions()
	mint := httptest.NewRecorder()
	handler.ServeHTTP(mint, httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", strings.NewReader(`{"purpose":"mcp-read"}`)))
	var code api.CliAuthCodeResponse
	if mint.Code != http.StatusOK || json.Unmarshal(mint.Body.Bytes(), &code) != nil {
		t.Fatal("could not mint MCP approval code")
	}
	next := "/cli-auth?code=" + url.QueryEscape(code.Code) + "&return=mcp"
	var challenge *http.Cookie
	for _, tc := range []struct {
		name, next string
		seconds    int
	}{
		{"ordinary dashboard", "/dashboard/", 600},
		{"unissued MCP link", "/cli-auth?code=MCPR-" + strings.Repeat("ab", 32), 600},
		{"pending MCP", next, 3600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/dashboard/mfa?next="+url.QueryEscape(tc.next), nil)
			request.AddCookie(pending)
			page := httptest.NewRecorder()
			handler.ServeHTTP(page, request)
			if page.Code != http.StatusOK {
				t.Fatalf("MFA challenge = %d", page.Code)
			}
			for _, c := range page.Result().Cookies() {
				if c.Name == "faas_csrf_mfa" {
					if c.MaxAge != tc.seconds || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
						t.Fatal("MFA CSRF cookie has the wrong setup lifetime or protections")
					}
					if tc.name == "pending MCP" {
						challenge = c
					}
				}
			}
		})
	}
	if challenge == nil {
		t.Fatal("MFA challenge did not set its CSRF cookie")
	}
	raw, err := base64.RawURLEncoding.DecodeString(challenge.Value)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := mgr.OpenForCSRF(raw)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Action, Subject string
		Expires         time.Time
	}
	if err := json.Unmarshal(plaintext, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Expires = envelope.Expires.Add(-20 * time.Minute)
	plaintext, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	challenge.Value, err = mgr.SealForCSRF(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := totp.GenerateCodeCustom(secret, time.Now().UTC(), totp.ValidateOpts{Period: 30, Digits: 6})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf_token": {challenge.Value}, "code": {proof}, "next": {next}}
	verify := httptest.NewRequest(http.MethodPost, "/dashboard/mfa", strings.NewReader(form.Encode()))
	verify.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	verify.AddCookie(pending)
	verify.AddCookie(challenge)
	verified := httptest.NewRecorder()
	handler.ServeHTTP(verified, verify)
	if verified.Code != http.StatusSeeOther || verified.Header().Get("Location") != next {
		t.Fatalf("MFA after 20 minutes = %d %q", verified.Code, verified.Header().Get("Location"))
	}
	approval := httptest.NewRequest(http.MethodGet, next, nil)
	for _, cookie := range verified.Result().Cookies() {
		approval.AddCookie(cookie)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, approval)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Authorize and continue") {
		t.Fatalf("native approval after MFA = %d", page.Code)
	}
	found := false
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name != "faas_csrf" {
			continue
		}
		found = true
		raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
		if err != nil {
			t.Fatal(err)
		}
		plaintext, err := mgr.OpenForCSRF(raw)
		if err != nil || json.Unmarshal(plaintext, &envelope) != nil || time.Until(envelope.Expires) < 59*time.Minute || cookie.MaxAge != 3600 {
			t.Fatal("native MCP approval retained a short CSRF lifetime")
		}
	}
	if !found {
		t.Fatal("native approval has no CSRF cookie")
	}
}
