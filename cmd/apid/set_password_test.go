package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-140: POST /dashboard/account/set-password picks its proof of
// presence from what the account has, instead of demanding a TOTP
// step-up from everyone. The matrix these tests pin:
//
//	fresh step-up stamp            → accepted (unchanged for MFA users)
//	has password, no stamp         → current_password required + verified
//	no password, MFA enrolled      → 403 step_up_required
//	no password, no MFA            → accepted (the opt-in the route exists for)
//
// Every branch sits behind a purpose-bound csrf_token, because the
// route is a same-site form POST (see TestSetPassword_RefusesFormWithoutCSRFToken).
//
// Before this ADR every session-cookie principal hit the blanket
// requireStepUpHandler gate, and the only writer of a step-up stamp is
// /v1/account/mfa/verify — so an OAuth-only customer without MFA could
// never set a password at all, and a customer with one could replace
// it without re-proving anything once they had a stamp.

const (
	seededPassword = "the-seeded-password-1"
	chosenPassword = "correct-horse-battery-staple"
)

func accountID(t *testing.T, store *state.MemStore, email string) string {
	t.Helper()
	acct, err := store.AccountByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	return acct.ID
}

// seedPassword gives alice a password row so the account counts as
// "has password" for the handler's decision.
func seedPassword(t *testing.T, store *state.MemStore, id string) {
	t.Helper()
	phc, err := auth.Encode(seededPassword)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := store.SetAccountPassword(t.Context(), id, phc); err != nil {
		t.Fatalf("SetAccountPassword: %v", err)
	}
}

// postSetPasswordForm submits the form the way the console does: with
// a purpose-bound csrf_token minted for this account and its faas_csrf
// sidecar cookie. `mgr == nil` sends the form bare, for the test that
// pins the token as mandatory.
func postSetPasswordForm(t *testing.T, h http.Handler, sid *http.Cookie, mgr *session.Manager, id string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form = cloneValues(form)
	var csrfCookie *http.Cookie
	if mgr != nil {
		tok, err := middleware.IssueForAuthenticated(mgr, "set_password", id)
		if err != nil {
			t.Fatalf("issue csrf: %v", err)
		}
		form.Set("csrf_token", tok)
		csrfCookie = &http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok}
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/dashboard/account/set-password",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(sid)
	if csrfCookie != nil {
		r.AddCookie(csrfCookie)
	}
	h.ServeHTTP(rec, r)
	return rec
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not a problem+json: %v\n%s", err, rec.Body.String())
	}
	return p.Code
}

func storedPasswordVerifies(t *testing.T, store *state.MemStore, id, plain string) bool {
	t.Helper()
	phc, err := store.AccountPasswordByAccountID(t.Context(), id)
	if err != nil {
		return false
	}
	ok, err := auth.Verify(phc, plain)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return ok
}

