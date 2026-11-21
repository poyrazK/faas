package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPasswordSignupSendsSingleUseVerificationLink(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	rec := apidserver.V1AuthJSONRequest(t, h, "/v1/auth/signup",
		`{"email":"verify-me@example.com","password":"correct-horse-battery-staple"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("signup status = %d: %s", rec.Code, rec.Body.String())
	}
	acct, err := store.AccountByEmail(context.Background(), "verify-me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if acct.EmailVerified() {
		t.Fatal("password signup created a verified account")
	}
	msgs := mailer.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("verification emails = %d, want 1", len(msgs))
	}
	const marker = "/v1/auth/verify-email?token="
	i := strings.Index(msgs[0].TextBody, marker)
	if i < 0 {
		t.Fatalf("verification email missing link: %q", msgs[0].TextBody)
	}
	token := strings.Fields(msgs[0].TextBody[i+len(marker):])[0]
	verifyURL := apidserver.EmailVerificationPath + "?token=" + token

	// The other account receives recovery instructions, not a form that
	// returns to the same sign-in via a saved browser session hint.
	other, _ := flowAccountKey(t, store, "other-browser@example.com", true)
	otherCookie := flowSession(t, store, *srv.Sessions(), other.ID)
	switchRec := httptest.NewRecorder()
	switchReq := httptest.NewRequest(http.MethodGet, verifyURL, nil)
	switchReq.Header.Set("Accept", "text/html")
	switchReq.AddCookie(otherCookie)
	h.ServeHTTP(switchRec, switchReq)
	page := html.UnescapeString(switchRec.Body.String())
	signInURL := "/login?reauth=1&verify=1&next=" + url.QueryEscape(verifyURL)
	if switchRec.Code != http.StatusOK || !strings.Contains(page, "This link is for a different account") ||
		!strings.Contains(page, `href="`+signInURL+`"`) || strings.Contains(page, `name="token"`) ||
		strings.Contains(page, acct.Email) || strings.Contains(page, other.Email) {
		t.Fatalf("wrong account recovery = %d: %s", switchRec.Code, page)
	}
	if switchRec.Header().Get("Referrer-Policy") != "no-referrer" || switchRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("wrong account page exposed the verification URL to referrers or caches: %v", switchRec.Header())
	}

	// Submit the normal password form with the old cookie still present.
	login := httptest.NewRecorder()
	form := url.Values{"email": {acct.Email}, "password": {"correct-horse-battery-staple"}, "next": {verifyURL}}
	loginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Accept", "text/html")
	loginReq.AddCookie(otherCookie)
	h.ServeHTTP(login, loginReq)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != verifyURL {
		t.Fatalf("account switch did not bring the browser back to verification: %d %s", login.Code, login.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == apidserver.SessionCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value == "" || cookie.Value == otherCookie.Value {
		t.Fatal("account switch did not set a new session")
	}

	// Opening the link with the correct session only shows confirmation.
	openRec := httptest.NewRecorder()
	openReq := httptest.NewRequest(http.MethodGet, verifyURL, nil)
	openReq.Header.Set("Accept", "text/html,application/xhtml+xml")
	openReq.AddCookie(cookie)
	h.ServeHTTP(openRec, openReq)
	if openRec.Code != http.StatusOK || !strings.Contains(openRec.Body.String(), `method="post"`) {
		t.Fatalf("open link = %d: %s", openRec.Code, openRec.Body.String())
	}
	if opened, err := store.AccountByEmail(context.Background(), "verify-me@example.com"); err != nil || opened.EmailVerified() {
		t.Fatalf("opening the link verified the account (err=%v)", err)
	}

	// The post verifies only from a browser signed in to the account.
	verifyRec := httptest.NewRecorder()
	verifyReq := verifyEmailPost(apidserver.EmailVerificationPath, token, cookie)
	verifyReq.Header.Set("Accept", "text/html")
	h.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK || !strings.Contains(verifyRec.Body.String(), "Email verified") ||
		!strings.Contains(verifyRec.Body.String(), "<code>gregale login</code>") {
		t.Fatalf("verify status = %d: %s", verifyRec.Code, verifyRec.Body.String())
	}
	acct, err = store.AccountByEmail(context.Background(), "verify-me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !acct.EmailVerified() {
		t.Fatal("verification link did not verify account")
	}

	replayRec := httptest.NewRecorder()
	h.ServeHTTP(replayRec, verifyEmailPost(apidserver.EmailVerificationPath, token, cookie))
	if replayRec.Code != http.StatusGone {
		t.Fatalf("replay status = %d, want 410", replayRec.Code)
	}

	// The old CLI approval can expire while the browser is signing in again.
	const expiredCode = "cafe1234"
	if err := store.IssueCliAuthCode(t.Context(), api.HashToken(mustHex(t, expiredCode)), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	retryURL := "/cli-auth?code=" + expiredCode
	reauthForm := url.Values{
		"email": {acct.Email}, "password": {"correct-horse-battery-staple"}, "next": {retryURL},
	}
	reauthReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(reauthForm.Encode()))
	reauthReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reauthReq.Header.Set("Accept", "text/html")
	reauthReq.AddCookie(cookie)
	reauth := httptest.NewRecorder()
	h.ServeHTTP(reauth, reauthReq)
	if reauth.Code != http.StatusSeeOther || reauth.Header().Get("Location") != retryURL {
		t.Fatalf("reauth did not return to the old approval link: %d", reauth.Code)
	}
	var reauthenticated *http.Cookie
	for _, c := range reauth.Result().Cookies() {
		if c.Name == apidserver.SessionCookie {
			reauthenticated = c
		}
	}
	if reauthenticated == nil {
		t.Fatal("reauth did not issue a session")
	}
	retryReq := httptest.NewRequest(http.MethodGet, retryURL, nil)
	retryReq.AddCookie(reauthenticated)
	retryRec := httptest.NewRecorder()
	h.ServeHTTP(retryRec, retryReq)
	body := retryRec.Body.String()
	if retryRec.Code != http.StatusOK || !strings.Contains(body, "Code unavailable") ||
		!strings.Contains(body, acct.Email) || !strings.Contains(body, "<code>gregale login</code>") ||
		!strings.Contains(body, "Open the new link in this browser") ||
		strings.Contains(body, expiredCode) || strings.Contains(body, `name="code"`) {
		t.Fatalf("expired approval did not offer recovery in the signed-in browser: %d %s", retryRec.Code, body)
	}

	// A fresh terminal code can be approved under the same reauthenticated session.
	const cliCode = "a1b2c3d4"
	pendingCliCode(t, store, cliCode)
	approveCliCode(t, srv, reauthenticated, acct.ID, cliCode)
	if _, approver, err := store.PeekCliAuthCode(t.Context(), api.HashToken(mustHex(t, cliCode))); err != nil || approver != acct.ID {
		t.Fatalf("verified account could not approve the fresh CLI code: %v", err)
	}
}

// verifyEmailPost is the confirmation page's form post of a verification
// token.
func verifyEmailPost(target, token string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader("token="+token))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

// TestUnverifiedAccountCannotReachDeployOrBilling_Property exercises the
// authorization invariant over every customer route that can publish code or
// touch payment state. Handler bodies must remain unreachable for any
// unverified account, regardless of which valid bearer key it presents.
func TestUnverifiedAccountCannotReachDeployOrBilling_Property(t *testing.T) {
	for n := 0; n < 12; n++ {
		store := state.NewMemStore()
		res, err := store.CreateAccountWithPersonalOrg(context.Background(), state.CreateAccountWithPersonalOrgParams{
			Email:                    fmt.Sprintf("unverified-%d@example.com", n),
			Plan:                     api.PlanFree,
			RequireEmailVerification: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(context.Background(), res.Account.ID, hash, "verification-property", api.ScopesAdminOnly); err != nil {
			t.Fatal(err)
		}
		// Address a real personal app so resource authorization reaches the
		// verification gate rather than returning an absent-resource response.
		if _, err := store.CreateApp(t.Context(), state.App{AccountID: res.Account.ID, OrgID: res.PersonalOrg.ID, Slug: "blocked-app", Type: state.AppTypeApp}); err != nil {
			t.Fatal(err)
		}
		before, err := store.CountDeployedApps(t.Context(), res.Account.ID)
		if err != nil {
			t.Fatal(err)
		}
		srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
		h := srv.Handler()
		cases := []struct {
			method string
			path   string
			body   string
		}{
			{http.MethodPost, "/v1/apps", `{"slug":"blocked-app","type":"app","ram_mb":128,"max_concurrency":1}`},
			{http.MethodPost, "/v1/apps/blocked-app/deployments", `{}`},
			{http.MethodPost, "/v1/apps/blocked-app/deployments/dev-source", ""},
			{http.MethodPost, "/v1/apps/blocked-app/deployments/source-ref", `{}`},
			{http.MethodPost, "/v1/apps/blocked-app/deployments/source-tarball", ""},
			// The resumable upload routes are the CLI's default source deploy
			// path and published code without the legacy route's gate.
			{http.MethodPost, "/v1/uploads", `{"app_slug":"blocked-app","total_size":10}`},
			{http.MethodPost, "/v1/uploads/00000000-0000-0000-0000-000000000000/commit", ""},
			{http.MethodPatch, "/v1/account/plan", `{"plan":"hobby"}`},
			{http.MethodGet, "/v1/billing/portal", ""},
			{http.MethodPost, "/v1/billing/retry", ""},
			{http.MethodPost, "/v1/billing/cancel", ""},
			// Project apply created an app and a building deployment for
			// an unverified account; the project and GitHub routes that
			// create or publish deployments carry the same gate.
			{http.MethodPost, "/v1/projects", ""},
			{http.MethodPost, "/v1/projects/blocked/environments", `{}`},
			{http.MethodPost, "/v1/projects/blocked/environments/prod/release-sets", `{}`},
			{http.MethodPost, "/v1/projects/blocked/environments/prod/promote", `{}`},
			{http.MethodPost, "/v1/apps/blocked-app/github/bind", `{}`},
			{http.MethodPost, "/v1/apps/blocked-app/github/sync", `{}`},
			// A developer session creates an app and can provision a
			// managed Postgres database; both routes require verification.
			{http.MethodPut, "/v1/dev/sessions/blocked-project", `{}`},
		}
		for _, tc := range cases {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+plaintext)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("iteration %d %s %s status = %d: %s", n, tc.method, tc.path, rec.Code, rec.Body.String())
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != api.CodeEmailVerificationRequired {
				t.Fatalf("iteration %d %s %s code = %q", n, tc.method, tc.path, problem.Code)
			}
		}
		if count, err := store.CountDeployedApps(context.Background(), res.Account.ID); err != nil || count != before {
			t.Fatalf("iteration %d deployed apps = %d, err=%v", n, count, err)
		}
	}
}

// An unverified account's owner who forgot the password gets a magic
// sign-in link, never a reset link: redeeming it claims the account.
func TestForgotPasswordSendsUnverifiedAccountAMagicLink(t *testing.T) {
	store := state.NewMemStore()
	_, err := store.CreateAccountWithPersonalOrg(context.Background(), state.CreateAccountWithPersonalOrgParams{
		Email:                    "unverified-reset@example.com",
		Plan:                     api.PlanFree,
		RequireEmailVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	mailer := &apidserver.RecordingMailer{}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", apidserver.NoopNotifier{}, "", mailer, nil, nil, nil, 0, "")
	req := httptest.NewRequest(http.MethodPost, "/login/forgot", strings.NewReader("email=unverified-reset%40example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot status = %d", rec.Code)
	}
	srv.WaitPasswordResetMail()
	msgs := mailer.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("emails = %d, want 1 magic sign-in link", len(msgs))
	}
	if body := msgs[0].TextBody; !strings.Contains(body, "/auth/verify?token=") || strings.Contains(body, "/auth/reset") {
		t.Fatalf("unverified forgot-password mail is not a magic link:\n%s", body)
	}
}

func TestOAuthProofVerifiesExistingPasswordAccount(t *testing.T) {
	store := state.NewMemStore()
	res, err := store.CreateAccountWithPersonalOrg(context.Background(), state.CreateAccountWithPersonalOrgParams{
		Email:                    "oauth-proof@example.com",
		Plan:                     api.PlanFree,
		RequireEmailVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	acct, err := srv.VerifyOAuthAccountEmail(context.Background(), res.Account, res.Account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if !acct.EmailVerified() {
		t.Fatal("OAuth email proof did not verify existing account")
	}
	persisted, err := store.AccountByID(context.Background(), res.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.EmailVerified() {
		t.Fatal("OAuth email proof was not persisted")
	}
}
