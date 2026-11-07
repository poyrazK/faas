package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

var flowMagicLinkToken = regexp.MustCompile(`/auth/verify\?token=([A-Za-z0-9_-]+)`)

func flowLastToken(t *testing.T, mailer *apidserver.RecordingMailer, pattern *regexp.Regexp) string {
	t.Helper()
	msgs := mailer.Snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		if m := pattern.FindStringSubmatch(msgs[i].TextBody); m != nil {
			return m[1]
		}
	}
	t.Fatalf("no mail matched %s", pattern)
	return ""
}

func flowSignup(t *testing.T, h http.Handler, email, password string) string {
	t.Helper()
	rec := apidserver.V1AuthJSONRequest(t, h, "/v1/auth/signup", `{"email":"`+email+`","password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("signup = %d: %s", rec.Code, rec.Body.String())
	}
	var resp api.ProgrammaticAuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.APIKey.Plaintext == "" {
		t.Fatalf("signup response %s: %v", rec.Body.String(), err)
	}
	return resp.APIKey.Plaintext
}

func flowBearer(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func flowForm(h http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// An owner who never verified and forgot the password recovers through
// forgot-password: the magic link it sends claims the account and drops
// whatever the first password signup left behind.
func TestForgotPasswordOnUnverifiedAccountClaimsIt(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	squatterKey := flowSignup(t, h, "claim-me@example.com", "squatter-password-123!")

	if rec := flowForm(h, "/login/forgot", url.Values{"email": {"claim-me@example.com"}}); rec.Code != http.StatusOK {
		t.Fatalf("forgot = %d", rec.Code)
	}
	srv.WaitPasswordResetMail()
	token := flowLastToken(t, mailer, flowMagicLinkToken)
	rec := flowForm(h, "/auth/verify", url.Values{"token": {token}})
	if rec.Code != http.StatusFound || rec.Header().Get("Set-Cookie") == "" {
		t.Fatalf("redeem magic link = %d: %s", rec.Code, rec.Body.String())
	}
	acct, err := store.AccountByEmail(context.Background(), "claim-me@example.com")
	if err != nil || !acct.EmailVerified() {
		t.Fatalf("account after claim verified=%v err=%v", acct.EmailVerified(), err)
	}
	if code := flowBearer(h, http.MethodGet, "/v1/account", squatterKey, "").Code; code != http.StatusUnauthorized {
		t.Errorf("pre-claim signup key = %d, want 401", code)
	}
	if rec := apidserver.V1AuthJSONRequest(t, h, "/v1/auth/login",
		`{"email":"claim-me@example.com","password":"squatter-password-123!"}`); rec.Code == http.StatusOK {
		t.Error("pre-claim password still signs in")
	}
}

// flowAccountKey creates an account (verified or not) and a full-scope key.
func flowAccountKey(t *testing.T, store state.Store, email string, verified bool) (state.Account, string) {
	t.Helper()
	res, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{
		Email: email, Plan: api.PlanFree, RequireEmailVerification: !verified,
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), res.Account.ID, hash, "flow", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	return res.Account, plain
}

// An unverified account asks for a fresh verification link without
// signing in again; a verified one is told there is nothing to send.
func TestResendEmailVerification(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, key := flowAccountKey(t, store, "resend-me@example.com", false)
	rec := flowBearer(h, http.MethodPost, "/v1/auth/verify-email/resend", key, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("resend = %d: %s", rec.Code, rec.Body.String())
	}
	msgs := mailer.Snapshot()
	if len(msgs) != 1 || msgs[0].To[0] != "resend-me@example.com" || !strings.Contains(msgs[0].TextBody, "/v1/auth/verify-email?token=") {
		t.Fatalf("resend mail = %+v", msgs)
	}
	_, verifiedKey := flowAccountKey(t, store, "already@example.com", true)
	rec = flowBearer(h, http.MethodPost, "/v1/auth/verify-email/resend", verifiedKey, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already_verified") {
		t.Fatalf("resend for verified account = %d: %s", rec.Code, rec.Body.String())
	}
}

// Past the 30-day grace an unverified account keeps its data but cannot
// sign in by password or use its keys, except to ask for a new link. The
// verification link lifts the restriction.
func TestUnverifiedAccountRestrictedAfterGrace(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	key := flowSignup(t, h, "late@example.com", "late-password-123!")
	if code := flowBearer(h, http.MethodGet, "/v1/apps", key, "").Code; code != http.StatusOK {
		t.Fatalf("inside grace /v1/apps = %d, want 200", code)
	}
	srv.SetVerificationClock(func() time.Time { return time.Now().Add(31 * 24 * time.Hour) })

	if rec := flowBearer(h, http.MethodGet, "/v1/apps", key, ""); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), api.CodeEmailVerificationRequired) {
		t.Fatalf("overdue /v1/apps = %d %s, want 403 email_verification_required", rec.Code, rec.Body.String())
	}
	if code := flowBearer(h, http.MethodGet, "/v1/account", key, "").Code; code != http.StatusOK {
		t.Errorf("overdue GET /v1/account = %d, want 200", code)
	}
	if code := flowBearer(h, http.MethodPost, "/v1/auth/verify-email/resend", key, "").Code; code != http.StatusAccepted {
		t.Errorf("overdue resend = %d, want 202", code)
	}
	rec := apidserver.V1AuthJSONRequest(t, h, "/v1/auth/login", `{"email":"late@example.com","password":"late-password-123!"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("overdue password login = %d %s, want 403", rec.Code, rec.Body.String())
	}

	token := flowLastToken(t, mailer, regexp.MustCompile(`/v1/auth/verify-email\?token=([A-Za-z0-9_-]+)`))
	if rec := flowForm(h, "/v1/auth/verify-email", url.Values{"token": {token}}); rec.Code >= 400 {
		t.Fatalf("verify = %d %s", rec.Code, rec.Body.String())
	}
	if acct, err := store.AccountByEmail(t.Context(), "late@example.com"); err != nil || !acct.EmailVerified() {
		t.Fatalf("account not verified: %v", err)
	}
	if code := flowBearer(h, http.MethodGet, "/v1/apps", key, "").Code; code != http.StatusOK {
		t.Errorf("after verifying /v1/apps = %d, want 200", code)
	}
}

// Any non-owner member can leave an org; the owner must transfer first and
// nobody leaves a personal org. DELETE /members/{self} stays refused.
func TestLeaveOrg(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "leavers", "Leavers", api.PlanPro)
	dev, asDev := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	if r := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+dev.ID, nil, asDev); r.Code < 400 {
		t.Fatalf("developer DELETE self = %d, want refusal", r.Code)
	}
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/leave", nil, nil); r.Code != http.StatusConflict {
		t.Fatalf("owner leave = %d %s, want 409", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/leave", nil, asDev); r.Code != http.StatusNoContent {
		t.Fatalf("developer leave = %d %s, want 204", r.Code, r.Body)
	}
	members, err := store.ListOrgMembers(t.Context(), org.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if m.AccountID == dev.ID && m.RemovedAt == nil {
			t.Fatal("developer is still an active member after leaving")
		}
	}
	solo, soloKey := flowAccountKey(t, store, "solo@example.com", true)
	personal, err := store.OrgByPersonalAccount(t.Context(), solo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+personal.Slug+"/leave", nil, map[string]string{"Authorization": "Bearer " + soloKey}); r.Code < 400 {
		t.Fatalf("leave personal org = %d, want refusal", r.Code)
	}
}
