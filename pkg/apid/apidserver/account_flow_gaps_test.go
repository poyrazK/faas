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
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
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

// flowSession mints a dashboard session cookie for accountID.
func flowSession(t *testing.T, store state.Store, mgr *session.Manager, accountID string) *http.Cookie {
	t.Helper()
	tok, err := apidserver.MintDashboardSession(t.Context(), store, mgr, accountID)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: apidserver.SessionCookie, Value: tok}
}

// flowSessionJSON sends a same-origin JSON request on a browser session,
// with a CSRF token for action when action is set.
func flowSessionJSON(t *testing.T, h http.Handler, mgr *session.Manager, cookie *http.Cookie, accountID, method, path, action string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	csrf := ""
	if action != "" {
		tok, err := middleware.IssueForAuthenticated(mgr, action, accountID)
		if err != nil {
			t.Fatal(err)
		}
		csrf = tok
		body["csrf_token"] = tok
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.AddCookie(cookie)
	if csrf != "" {
		req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrf})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func flowMailsTo(mailer *apidserver.RecordingMailer, to string) []string {
	var out []string
	for _, m := range mailer.Snapshot() {
		for _, rcpt := range m.To {
			if rcpt == to {
				out = append(out, m.TextBody)
			}
		}
	}
	return out
}

// An email change is requested from a browser session, confirmed from the
// new address, signs out every session and notifies the old address. An
// API key cannot request it, and a taken address is mailed nothing.
func TestEmailChangeConfirmsNewAddress(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	mgr := *srv.Sessions()
	acct, key := flowAccountKey(t, store, "before@example.com", true)
	flowAccountKey(t, store, "taken@example.com", true)
	cookie := flowSession(t, store, mgr, acct.ID)

	if code := flowBearer(h, http.MethodPost, "/v1/account/email", key, `{"email":"after@example.com"}`).Code; code != http.StatusForbidden {
		t.Fatalf("email change with an API key = %d, want 403", code)
	}
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/email", "account_email_change", map[string]any{"email": "taken@example.com"}); rec.Code != http.StatusAccepted {
		t.Fatalf("change to taken address = %d %s, want 202", rec.Code, rec.Body.String())
	}
	if got := flowMailsTo(mailer, "taken@example.com"); len(got) != 0 {
		t.Fatalf("taken address was mailed: %q", got)
	}
	srv.ResetSecurityMailLimiter()
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/email", "account_email_change", map[string]any{"email": "After@Example.com"}); rec.Code != http.StatusAccepted {
		t.Fatalf("change = %d %s, want 202", rec.Code, rec.Body.String())
	}
	token := flowLastToken(t, mailer, regexp.MustCompile(`/auth/email-change\?token=([A-Za-z0-9_-]+)`))
	if got := flowMailsTo(mailer, "after@example.com"); len(got) != 1 {
		t.Fatalf("confirmation mails to the new address = %d, want 1", len(got))
	}
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/auth/email-change?token="+token, nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/auth/email-change"`) {
		t.Fatalf("confirm page = %d %s", page.Code, page.Body.String())
	}
	if acct2, _ := store.AccountByID(t.Context(), acct.ID); acct2.Email != "before@example.com" {
		t.Fatal("rendering the confirm page changed the email")
	}
	rec := flowForm(h, "/auth/email-change", url.Values{"token": {token}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "email_changed=1") {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body.String())
	}
	moved, err := store.AccountByEmail(t.Context(), "after@example.com")
	if err != nil || moved.ID != acct.ID || !moved.EmailVerified() {
		t.Fatalf("account after change: %+v err=%v", moved, err)
	}
	if got := flowMailsTo(mailer, "before@example.com"); len(got) != 1 || !strings.Contains(got[0], "a***@example.com") {
		t.Fatalf("old address notice = %q", got)
	}
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodGet, "/v1/account", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("session after email change = %d, want 401", rec.Code)
	}
	if rec := flowForm(h, "/auth/email-change", url.Values{"token": {token}}); rec.Code < 400 {
		t.Errorf("second use of the link = %d, want refusal", rec.Code)
	}
}

// A password account proves the current password to change its email.
func TestEmailChangeNeedsCurrentPassword(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	mgr := *srv.Sessions()
	flowSignup(t, h, "pw@example.com", "pw-password-123!")
	acct, err := store.AccountByEmail(t.Context(), "pw@example.com")
	if err != nil {
		t.Fatal(err)
	}
	cookie := flowSession(t, store, mgr, acct.ID)
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/email", "account_email_change",
		map[string]any{"email": "pw2@example.com", "current_password": "wrong-password-123!"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password = %d %s, want 401", rec.Code, rec.Body.String())
	}
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/email", "account_email_change",
		map[string]any{"email": "pw2@example.com", "current_password": "pw-password-123!"}); rec.Code != http.StatusAccepted {
		t.Fatalf("right current password = %d %s, want 202", rec.Code, rec.Body.String())
	}
}

// Linked providers are listed and can be removed unless they are the last
// way to sign in.
func TestSignInMethodsListAndUnlink(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	mgr := *srv.Sessions()
	acct, key := flowAccountKey(t, store, "linked@example.com", true)
	for _, p := range []string{"google", "github"} {
		if err := store.UpsertOAuthLink(t.Context(), acct.ID, p, p+"-sub", "linked@example.com", true); err != nil {
			t.Fatal(err)
		}
	}
	rec := flowBearer(h, http.MethodGet, "/v1/account/sign-in-methods", key, "")
	var methods api.SignInMethodsResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &methods) != nil || len(methods.Providers) != 2 || methods.Password {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	cookie := flowSession(t, store, mgr, acct.ID)
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodDelete, "/v1/account/sign-in-methods/github", "sign_in_method_unlink", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink github = %d %s, want 204", rec.Code, rec.Body.String())
	}
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodDelete, "/v1/account/sign-in-methods/google", "sign_in_method_unlink", nil); rec.Code != http.StatusConflict {
		t.Fatalf("unlink last method = %d %s, want 409", rec.Code, rec.Body.String())
	}
}

// A step-up session replaces every recovery code; the old ones stop working.
func TestRecoveryCodesRegenerate(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	oldCodes, _, fresh := e.GenerateEnrolledAccount(t)
	acct := *e.Acct()
	mgr := *(*e.Srv()).Sessions()
	rec := flowSessionJSON(t, *e.H(), mgr, fresh, acct.ID, http.MethodPost, "/v1/account/mfa/recovery-codes", "mfa_recovery_codes", nil)
	var out api.RecoveryCodesResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.RecoveryCodes) != len(oldCodes) {
		t.Fatalf("regenerate = %d %s", rec.Code, rec.Body.String())
	}
	for i, code := range []string{oldCodes[0], out.RecoveryCodes[0]} {
		presented, err := authcode.HashRecoveryCode(code)
		if err != nil {
			t.Fatal(err)
		}
		matched, _, err := (*e.Store()).MatchRecoveryCode(t.Context(), acct.ID, presented)
		if err != nil || matched != (i == 1) {
			t.Fatalf("code %d matched=%v err=%v (old codes must stop working, new ones must work)", i, matched, err)
		}
	}
}

// PATCH /v1/account sets the display name; GET returns it.
func TestDisplayName(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, key := flowAccountKey(t, store, "named@example.com", true)
	if rec := flowBearer(h, http.MethodPatch, "/v1/account", key, `{"display_name":"  Ada Lovelace "}`); rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	if rec := flowBearer(h, http.MethodGet, "/v1/account", key, ""); !strings.Contains(rec.Body.String(), `"display_name":"Ada Lovelace"`) {
		t.Fatalf("whoami = %s", rec.Body.String())
	}
	if rec := flowBearer(h, http.MethodPatch, "/v1/account", key, `{"display_name":"`+strings.Repeat("x", api.DisplayNameMaxChars+1)+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("too long = %d, want 400", rec.Code)
	}
}

