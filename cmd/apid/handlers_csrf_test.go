package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
)

func TestIssueCSRFToken(t *testing.T) {
	t.Run("issues token for an authenticated session", func(t *testing.T) {
		env := newSessionEnv(t)
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/csrf?action=mfa_confirm", nil)
		req.AddCookie(env.cookie)
		rec := httptest.NewRecorder()
		env.h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var payload api.CSRFTokenResponse
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if payload.CSRFToken == "" {
			t.Fatal("response did not contain csrf_token")
		}

		var csrfCookie *http.Cookie
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == middleware.CookieNameAuthenticated {
				csrfCookie = cookie
				break
			}
		}
		if csrfCookie == nil {
			t.Fatal("response did not set faas_csrf cookie")
		}
		if csrfCookie.Value != payload.CSRFToken || !csrfCookie.HttpOnly {
			t.Fatalf("csrf cookie = %#v, want HttpOnly cookie matching response token", csrfCookie)
		}

		verifyReq := httptest.NewRequest(http.MethodPost, "/v1/account/mfa/confirm",
			bytes.NewBufferString(`{"totp":"123456","csrf_token":"`+payload.CSRFToken+`"}`))
		verifyReq.Header.Set("Content-Type", "application/json")
		verifyReq.AddCookie(csrfCookie)
		if err := middleware.VerifyAuthenticated(env.mgr, verifyReq, "mfa_confirm", env.acct.ID); err != nil {
			t.Fatalf("issued token did not verify: %v", err)
		}
	})

	t.Run("is reachable while the session is mfa pending", func(t *testing.T) {
		env := setupWithMFA(t, api.PlanPro, true, false)
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/csrf?action=mfa_recover", nil)
		req.AddCookie(env.cookie)
		rec := httptest.NewRecorder()
		env.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
	})

	t.Run("rejects unknown actions", func(t *testing.T) {
		env := newSessionEnv(t)
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/csrf?action=arbitrary", nil)
		req.AddCookie(env.cookie)
		rec := httptest.NewRecorder()
		env.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		if !problemHasCode(t, rec.Body.Bytes(), api.CodeValidation) {
			t.Fatalf("problem = %s, want code %q", rec.Body.String(), api.CodeValidation)
		}
	})
}

func TestIssueCSRFToken_ConnectBinding(t *testing.T) {
	env := newSessionEnv(t)
	token, cookie := browserConnectCSRF(t, env.h, env.cookie)
	for _, tc := range []struct {
		name      string
		action    string
		accountID string
		valid     bool
	}{
		{name: "connect action and account", action: githubConnectAction, accountID: env.acct.ID, valid: true},
		{name: "wrong action", action: "mfa_confirm", accountID: env.acct.ID},
		{name: "wrong account", action: githubConnectAction, accountID: "another-account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/dashboard/install/connect",
				strings.NewReader(url.Values{"csrf_token": {token}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(cookie)
			err := middleware.VerifyAuthenticatedNamed(env.mgr, req, tc.action, tc.accountID, githubConnectCSRFCookie)
			if (err == nil) != tc.valid {
				t.Fatalf("valid = %t, verification error = %v", tc.valid, err)
			}
		})
	}
}