// steppedUpCookie re-issues alice's cookie with a fresh step-up stamp,
// mirroring newSteppedUpDashboardServer but against a server whose
// store the test also holds.
func steppedUpCookie(t *testing.T, store *state.MemStore, mgr *session.Manager, id string) *http.Cookie {
	t.Helper()
	sid := "stepped-up-sid"
	if _, err := store.CreateSession(t.Context(), sid, id, "192.0.2.10", "stepped-up-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cookie, err := mgr.IssueWithSessionAndBindingHashAndStepUp(sid, id, "", time.Now(), false)
	if err != nil {
		t.Fatalf("issue stepped-up cookie: %v", err)
	}
	return &http.Cookie{Name: sessionCookie, Value: cookie}
}

// The route is a same-site form POST: a function hosted at
// *.apps.gregale.dev is same-site with api.gregale.dev, so SameSite=Lax
// still attaches faas_sid to a form it auto-submits. Without a
// purpose-bound token the `session` proof would let that page choose
// the victim's password.
func TestSetPassword_RefusesFormWithoutCSRFToken(t *testing.T) {
	h, sid, store, _ := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")

	rec := postSetPasswordForm(t, h, sid, nil, id, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodeValidation {
		t.Errorf("code = %q, want %q", code, api.CodeValidation)
	}
	if _, err := store.AccountPasswordByAccountID(t.Context(), id); err == nil {
		t.Fatal("a password was stored from a form with no CSRF token")
	}
}

func TestSetPassword_OAuthOnlyNoMFA_SetsWithoutStepUp(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	if !storedPasswordVerifies(t, store, id, chosenPassword) {
		t.Fatal("password was not stored")
	}
}

func TestSetPassword_ExplicitMFARequired_RequiresEnrollment(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	if _, err := store.SetMFARequired(t.Context(), id, true); err != nil {
		t.Fatalf("SetMFARequired: %v", err)
	}

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodeMFARequired {
		t.Errorf("code = %q, want %q", code, api.CodeMFARequired)
	}
	if _, err := store.AccountPasswordByAccountID(t.Context(), id); err == nil {
		t.Fatal("a password was stored while explicit MFA policy was pending")
	}
}

func TestSetPassword_HasPassword_RequiresCurrentPassword(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodeInvalidCredentials {
		t.Errorf("code = %q, want %q", code, api.CodeInvalidCredentials)
	}
	if !storedPasswordVerifies(t, store, id, seededPassword) {
		t.Fatal("the seeded password was replaced without proof")
	}
}

func TestSetPassword_HasPassword_RejectsWrongCurrentPassword(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{
		"password":         {chosenPassword},
		"current_password": {"not-the-seeded-password"},
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodeInvalidCredentials {
		t.Errorf("code = %q, want %q", code, api.CodeInvalidCredentials)
	}
	if !storedPasswordVerifies(t, store, id, seededPassword) {
		t.Fatal("the seeded password was replaced on a wrong current_password")
	}
}

func TestSetPassword_HasPassword_AcceptsCorrectCurrentPassword(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{
		"password":         {chosenPassword},
		"current_password": {seededPassword},
	})

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	if !storedPasswordVerifies(t, store, id, chosenPassword) {
		t.Fatal("the new password was not stored")
	}
}

func TestSetPassword_HasPassword_FreshStepUpStandsInForCurrentPassword(t *testing.T) {
	h, _, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)
	sid := steppedUpCookie(t, store, mgr, id)

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	if !storedPasswordVerifies(t, store, id, chosenPassword) {
		t.Fatal("the new password was not stored")
	}
}

// The second factor outranks the knowledge factor: with TOTP enrolled,
// the right current_password is not enough — otherwise a phished
// password plus a stolen session could rotate it.
func TestSetPassword_HasPasswordWithMFA_RequiresStepUpDespiteCurrentPassword(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)
	if err := store.MarkMFAEnrolled(t.Context(), id); err != nil {
		t.Fatalf("MarkMFAEnrolled: %v", err)
	}

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{
		"password":         {chosenPassword},
		"current_password": {seededPassword},
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodeStepUpRequired {
		t.Errorf("code = %q, want %q", code, api.CodeStepUpRequired)
	}
	if !storedPasswordVerifies(t, store, id, seededPassword) {
		t.Fatal("the seeded password was replaced without a step-up")
	}
}

func TestSetPassword_OAuthOnlyWithMFA_RequiresStepUp(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	if err := store.MarkMFAEnrolled(t.Context(), id); err != nil {
		t.Fatalf("MarkMFAEnrolled: %v", err)
	}

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodeStepUpRequired {
		t.Errorf("code = %q, want %q", code, api.CodeStepUpRequired)
	}
	if _, err := store.AccountPasswordByAccountID(t.Context(), id); err == nil {
		t.Fatal("a password was stored without a step-up")
	}
}

// The length rule is free and inspects only the new password, so it runs
// before the proof — no DB read and no Argon2id verify for a request
// that was never going to be accepted. Observable: a short password
// answers 400 even when current_password is wrong.
func TestSetPassword_LengthRuleRunsBeforeProof(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{
		"password":         {"short"},
		"current_password": {"not-the-seeded-password"},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodePasswordTooWeak {
		t.Errorf("code = %q, want %q", code, api.CodePasswordTooWeak)
	}
}

// current_password is a credential check, so it sits in the same
// per-IP failure bucket as /login (§11: 10 failures/min/IP). A stolen
// session must not be a free oracle for guessing the password.
func TestSetPassword_WrongCurrentPasswordIsRateLimited(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)

	form := url.Values{
		"password":         {chosenPassword},
		"current_password": {"not-the-seeded-password"},
	}
	var last *httptest.ResponseRecorder
	for i := 0; i < 12; i++ {
		last = postSetPasswordForm(t, h, sid, mgr, id, form)
		if last.Code == http.StatusTooManyRequests {
			break
		}
		if last.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, want 401 or 429\nbody = %s", i+1, last.Code, last.Body.String())
		}
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("12 wrong guesses never tripped the limiter; last code = %d", last.Code)
	}
	if !storedPasswordVerifies(t, store, id, seededPassword) {
		t.Fatal("the seeded password was replaced during a guessing run")
	}
}