// A sign-in is new only when no recent session shares its browser family
// and network; an account's first sign-in is not.
func TestSignInIsNew(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	acct, _ := flowAccountKey(t, store, "devices@example.com", true)
	chrome := "Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
	firefox := "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0"
	if srv.SignInIsNew(t.Context(), acct.ID, "198.51.100.7", chrome) {
		t.Fatal("first sign-in reported as new")
	}
	if _, err := store.CreateSession(t.Context(), "11111111-1111-1111-1111-111111111111", acct.ID, "198.51.100.7", chrome); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ip, ua string
		want   bool
	}{
		{"198.51.100.99", chrome, false},
		{"203.0.113.5", chrome, true},
		{"198.51.100.7", firefox, true},
	} {
		if got := srv.SignInIsNew(t.Context(), acct.ID, tc.ip, tc.ua); got != tc.want {
			t.Errorf("SignInIsNew(%s, %s) = %v, want %v", tc.ip, tc.ua[:20], got, tc.want)
		}
	}
}

// flowAccountPage renders /dashboard/account and returns the body and the
// form CSRF cookies it set.
func flowAccountPage(t *testing.T, h http.Handler, session *http.Cookie, query string) (string, []*http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/account"+query, nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("account page = %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String(), rec.Result().Cookies()
}

