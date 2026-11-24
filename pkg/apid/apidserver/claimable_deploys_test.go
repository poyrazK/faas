package apidserver_test

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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/claim"
	"github.com/onebox-faas/faas/pkg/state"
)

// Claimable deploys (ADR-20261124080000), end to end through apid's routes.

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

func claimGet(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func claimPath(claimURL string) string { return claimURL[strings.Index(claimURL, claim.PagePath+"/"):] }

var claimConfirmLink = regexp.MustCompile(`/claim/confirm\?token=([A-Za-z0-9_-]+)`)

// requestClaim posts an address on the claim page and returns the mailed
// confirmation token, or "" when none was mailed.
func requestClaim(t *testing.T, srv *apidserver.Server, mailer *apidserver.RecordingMailer, path, email string) string {
	t.Helper()
	h := srv.Handler()
	if rec := claimForm(h, path, url.Values{"email": {email}}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your email") {
		t.Fatalf("claim post = %d: %s", rec.Code, rec.Body.String())
	}
	srv.WaitPasswordResetMail()
	token := ""
	for _, msg := range mailer.Snapshot() {
		if len(msg.To) == 1 && msg.To[0] == email {
			if m := claimConfirmLink.FindStringSubmatch(msg.TextBody); m != nil {
				token = m[1]
			}
		}
	}
	return token
}

func unclaimedRow(t *testing.T, store state.Store, accountID string) state.UnclaimedAccount {
	t.Helper()
	row, err := store.(state.UnclaimedAccountStore).UnclaimedAccountByID(t.Context(), accountID)
	if err != nil {
		t.Fatalf("unclaimed row: %v", err)
	}
	return row
}

// An agent deploys with no account; the person who opens the claim link and
// confirms an address owns the same account, with its apps, and the agent's
// anonymous key stops working.
func TestAnonymousDeployIsClaimedInPlace(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()

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
	if err != nil || !claim.IsUnclaimed(acct) || !acct.EmailVerified() || acct.Plan != api.PlanFree {
		t.Fatalf("account = %+v (err %v)", acct, err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "agent-app", Status: state.AppActive, RAMMB: 256}); err != nil {
		t.Fatal(err)
	}

	// The key deploys and reads; it may not reach what needs a claim,
	// including credentials and resources under the app itself.
	if rec := claimBearer(h, http.MethodGet, "/v1/apps", sess.APIKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/apps = %d: %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{
		"/v1/keys", "/v1/domains", "/v1/orgs",
		"/v1/apps/agent-app/ai-access-tokens", "/v1/apps/agent-app/log-drains",
	} {
		if rec := claimBearer(h, http.MethodPost, path, sess.APIKey, `{}`); rec.Code == http.StatusOK || rec.Code == http.StatusCreated ||
			(rec.Code == http.StatusForbidden && !strings.Contains(rec.Body.String(), api.CodeClaimRequired)) {
			t.Errorf("POST %s = %d %s, want it refused", path, rec.Code, rec.Body.String())
		}
	}
	// App settings a deploy applies pass; the rest of the app PATCH waits
	// for a claim.
	if rec := claimBearer(h, http.MethodPatch, "/v1/apps/agent-app", sess.APIKey, `{"require_authn":false}`); rec.Code == http.StatusForbidden {
		t.Errorf("PATCH require_authn = %d %s, want it allowed", rec.Code, rec.Body.String())
	}
	if rec := claimBearer(h, http.MethodPatch, "/v1/apps/agent-app", sess.APIKey, `{"require_authn":false,"egress_allowlist":["198.51.100.0/24"]}`); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), api.CodeClaimRequired) || !strings.Contains(rec.Body.String(), "egress_allowlist") {
		t.Errorf("PATCH egress_allowlist = %d %s, want claim_required naming the field", rec.Code, rec.Body.String())
	}
	// Creating an app takes the same settings; the rest waits for a claim.
	if rec := claimBearer(h, http.MethodPost, "/v1/apps", sess.APIKey, `{"slug":"second-app","warm_pool_size":5}`); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "warm_pool_size") {
		t.Errorf("POST /v1/apps warm_pool_size = %d %s, want claim_required naming the field", rec.Code, rec.Body.String())
	}
	// A WebSocket upgrade under a read root acts rather than reads.
	upgrade := httptest.NewRequest(http.MethodGet, "/v1/apps/agent-app", nil)
	upgrade.Header.Set("Authorization", "Bearer "+sess.APIKey)
	upgrade.Header.Set("Connection", "Upgrade")
	upgrade.Header.Set("Upgrade", "websocket")
	upRec := httptest.NewRecorder()
	h.ServeHTTP(upRec, upgrade)
	if upRec.Code != http.StatusForbidden || !strings.Contains(upRec.Body.String(), api.CodeClaimRequired) {
		t.Errorf("GET with Upgrade = %d %s, want claim_required", upRec.Code, upRec.Body.String())
	}
	// Account-level routes behind the plain auth chain are closed too.
	for _, route := range []struct{ method, path string }{
		{http.MethodDelete, "/v1/account"},
		{http.MethodPost, "/v1/account/restore"},
		{http.MethodGet, "/v1/auth/sessions"},
	} {
		if rec := claimBearer(h, route.method, route.path, sess.APIKey, `{}`); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeClaimRequired) {
			t.Errorf("%s %s = %d %s, want claim_required", route.method, route.path, rec.Code, rec.Body.String())
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
	if rec := claimGet(h, claimPath(sess.ClaimURL)); rec.Code != http.StatusGone {
		t.Errorf("replaced claim link = %d, want 410", rec.Code)
	}
	newPath := claimPath(link.ClaimURL)
	if page := claimGet(h, newPath); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "agent-app") {
		t.Fatalf("claim page = %d: %s", page.Code, page.Body.String())
	}

	// The claimer confirms an address.
	token := requestClaim(t, srv, mailer, newPath, "owner@example.com")
	if token == "" {
		t.Fatalf("no claim confirmation mailed: %+v", mailer.Snapshot())
	}
	if rec := claimGet(h, claim.ConfirmPath+"?token="+token); rec.Code != http.StatusOK {
		t.Fatalf("confirm page = %d", rec.Code)
	}
	rec = claimForm(h, claim.ConfirmPath, url.Values{"token": {token}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/auth/verify?token=") {
		t.Fatalf("confirm = %d location %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	claimed, err := store.AccountByID(t.Context(), acct.ID)
	if err != nil || claimed.Email != "owner@example.com" || claim.IsUnclaimed(claimed) {
		t.Fatalf("claimed account = %+v (err %v)", claimed, err)
	}
	if row := unclaimedRow(t, store, acct.ID); row.ClaimedAt == nil {
		t.Fatalf("unclaimed row = %+v", row)
	}
	if apps, err := store.ListApps(t.Context(), acct.ID); err != nil || len(apps) != 1 {
		t.Fatalf("apps after claim = %v (err %v)", apps, err)
	}
	if rec := claimBearer(h, http.MethodGet, "/v1/apps", sess.APIKey, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous key after claim = %d, want 401", rec.Code)
	}
	if rec := claimForm(h, claim.ConfirmPath, url.Values{"token": {token}}); rec.Code == http.StatusSeeOther {
		t.Errorf("second confirm redirected again")
	}
	if rec := claimGet(h, newPath); rec.Code != http.StatusGone {
		t.Errorf("claim page after claim = %d, want 410", rec.Code)
	}
}

// An address that already has an account gets an explanation, never a
// confirmation link, and the answer does not say which addresses exist.
func TestClaimWithTakenAddressMailsExplanation(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	if _, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "taken@example.com", Plan: api.PlanFree}); err != nil {
		t.Fatal(err)
	}
	_, sess := anonymousSession(t, srv.Handler(), "203.0.113.11:5000")
	if token := requestClaim(t, srv, mailer, claimPath(sess.ClaimURL), "taken@example.com"); token != "" {
		t.Fatalf("taken address got a confirmation link")
	}
	found := false
	for _, msg := range mailer.Snapshot() {
		found = found || (msg.To[0] == "taken@example.com" && strings.Contains(msg.TextBody, "already has a Gregale account"))
	}
	if !found {
		t.Fatalf("no explanation mailed: %+v", mailer.Snapshot())
	}
}

