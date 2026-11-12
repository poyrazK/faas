package apidserver_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An unverified account cannot create the credentials that outlive an
// ownership claim: API keys, organizations and invitations
// (ADR 20261109120000).
func TestUnverifiedAccountCannotMintLastingCredentials(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, unverified := flowAccountKey(t, store, "unverified-mint@example.com", false)
	_, verified := flowAccountKey(t, store, "verified-mint@example.com", true)
	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/v1/keys", `{"label":"ci"}`},
		{http.MethodPost, "/v1/orgs", `{"name":"Squat Co","slug":"squat-co"}`},
	}
	for _, tc := range cases {
		rec := flowBearer(h, tc.method, tc.path, unverified, tc.body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "email_verification_required") {
			t.Errorf("unverified %s %s = %d %s, want 403 email_verification_required", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if rec := flowBearer(h, tc.method, tc.path, verified, tc.body); strings.Contains(rec.Body.String(), "email_verification_required") {
			t.Errorf("verified %s %s refused for verification: %s", tc.method, tc.path, rec.Body.String())
		}
	}
}

// The rotation grace window is capped so a stolen session cannot keep a
// rotated key alive for years.
func TestGraceWindowCapped(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, key := flowAccountKey(t, store, "grace-cap@example.com", true)
	for _, tc := range []struct {
		days int
		want int
	}{
		{api.APIKeyGraceWindowMaxDays, http.StatusOK},
		{api.APIKeyGraceWindowMaxDays + 1, http.StatusBadRequest},
		{3650, http.StatusBadRequest},
	} {
		body := `{"days":` + strconv.Itoa(tc.days) + `}`
		rec := flowBearer(h, http.MethodPatch, "/v1/account/keys/grace_window_days", key, body)
		if rec.Code != tc.want {
			t.Errorf("grace %d days = %d %s, want %d", tc.days, rec.Code, rec.Body.String(), tc.want)
		}
		if tc.want == http.StatusBadRequest && !strings.Contains(rec.Body.String(), "/api-keys") {
			t.Errorf("grace refusal lacks a docs URL: %s", rec.Body.String())
		}
	}
}

// Signing out everywhere also ends an email change nobody confirmed yet: the
// link may sit in the mailbox of whoever started it (ADR 20261109120000).
func TestSignOutEverywhereEndsPendingEmailChange(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	acct, key := flowAccountKey(t, store, "signout-all@example.com", true)
	flow := store.(state.AccountFlowStore)
	hash := api.HashToken([]byte("pending-email-change-token-0123456"))
	if err := flow.IssueEmailChange(t.Context(), hash, acct.ID, "attacker@example.net", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if rec := flowBearer(h, http.MethodPost, "/v1/auth/sessions/revoke_all", key, ""); rec.Code >= 300 {
		t.Fatalf("revoke_all = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := flow.ConsumeEmailChange(t.Context(), hash); err == nil {
		t.Fatal("the email change survived signing out everywhere")
	}
}
