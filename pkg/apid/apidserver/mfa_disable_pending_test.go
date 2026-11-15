package apidserver_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A mailbox alone used to turn off two-factor after 24 hours; the owner could
// not stop it. Proving the second factor now cancels a pending email-assisted
// disable, and only a session that passed the second factor can cancel it by
// hand (F14-1, ADR-20261115100000).
func TestPendingEmailDisableIsCancelledBySecondFactor(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	_, secret, cookie := e.GenerateEnrolledAccount(t)
	store, acct, h := *e.Store(), *e.Acct(), *e.H()
	request := func() {
		t.Helper()
		token := make([]byte, 32)
		_, _ = rand.Read(token)
		if err := store.IssueMFADisableRequest(t.Context(), api.HashToken(token), acct.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() bool {
		t.Helper()
		at, err := store.PendingMFADisableRequestedAt(t.Context(), acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		return at != nil
	}
	do := func(c *http.Cookie, method, path string, body any, extra ...*http.Cookie) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(c)
		for _, x := range extra {
			req.AddCookie(x)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	cancel := func(c *http.Cookie) *httptest.ResponseRecorder {
		rec := do(c, http.MethodGet, "/v1/auth/csrf?action=mfa_disable_email_cancel", nil)
		var token struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &token)
		return do(c, http.MethodPost, "/v1/account/mfa/disable-email-cancel", api.MFADisableEmailCancelRequest{CSRFToken: token.CSRFToken}, rec.Result().Cookies()...)
	}

	request()
	if !pending() {
		t.Fatal("no pending disable after the request")
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	verified := do(cookie, http.MethodPost, "/v1/account/mfa/verify", map[string]string{"totp": code})
	if verified.Code != http.StatusOK {
		t.Fatalf("verify = %d %s", verified.Code, verified.Body)
	}
	// The verify re-issues the session cookie; later requests use the new one.
	for _, c := range verified.Result().Cookies() {
		if c.Name == cookie.Name && c.Value != "" {
			cookie = c
		}
	}
	if pending() {
		t.Fatal("a TOTP sign-in left the pending disable in place")
	}

	request()
	if rec := cancel(e.MfaIssueWithPending(t, true)); rec.Code == http.StatusOK {
		t.Fatalf("a session from a password reset alone cancelled the disable: %s", rec.Body)
	}
	if !pending() {
		t.Fatal("a session without the second factor ended the pending disable")
	}
	rec := cancel(cookie)
	var out api.MFADisableEmailCancelResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Cancelled != 1 {
		t.Fatalf("cancel from a verified session = %d %s, want 200 with cancelled=1", rec.Code, rec.Body)
	}
	if pending() {
		t.Fatal("cancel left the pending disable in place")
	}
}
