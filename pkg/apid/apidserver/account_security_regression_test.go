package apidserver_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/pquerna/otp/totp"
)

func TestAccountAuditResetEmailRendersForm(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	acct, err := store.CreateAccount(t.Context(), "reset-probe@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	raw := bytes.Repeat([]byte{1}, 32)
	if err := store.IssueLoginToken(t.Context(), api.HashToken(raw), acct.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/reset?token="+base64.RawURLEncoding.EncodeToString(raw), nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET reset = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `name="password"`) || !strings.Contains(rec.Body.String(), `name="token"`) {
		t.Fatalf("valid reset email link returns 200 but no password reset form; contains fallback = %v", strings.Contains(rec.Body.String(), "Open the reset link from your email"))
	}
}

func TestAccountAuditSignupCountsPasswordFailures(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	acct, err := store.CreateAccount(context.Background(), "signup-limit-probe@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	const password = "correct-horse-battery-staple"
	phc, err := auth.Encode(password)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetAccountPassword(t.Context(), acct.ID, phc); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	post := func(path, pass string, client int) int {
		body, _ := json.Marshal(api.PasswordLoginRequest{Email: acct.Email, Password: pass})
		req := httptest.NewRequest("POST", path, bytes.NewReader(body))
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", client)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range api.LoginFailuresPerEmail {
		if code := post("/signup", "wrong-password-long-enough", i+1); code != 401 {
			t.Fatalf("guess %d = %d", i+1, code)
		}
	}
	if code := post("/login", password, 200); code != 429 {
		t.Fatalf("login after %d wrong /signup passwords from distinct IPs = %d, want per-email 429", api.LoginFailuresPerEmail, code)
	}
}

func TestAccountAuditMFAConfirmRejectsSpentCode(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	_, secret, cookie := e.GenerateEnrolledAccount(t)
	*e.Cookie() = cookie
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h := *e.H()
	post := func(path string, body map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/v1/account/mfa/verify", map[string]string{"totp": code}); rec.Code != 200 {
		t.Fatalf("first verify = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := post("/v1/account/mfa/verify", map[string]string{"totp": code}); rec.Code != 401 {
		t.Fatalf("verify replay = %d: %s", rec.Code, rec.Body.String())
	}
	cookie = e.MfaIssueWithPending(t, true)
	csrfReq := httptest.NewRequest("GET", "/v1/auth/csrf?action=mfa_confirm", nil)
	csrfReq.AddCookie(cookie)
	csrfRec := httptest.NewRecorder()
	h.ServeHTTP(csrfRec, csrfReq)
	if csrfRec.Code != 200 {
		t.Fatalf("csrf = %d: %s", csrfRec.Code, csrfRec.Body.String())
	}
	var csrf api.CSRFTokenResponse
	if err := json.Unmarshal(csrfRec.Body.Bytes(), &csrf); err != nil {
		t.Fatal(err)
	}
	rec := post("/v1/account/mfa/confirm", map[string]string{"totp": code, "csrf_token": csrf.CSRFToken}, csrfRec.Result().Cookies()...)
	if rec.Code < 400 {
		t.Fatalf("code rejected as replay by /verify is accepted by /confirm for enrolled account and pending session: status %d, sets cookie = %v", rec.Code, len(rec.Result().Cookies()) > 0)
	}
}
