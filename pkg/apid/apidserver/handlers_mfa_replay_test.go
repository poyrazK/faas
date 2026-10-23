package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A TOTP code that stepped up one session must not step up another within
// its validity window (RFC 6238 §5.2); a code from a later step still does.
func TestMFAVerify_RefusesAReplayedCode(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	_, secret, _ := e.GenerateEnrolledAccount(t)
	codeAt := func(at time.Time) string {
		code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			t.Fatal(err)
		}
		return code
	}
	verify := func(code string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(api.MFAVerifyRequest{Totp: code})
		req := httptest.NewRequest(http.MethodPost, "/v1/account/mfa/verify", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(e.MfaIssueWithPending(t, true))
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	now := time.Now().UTC()
	code := codeAt(now)
	if rec := verify(code); rec.Code != http.StatusOK {
		t.Fatalf("first use: status %d: %s", rec.Code, rec.Body)
	}
	apidserver.AssertProblem(t, verify(code), http.StatusUnauthorized, api.CodeMFAInvalidCode)
	if next := codeAt(now.Add(30 * time.Second)); next != code {
		if rec := verify(next); rec.Code != http.StatusOK {
			t.Fatalf("the next step's code: status %d: %s", rec.Code, rec.Body)
		}
	}
}