// Anonymous sessions are capped per address in the store, so the cap holds
// across apids; a body without accept_terms is refused.
func TestAnonymousSessionsCappedPerAddress(t *testing.T) {
	srv, _, _ := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	for i := range api.UnclaimedAccountsPerAddressPerDayDefault {
		if rec, _ := anonymousSession(t, h, "203.0.113.20:5000"); rec.Code != http.StatusCreated {
			t.Fatalf("session %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	rec, _ := anonymousSession(t, h, "203.0.113.20:5000")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), api.CodeAnonymousDeploysUnavailable) || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("over the cap = %d %v: %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil || prob.Limit == nil || prob.Observed == nil ||
		*prob.Limit != int64(api.UnclaimedAccountsPerAddressPerDayDefault) || *prob.Observed < *prob.Limit {
		t.Fatalf("over the cap problem = %+v (err %v), want limit and observed", prob, err)
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
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, first := anonymousSession(t, h, "203.0.113.30:5000")
	_, second := anonymousSession(t, h, "203.0.113.31:5000")
	svc := srv.Claim()
	if n := svc.ExpireOnce(t.Context()); n != 0 {
		t.Fatalf("early sweep ended %d", n)
	}
	later := time.Now().Add(api.UnclaimedDeployTTLDefault + time.Hour)
	svc.Now = func() time.Time { return later }
	if _, err := store.(state.UnclaimedAccountStore).MarkUnclaimedAccountClaimed(t.Context(), second.AccountID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := svc.ExpireOnce(t.Context()); n != 1 {
		t.Fatalf("sweep ended %d, want 1", n)
	}
	if a, _ := store.AccountByID(t.Context(), first.AccountID); a.Status != state.AccountDeletedPending {
		t.Errorf("expired account status = %s, want deleted_pending", a.Status)
	}
	if row := unclaimedRow(t, store, first.AccountID); row.ExpiredAt == nil {
		t.Errorf("expired row = %+v", row)
	}
	if a, _ := store.AccountByID(t.Context(), second.AccountID); a.Status != state.AccountActive {
		t.Errorf("claimed account status = %s, want active", a.Status)
	}
	if n := svc.ExpireOnce(t.Context()); n != 0 {
		t.Errorf("second sweep ended %d more", n)
	}
	if rec := claimGet(h, claimPath(first.ClaimURL)); rec.Code != http.StatusGone {
		t.Errorf("expired claim page = %d, want 410", rec.Code)
	}
}

// A confirmation mailed through a claim link stops working when the agent
// replaces the link, so a leaked link cannot be used to take the app.
func TestNewClaimLinkCancelsMailedConfirmation(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, sess := anonymousSession(t, h, "203.0.113.40:5000")
	token := requestClaim(t, srv, mailer, claimPath(sess.ClaimURL), "attacker@example.net")
	if token == "" {
		t.Fatal("no confirmation mailed")
	}
	if rec := claimBearer(h, http.MethodPost, "/v1/account/claim-link", sess.APIKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("claim-link = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := claimForm(h, claim.ConfirmPath, url.Values{"token": {token}}); rec.Code == http.StatusSeeOther {
		t.Fatalf("confirmation from the replaced link still claimed the app")
	}
	if a, _ := store.AccountByID(t.Context(), sess.AccountID); !claim.IsUnclaimed(a) {
		t.Fatalf("account moved to %s", a.Email)
	}
}

// A confirmation cannot land once the claim window ended: expiry drops it.
func TestClaimConfirmationEndsWithTheWindow(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, sess := anonymousSession(t, h, "203.0.113.50:5000")
	token := requestClaim(t, srv, mailer, claimPath(sess.ClaimURL), "late@example.com")
	if token == "" {
		t.Fatal("no confirmation mailed")
	}
	svc := srv.Claim()
	later := sess.ExpiresAt.Add(time.Minute)
	svc.Now = func() time.Time { return later }
	if n := svc.ExpireOnce(t.Context()); n != 1 {
		t.Fatalf("sweep ended %d, want 1", n)
	}
	if rec := claimForm(h, claim.ConfirmPath, url.Values{"token": {token}}); rec.Code == http.StatusSeeOther {
		t.Fatalf("confirmation after the window claimed the app")
	}
	if a, _ := store.AccountByID(t.Context(), sess.AccountID); a.Email == "late@example.com" {
		t.Fatalf("address moved onto an expired account")
	}
}

// When the address moved but the claim was not recorded, the anonymous key
// is refused at once and the sweep finishes the claim: keys revoked, row
// claimed, account kept.
func TestUnrecordedClaimIsFinishedBySweep(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, sess := anonymousSession(t, h, "203.0.113.60:5000")
	flow := store.(state.AccountFlowStore)
	hash := api.HashToken([]byte("0123456789abcdef0123456789abcdef"))
	if err := flow.IssueEmailChange(t.Context(), hash, sess.AccountID, "owner2@example.com", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.ConsumeEmailChange(t.Context(), hash); err != nil {
		t.Fatal(err)
	}
	if rec := claimBearer(h, http.MethodGet, "/v1/apps", sess.APIKey, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous key on a claimed account = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	// The new owner makes a key of their own before the sweep runs; the
	// late finish must leave it alone.
	owned, err := store.CreateAPIKey(t.Context(), sess.AccountID, api.HashToken([]byte("owner-key")), "laptop", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	svc := srv.Claim()
	later := sess.ExpiresAt.Add(time.Minute)
	svc.Now = func() time.Time { return later }
	if n := svc.ExpireOnce(t.Context()); n != 0 {
		t.Fatalf("sweep expired %d claimed accounts", n)
	}
	if row := unclaimedRow(t, store, sess.AccountID); row.ClaimedAt == nil || row.ExpiredAt != nil {
		t.Fatalf("row = %+v, want claimed", row)
	}
	if keys, err := store.ListAPIKeys(t.Context(), sess.AccountID); err != nil || len(keys) != 1 || keys[0].ID != owned.ID {
		t.Fatalf("keys after finishing the claim = %v (err %v), want only the owner's", keys, err)
	}
	if a, _ := store.AccountByID(t.Context(), sess.AccountID); a.Status != state.AccountActive {
		t.Fatalf("claimed account status = %s", a.Status)
	}
}

// After the window ends, a claim-link request answers 410 claim_expired.
func TestClaimLinkAfterWindowIsGone(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, sess := anonymousSession(t, h, "203.0.113.70:5000")
	if _, err := store.(state.UnclaimedAccountStore).EndUnclaimedWindow(t.Context(), sess.AccountID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	// The window ends no earlier than just after creation; look a moment later.
	srv.Claim().Now = func() time.Time { return time.Now().Add(time.Second) }
	rec := claimBearer(h, http.MethodPost, "/v1/account/claim-link", sess.APIKey, "")
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), api.CodeClaimExpired) {
		t.Fatalf("claim-link after the window = %d %s, want 410 claim_expired", rec.Code, rec.Body.String())
	}
}

// A browser posting the claim form from the page may send Origin: null;
// with Sec-Fetch-Site: same-origin that is this host's own page.
func TestClaimFormAcceptsSameOriginNullOrigin(t *testing.T) {
	srv, _, _ := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, sess := anonymousSession(t, h, "203.0.113.80:5000")
	post := func(site string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, claimPath(sess.ClaimURL), strings.NewReader(url.Values{"email": {"browser@example.com"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		req.RemoteAddr = "198.51.100.8:4000"
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("same-origin"); rec.Code != http.StatusOK {
		t.Fatalf("same-origin null Origin = %d %s, want 200", rec.Code, rec.Body.String())
	}
	for _, site := range []string{"", "same-site", "cross-site"} {
		if rec := post(site); rec.Code != http.StatusForbidden {
			t.Errorf("null Origin with Sec-Fetch-Site %q = %d, want 403", site, rec.Code)
		}
	}
}
