package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/pquerna/otp/totp"
)

func TestDashboardMFASettingsEnrollmentAndSuspendedRecovery(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, true, false)
	h := *e.H()
	do := func(method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if method == "POST" && path == "/v1/account/mfa/confirm" {
			req.Header.Set("Idempotency-Key", "same-mfa-proof")
		}
		req.AddCookie(*e.Cookie())
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	page := do("GET", "/dashboard/account/mfa?action=enroll", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `id="start"`) {
		t.Fatalf("pending session cannot enroll in the browser: %d %s", page.Code, page.Body.String())
	}
	if other := do("GET", "/dashboard/apps", nil); other.Code != 302 {
		t.Fatal("pending session escaped the MFA pages")
	}
	enrollment := do("POST", "/v1/account/mfa/enroll", map[string]string{})
	var material api.MFAEnrollResponse
	if enrollment.Code != 200 || json.Unmarshal(enrollment.Body.Bytes(), &material) != nil || len(material.RecoveryCodes) < 2 {
		t.Fatalf("browser enrollment = %d", enrollment.Code)
	}
	page = do("GET", "/dashboard/account/mfa?action=confirm", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `<div id="setup" >`) || strings.Contains(page.Body.String(), material.Secret) {
		t.Fatal("returning to confirmation must show its form without exposing saved setup material")
	}
	proof := func(action string) (api.CSRFTokenResponse, []*http.Cookie) {
		t.Helper()
		rec := do("GET", "/v1/auth/csrf?action=mfa_"+action, nil)
		var token api.CSRFTokenResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &token) != nil || token.CSRFToken == "" {
			t.Fatalf("get %s CSRF token = %d", action, rec.Code)
		}
		return token, rec.Result().Cookies()
	}
	code, err := totp.GenerateCode(material.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	csrf, cookies := proof("confirm")
	confirmed := do("POST", "/v1/account/mfa/confirm", map[string]string{"totp": code, "csrf_token": csrf.CSRFToken}, cookies...)
	if confirmed.Code != 200 {
		t.Fatalf("confirm = %d %s", confirmed.Code, confirmed.Body.String())
	}
	// A confirm request carrying a request key still has to check its proof.
	replay := httptest.NewRequest("POST", "/v1/account/mfa/confirm", strings.NewReader(`{"totp":"`+code+`","csrf_token":"`+csrf.CSRFToken+`"}`))
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set("Idempotency-Key", "same-mfa-proof")
	replay.AddCookie(*e.Cookie())
	for _, cookie := range cookies {
		replay.AddCookie(cookie)
	}
	replayed := httptest.NewRecorder()
	h.ServeHTTP(replayed, replay)
	if replayed.Code != 409 || replayed.Header().Get("Set-Cookie") != "" {
		t.Fatalf("enrollment replay cleared a session: %d", replayed.Code)
	}
	if used := do("POST", "/v1/account/mfa/verify", map[string]string{"totp": code}); used.Code != 401 {
		t.Fatalf("enrollment code verified another session: %d", used.Code)
	}
	if err := (*e.Store()).UpdateAccountStatus(t.Context(), (*e.Acct()).ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	page = do("GET", "/dashboard/account/mfa?action=recover", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-action="recover"`) {
		t.Fatalf("suspended session cannot open browser recovery: %d", page.Code)
	}
	csrf, cookies = proof("recover")
	recovered := do("POST", "/v1/account/mfa/recover", map[string]string{"code": material.RecoveryCodes[0], "csrf_token": csrf.CSRFToken}, cookies...)
	if recovered.Code != 200 || recovered.Header().Get("Set-Cookie") == "" {
		t.Fatalf("suspended session cannot recover with its code: %d %s", recovered.Code, recovered.Body.String())
	}
	if later := do("GET", "/v1/apps", nil); later.Code < 400 {
		t.Fatal("recovery cleared the billing suspension")
	}
}

func TestDashboardMFASettingsRequiresLiveSession(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, httptest.NewRequest("GET", "/dashboard/account/mfa?action=recover", nil))
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Fatalf("anonymous MFA settings = %d", rec.Code)
	}
	env, err := (*e.Store()).ListSessions(t.Context(), (*e.Acct()).ID)
	if err != nil || len(env) != 1 {
		t.Fatal("missing fixture session", err)
	}
	if _, err := (*e.Store()).RevokeSession(t.Context(), env[0].ID, (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/dashboard/account/mfa", nil)
	req.AddCookie(*e.Cookie())
	rec = httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("revoked session opened MFA settings: %d", rec.Code)
	}
}