func TestSetPassword_WeakPasswordStillRefusedAfterProof(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{
		"password":         {"short"},
		"current_password": {seededPassword},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodePasswordTooWeak {
		t.Errorf("code = %q, want %q", code, api.CodePasswordTooWeak)
	}
	if !storedPasswordVerifies(t, store, id, seededPassword) {
		t.Fatal("the seeded password was replaced by a weak one")
	}
}

// --- Coverage for the refusal and failure branches ----------------------

// newSetPasswordServer is newAuthedDashboardServerFull with a caller-
// supplied store, so a test can wrap the MemStore and fail one method.
func newSetPasswordServer(t *testing.T, store state.Store, accountID string) (http.Handler, *http.Cookie, *session.Manager) {
	t.Helper()
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	cookie, err := mgr.Issue(accountID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*60_000_000_000, "")
	return srv.handler(), &http.Cookie{Name: sessionCookie, Value: cookie}, mgr
}

// failingPasswordStore fails the password read or write on demand;
// everything else is the real MemStore.
type failingPasswordStore struct {
	*state.MemStore
	failRead  bool
	failWrite bool
}

var errStoreDown = errors.New("store: simulated failure")

func (f *failingPasswordStore) AccountPasswordByAccountID(ctx context.Context, id string) (string, error) {
	if f.failRead {
		return "", errStoreDown
	}
	return f.MemStore.AccountPasswordByAccountID(ctx, id)
}

func (f *failingPasswordStore) SetAccountPassword(ctx context.Context, id, phc string) error {
	if f.failWrite {
		return errStoreDown
	}
	return f.MemStore.SetAccountPassword(ctx, id, phc)
}

func TestSetPassword_MalformedFormBodyIsRefused(t *testing.T) {
	h, sid, _, _ := newAuthedDashboardServerFull(t)
	rec := httptest.NewRecorder()
	// "%zz" is not valid percent-encoding, so ParseForm fails before
	// anything else runs.
	r := httptest.NewRequest(http.MethodPost, "/dashboard/account/set-password",
		strings.NewReader("password=%zz"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(sid)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodeValidation {
		t.Errorf("code = %q, want %q", code, api.CodeValidation)
	}
}

// A stamp older than the window is reported as "expired", not
// "missing" — the split ADR-077's audit queries depend on.
func TestSetPassword_StaleStepUpIsAuditedAsExpired(t *testing.T) {
	h, _, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	if err := store.MarkMFAEnrolled(t.Context(), id); err != nil {
		t.Fatalf("MarkMFAEnrolled: %v", err)
	}
	sidValue := "stale-stepped-up-sid"
	if _, err := store.CreateSession(t.Context(), sidValue, id, "192.0.2.10", "stale-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stale, err := mgr.IssueWithSessionAndBindingHashAndStepUp(sidValue, id, "", time.Now().Add(-10*time.Minute), false)
	if err != nil {
		t.Fatalf("issue stale cookie: %v", err)
	}
	sid := &http.Cookie{Name: sessionCookie, Value: stale}

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403\nbody = %s", rec.Code, rec.Body.String())
	}
	events, err := store.ListEvents(t.Context(), id, 20)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var reason string
	for _, ev := range events {
		if ev.Kind != "auth.step_up_required" {
			continue
		}
		var data struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatalf("event data: %v", err)
		}
		reason = data.Reason
	}
	if reason != "expired" {
		t.Fatalf("audit reason = %q, want %q", reason, "expired")
	}
}

// A stored hash that Verify cannot parse is a data problem, logged
// server-side; the caller still gets the same 401 as a wrong guess.
func TestSetPassword_MalformedStoredHashIsRefused(t *testing.T) {
	h, sid, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	if err := store.SetAccountPassword(t.Context(), id, "not-a-phc-string"); err != nil {
		t.Fatalf("seed malformed hash: %v", err)
	}

	rec := postSetPasswordForm(t, h, sid, mgr, id, url.Values{
		"password":         {chosenPassword},
		"current_password": {seededPassword},
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401\nbody = %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); code != api.CodeInvalidCredentials {
		t.Errorf("code = %q, want %q", code, api.CodeInvalidCredentials)
	}
	if phc, _ := store.AccountPasswordByAccountID(t.Context(), id); phc != "not-a-phc-string" {
		t.Fatal("the stored hash was replaced on a verify error")
	}
}

func TestSetPassword_PasswordLookupFailureIs500(t *testing.T) {
	inner := state.NewMemStore()
	acct, err := inner.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	store := &failingPasswordStore{MemStore: inner, failRead: true}
	h, sid, mgr := newSetPasswordServer(t, store, acct.ID)

	rec := postSetPasswordForm(t, h, sid, mgr, acct.ID, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500\nbody = %s", rec.Code, rec.Body.String())
	}
	if _, err := inner.AccountPasswordByAccountID(t.Context(), acct.ID); err == nil {
		t.Fatal("a password was stored although the lookup failed")
	}
}

func TestSetPassword_PasswordWriteFailureIs500(t *testing.T) {
	inner := state.NewMemStore()
	acct, err := inner.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	store := &failingPasswordStore{MemStore: inner, failWrite: true}
	h, sid, mgr := newSetPasswordServer(t, store, acct.ID)

	rec := postSetPasswordForm(t, h, sid, mgr, acct.ID, url.Values{"password": {chosenPassword}})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500\nbody = %s", rec.Code, rec.Body.String())
	}
	if _, err := inner.AccountPasswordByAccountID(t.Context(), acct.ID); err == nil {
		t.Fatal("a password was stored although the write failed")
	}
}