func flowFormToken(t *testing.T, body, action string) string {
	t.Helper()
	m := regexp.MustCompile(`action="` + regexp.QuoteMeta(action) + `">\s*<input type="hidden" name="csrf_token" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no form for %s on the account page", action)
	}
	return m[1]
}

func flowDashboardPost(h http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
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

// The account page lists sessions and linked providers, and every form on
// it answers a refusal with a redirect to a fixed message, not JSON.
func TestAccountPageSecurityForms(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	mgr := *srv.Sessions()
	flowSignup(t, h, "page@example.com", "page-password-123!")
	acct, err := store.AccountByEmail(t.Context(), "page@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertOAuthLink(t.Context(), acct.ID, "github", "gh-page", acct.Email, true); err != nil {
		t.Fatal(err)
	}
	other := "33333333-3333-3333-3333-333333333333"
	if _, err := store.CreateSession(t.Context(), other, acct.ID, "203.0.113.9", "other-browser"); err != nil {
		t.Fatal(err)
	}
	session := flowSession(t, store, mgr, acct.ID)
	body, cookies := flowAccountPage(t, h, session, "")
	for _, want := range []string{"Sign-in methods", "github", "Sessions", "other-browser", "Sign out (this browser)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("account page lacks %q", want)
		}
	}
	all := append([]*http.Cookie{session}, cookies...)

	rec := flowDashboardPost(h, "/dashboard/account/email", url.Values{
		"csrf_token": {flowFormToken(t, body, "/dashboard/account/email")}, "email": {"page2@example.com"}, "current_password": {"wrong-password-123!"},
	}, all...)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "account_error=password") {
		t.Fatalf("wrong password on the email form = %d %q, want a redirect with account_error=password", rec.Code, rec.Header().Get("Location"))
	}
	if page, _ := flowAccountPage(t, h, session, "?account_error=password"); !strings.Contains(page, "Your current password was not correct.") {
		t.Fatal("account page does not explain the refused email change")
	}
	if page, _ := flowAccountPage(t, h, session, "?account_error=%3Cscript%3E"); strings.Contains(page, "<script>") {
		t.Fatal("account page echoed an unknown error key")
	}

	rec = flowDashboardPost(h, "/dashboard/account/sessions/"+other+"/revoke", url.Values{
		"csrf_token": {regexp.MustCompile(`action="/dashboard/account/sessions/` + other + `/revoke">\s*<input type="hidden" name="csrf_token" value="([^"]+)"`).FindStringSubmatch(body)[1]},
	}, all...)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "session=revoked") {
		t.Fatalf("revoke other session = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got, err := store.GetSession(t.Context(), other); err != nil || got.RevokedAt == nil {
		t.Fatalf("other session not revoked: %+v %v", got, err)
	}

	rec = flowDashboardPost(h, "/dashboard/account/providers/github/unlink", url.Values{
		"csrf_token": {flowFormToken(t, body, "/dashboard/account/providers/github/unlink")},
	}, all...)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "provider=removed") {
		t.Fatalf("unlink github with a password set = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = flowDashboardPost(h, "/dashboard/account/display-name", url.Values{"csrf_token": {"stale"}, "display_name": {"x"}}, all...)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "account_error=expired") {
		t.Fatalf("stale CSRF token = %d %q, want account_error=expired", rec.Code, rec.Header().Get("Location"))
	}
}

// The sign-in page offers a magic link and answers the browser form with
// the same page and a constant message.
func TestLoginPageMagicLink(t *testing.T) {
	srv, mailer, _ := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/login", nil))
	if !strings.Contains(page.Body.String(), `action="/login/magic-link"`) {
		t.Fatal("sign-in page has no magic link form")
	}
	req := httptest.NewRequest(http.MethodPost, "/login/magic-link", strings.NewReader(url.Values{"email": {"linky@example.com"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "a sign-in link is on its way") {
		t.Fatalf("magic link form = %d: %s", rec.Code, rec.Body.String())
	}
	flowLastToken(t, mailer, flowMagicLinkToken)
}

// Org member lists carry each member's display name.
func TestOrgMembersShowDisplayName(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "named-team", "Named Team", api.PlanPro)
	dev, _ := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	if err := store.SetAccountDisplayName(t.Context(), dev.ID, "Dev Person"); err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/members", nil, nil)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"display_name":"Dev Person"`) {
		t.Fatalf("members = %d %s", r.Code, r.Body)
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
