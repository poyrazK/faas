package apidserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func anonymousSession(t *testing.T, h http.Handler, remote string) (*httptest.ResponseRecorder, api.AnonymousSessionResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/anonymous/sessions", strings.NewReader(`{"accept_terms":true,"client":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	h.ServeHTTP(rec, req)
	var out api.AnonymousSessionResponse
	if rec.Code == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode session: %v (%s)", err, rec.Body.String())
		}
	}
	return rec, out
}

func claimBearer(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "198.51.100.7:4000"
	h.ServeHTTP(rec, req)
	return rec
}

func claimForm(h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "198.51.100.8:4000"
	h.ServeHTTP(rec, req)
	return rec
}

var claimConfirmLink = regexp.MustCompile(`/claim/confirm\?token=([A-Za-z0-9_-]+)`)

// An agent deploys with no account; the person who opens the claim link and
// confirms an address owns the same account, with its apps, and the agent's
// anonymous key stops working (ADR-20261124080000).
func TestAnonymousDeployIsClaimedInPlace(t *testing.T) {
	srv, mailer, store := v1AuthTestHarness(t)
	h := srv.handler()

	rec, sess := anonymousSession(t, h, "203.0.113.10:5000")
	if rec.Code != http.StatusCreated {
		t.Fatalf("anonymous session = %d: %s", rec.Code, rec.Body.String())
	}
	if sess.APIKey == "" || !strings.Contains(sess.ClaimURL, "/claim/") || sess.Limits.Plan != string(api.PlanFree) {
		t.Fatalf("session = %+v", sess)
	}
	if got := time.Until(sess.ExpiresAt); got < 71*time.Hour || got > 73*time.Hour {
		t.Fatalf("expires in %s, want about 72h", got)
	}
	acct, err := store.AccountByID(t.Context(), sess.AccountID)
	if err != nil || !isUnclaimedAccount(acct) || !acct.EmailVerified() || acct.Plan != api.PlanFree {
		t.Fatalf("account = %+v (err %v)", acct, err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "agent-app", Status: state.AppActive, RAMMB: 256}); err != nil {
		t.Fatal(err)
	}

	// The key deploys and reads; it may not reach what needs a claim.
	if rec := claimBearer(h, http.MethodGet, "/v1/apps", sess.APIKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/apps = %d: %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/v1/keys", "/v1/domains", "/v1/orgs"} {
		if rec := claimBearer(h, http.MethodPost, path, sess.APIKey, `{}`); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeClaimRequired) {
			t.Errorf("POST %s = %d %s, want 403 claim_required", path, rec.Code, rec.Body.String())
		}
	}

	// A fresh claim link replaces the first one.
	rec = claimBearer(h, http.MethodPost, "/v1/account/claim-link", sess.APIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("claim-link = %d: %s", rec.Code, rec.Body.String())
	}
	var link api.ClaimLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	oldPath := sess.ClaimURL[strings.Index(sess.ClaimURL, "/claim/"):]
	newPath := link.ClaimURL[strings.Index(link.ClaimURL, "/claim/"):]
	if rec := httptest.NewRecorder(); true {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, oldPath, nil))
		if rec.Code != http.StatusGone {
			t.Errorf("replaced claim link = %d, want 410", rec.Code)
		}
	}
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, newPath, nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "agent-app") {
		t.Fatalf("claim page = %d: %s", page.Code, page.Body.String())
	}

	// The claimer confirms an address.
	if rec := claimForm(h, newPath, url.Values{"email": {"owner@example.com"}}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your email") {
		t.Fatalf("claim post = %d: %s", rec.Code, rec.Body.String())
	}
	srv.resetMail.wait()
	var token string
	for _, msg := range mailer.snapshot() {
		if len(msg.To) == 1 && msg.To[0] == "owner@example.com" {
			if m := claimConfirmLink.FindStringSubmatch(msg.TextBody); m != nil {
				token = m[1]
			}
		}
	}
	if token == "" {
		t.Fatalf("no claim confirmation mailed: %+v", mailer.snapshot())
	}
	confirm := httptest.NewRecorder()
	h.ServeHTTP(confirm, httptest.NewRequest(http.MethodGet, claimConfirmPath+"?token="+token, nil))
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm page = %d", confirm.Code)
	}
	rec = claimForm(h, claimConfirmPath, url.Values{"token": {token}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), magicLinkPath+"?token=") {
		t.Fatalf("confirm = %d location %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	claimed, err := store.AccountByID(t.Context(), acct.ID)
	if err != nil || claimed.Email != "owner@example.com" || isUnclaimedAccount(claimed) {
		t.Fatalf("claimed account = %+v (err %v)", claimed, err)
	}
	row, err := store.(state.UnclaimedAccountStore).UnclaimedAccountByID(t.Context(), acct.ID)
	if err != nil || row.ClaimedAt == nil {
		t.Fatalf("unclaimed row = %+v (err %v)", row, err)
	}
	if apps, err := store.ListApps(t.Context(), acct.ID); err != nil || len(apps) != 1 {
		t.Fatalf("apps after claim = %v (err %v)", apps, err)
	}
	if rec := claimBearer(h, http.MethodGet, "/v1/apps", sess.APIKey, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous key after claim = %d, want 401", rec.Code)
	}
	// The used link and the claim page are gone.
	if rec := claimForm(h, claimConfirmPath, url.Values{"token": {token}}); rec.Code == http.StatusSeeOther {
		t.Errorf("second confirm redirected again")
	}
	gone := httptest.NewRecorder()
	h.ServeHTTP(gone, httptest.NewRequest(http.MethodGet, newPath, nil))
	if gone.Code != http.StatusGone {
		t.Errorf("claim page after claim = %d, want 410", gone.Code)
	}
}

// An address that already has an account gets an explanation, never a
// confirmation link, and the answer does not say which addresses exist.
func TestClaimWithTakenAddressMailsExplanation(t *testing.T) {
	srv, mailer, store := v1AuthTestHarness(t)
	h := srv.handler()
	if _, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "taken@example.com", Plan: api.PlanFree}); err != nil {
		t.Fatal(err)
	}
	_, sess := anonymousSession(t, h, "203.0.113.11:5000")
	path := sess.ClaimURL[strings.Index(sess.ClaimURL, "/claim/"):]
	rec := claimForm(h, path, url.Values{"email": {"taken@example.com"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your email") {
		t.Fatalf("claim post = %d: %s", rec.Code, rec.Body.String())
	}
	srv.resetMail.wait()
	for _, msg := range mailer.snapshot() {
		if strings.Contains(msg.TextBody, "/claim/confirm") {
			t.Fatalf("taken address got a confirmation link: %s", msg.TextBody)
		}
	}
	found := false
	for _, msg := range mailer.snapshot() {
		found = found || (msg.To[0] == "taken@example.com" && strings.Contains(msg.TextBody, "already has a Gregale account"))
	}
	if !found {
		t.Fatalf("no explanation mailed: %+v", mailer.snapshot())
	}
}

// Anonymous sessions are capped per address; the sign-up cap of the same
// address is untouched.
func TestAnonymousSessionsCappedPerAddress(t *testing.T) {
	srv, _, _ := v1AuthTestHarness(t)
	h := srv.handler()
	for i := range api.UnclaimedAccountsPerAddressPerDayDefault {
		if rec, _ := anonymousSession(t, h, "203.0.113.20:5000"); rec.Code != http.StatusCreated {
			t.Fatalf("session %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	rec, _ := anonymousSession(t, h, "203.0.113.20:5000")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), api.CodeAnonymousDeploysUnavailable) {
		t.Fatalf("over the cap = %d: %s", rec.Code, rec.Body.String())
	}
	if !srv.signups.allow("203.0.113.20") {
		t.Fatal("anonymous sessions used the address's sign-up allowance")
	}
	bad := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/anonymous/sessions", strings.NewReader(`{}`))
	req.RemoteAddr = "203.0.113.21:5000"
	h.ServeHTTP(bad, req)
	if bad.Code != http.StatusBadRequest {
		t.Errorf("without accept_terms = %d, want 400", bad.Code)
	}
}

// Past its expiry an unclaimed account is scheduled for deletion; a claimed
// one and one still inside its window are left alone.
func TestUnclaimedAccountsExpire(t *testing.T) {
	srv, _, store := v1AuthTestHarness(t)
	h := srv.handler()
	_, first := anonymousSession(t, h, "203.0.113.30:5000")
	_, second := anonymousSession(t, h, "203.0.113.31:5000")
	if n := srv.expireUnclaimedAccounts(t.Context()); n != 0 {
		t.Fatalf("early sweep ended %d", n)
	}
	defer func(prev func() time.Time) { timeNow = prev }(timeNow)
	later := time.Now().Add(api.UnclaimedDeployTTLDefault + time.Hour)
	timeNow = func() time.Time { return later }
	if _, err := store.(state.UnclaimedAccountStore).MarkUnclaimedAccountClaimed(t.Context(), second.AccountID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := srv.expireUnclaimedAccounts(t.Context()); n != 1 {
		t.Fatalf("sweep ended %d, want 1", n)
	}
	if a, _ := store.AccountByID(t.Context(), first.AccountID); a.Status != state.AccountDeletedPending {
		t.Errorf("expired account status = %s, want deleted_pending", a.Status)
	}
	if a, _ := store.AccountByID(t.Context(), second.AccountID); a.Status != state.AccountActive {
		t.Errorf("claimed account status = %s, want active", a.Status)
	}
	if n := srv.expireUnclaimedAccounts(t.Context()); n != 0 {
		t.Errorf("second sweep ended %d more", n)
	}
	// The claim page of an expired account is gone.
	path := first.ClaimURL[strings.Index(first.ClaimURL, "/claim/"):]
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusGone {
		t.Errorf("expired claim page = %d, want 410", rec.Code)
	}
}

func TestUnclaimedRouteAllowed(t *testing.T) {
	cases := []struct {
		method, pattern string
		want            bool
	}{
		{"GET", "GET /v1/apps", true},
		{"POST", "POST /v1/apps", true},
		{"POST", "POST /v1/apps/{slug}/deployments", true},
		{"POST", "POST /v1/uploads", true},
		{"PUT", "PUT /v1/apps/{slug}/env/{key}", true},
		{"GET", "GET /v1/deployments/{id}/logs", true},
		{"GET", "GET /v1/account", true},
		{"POST", "POST /v1/account/claim-link", true},
		{"DELETE", "DELETE /v1/account", false},
		{"POST", "POST /v1/account/keys", false},
		{"POST", "POST /v1/apps/{slug}/domains", false},
		{"POST", "POST /v1/apps/{slug}/postgres", false},
		{"POST", "POST /v1/apps/{slug}/buckets", false},
		{"POST", "POST /v1/apps/{slug}/crons", false},
		{"POST", "POST /v1/keys", false},
		{"GET", "GET /v1/keys", false},
		{"POST", "POST /v1/orgs", false},
		{"POST", "POST /v1/billing/checkout", false},
		{"POST", "POST /v1/sandboxes", false},
		{"POST", "POST /v1/apps/{slug}/static-egress-ip", false},
	}
	for _, c := range cases {
		if got := unclaimedRouteAllowed(c.method, c.pattern, ""); got != c.want {
			t.Errorf("%s = %v, want %v", c.pattern, got, c.want)
		}
	}
}
