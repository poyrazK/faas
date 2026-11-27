package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/claim"
	"github.com/onebox-faas/faas/pkg/state"
)

// The whole claimable-deploy flow through apid's handlers on Postgres: an
// anonymous session, an app, a fresh claim link, the claim, the anonymous
// key ending, and the sweep ending a second account whose window closed
// (ADR-20261124080000).
func TestClaimableDeployFlowOnPostgres(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthPGTestHarness(t)
	h := srv.Handler()

	rec, sess := anonymousSession(t, h, "203.0.113.90:5000")
	if rec.Code != http.StatusCreated {
		t.Fatalf("anonymous session = %d: %s", rec.Code, rec.Body.String())
	}
	acct, err := store.AccountByID(t.Context(), sess.AccountID)
	if err != nil || !claim.IsUnclaimed(acct) {
		t.Fatalf("account = %+v (err %v)", acct, err)
	}

	// The anonymous key creates its app, but not with settings that wait
	// for a claim.
	if rec := claimBearer(h, http.MethodPost, "/v1/apps", sess.APIKey, `{"slug":"pg-agent-app","warm_pool_size":2}`); rec.Code != http.StatusForbidden {
		t.Fatalf("create with warm_pool_size = %d %s, want 403", rec.Code, rec.Body.String())
	}
	// Its app takes the account's suffix, so it never holds a short name.
	appSlug := api.AnonymousAppSlug(sess.AccountID, "pg-agent-app")
	if rec := claimBearer(h, http.MethodPost, "/v1/apps", sess.APIKey, `{"slug":"pg-agent-app","require_authn":false}`); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), appSlug) {
		t.Fatalf("create without the suffix = %d %s, want 403 naming %s", rec.Code, rec.Body.String(), appSlug)
	}
	if rec := claimBearer(h, http.MethodPost, "/v1/apps", sess.APIKey, `{"slug":"`+appSlug+`","require_authn":false}`); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create app = %d %s", rec.Code, rec.Body.String())
	}
	if rec := claimBearer(h, http.MethodGet, "/v1/keys", sess.APIKey, ""); rec.Code != http.StatusForbidden {
		t.Errorf("GET /v1/keys = %d, want 403", rec.Code)
	}

	// A fresh link; the first one stops working.
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
	path := claimPath(link.ClaimURL)
	if page := claimGet(h, path); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), appSlug) {
		t.Fatalf("claim page = %d: %s", page.Code, page.Body.String())
	}

	// The person confirms an address and owns the account.
	token := requestClaim(t, srv, mailer, path, "pg-owner@example.com")
	if token == "" {
		t.Fatalf("no confirmation mailed: %+v", mailer.Snapshot())
	}
	rec = claimForm(h, claim.ConfirmPath, url.Values{"token": {token}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d: %s", rec.Code, rec.Body.String())
	}
	claimed, err := store.AccountByID(t.Context(), acct.ID)
	if err != nil || claimed.Email != "pg-owner@example.com" || claim.IsUnclaimed(claimed) {
		t.Fatalf("claimed account = %+v (err %v)", claimed, err)
	}
	if row := unclaimedRow(t, store, acct.ID); row.ClaimedAt == nil {
		t.Fatalf("row = %+v, want claimed", row)
	}
	if apps, err := store.ListApps(t.Context(), acct.ID); err != nil || len(apps) != 1 {
		t.Fatalf("apps after claim = %v (err %v)", apps, err)
	}
	if keys, err := store.ListAPIKeys(t.Context(), acct.ID); err != nil || len(keys) != 0 {
		t.Fatalf("keys after claim = %v (err %v), want the anonymous key revoked", keys, err)
	}
	if rec := claimBearer(h, http.MethodGet, "/v1/apps", sess.APIKey, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous key after claim = %d, want 401", rec.Code)
	}

	// A second anonymous account whose window ended is ended by the sweep;
	// the claimed one is left alone.
	_, other := anonymousSession(t, h, "203.0.113.91:5000")
	if _, err := store.(state.UnclaimedAccountStore).EndUnclaimedWindow(t.Context(), other.AccountID, time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	svc := srv.Claim()
	svc.Now = func() time.Time { return time.Now().Add(time.Second) }
	if n := svc.ExpireOnce(t.Context()); n != 1 {
		t.Fatalf("sweep ended %d, want 1", n)
	}
	if a, _ := store.AccountByID(t.Context(), other.AccountID); a.Status != state.AccountDeletedPending {
		t.Errorf("expired account status = %s, want deleted_pending", a.Status)
	}
	if a, _ := store.AccountByID(t.Context(), acct.ID); a.Status != state.AccountActive {
		t.Errorf("claimed account status = %s, want active", a.Status)
	}
}
