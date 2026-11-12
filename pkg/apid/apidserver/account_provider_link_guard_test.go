// adr: 20261111090000
package apidserver_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// linkForm posts the account page's "Link GitHub" form.
func linkForm(t *testing.T, srv *apidserver.Server, cookie *http.Cookie, accountID string) string {
	t.Helper()
	tok, err := middleware.IssueForAuthenticatedNamed(*srv.Sessions(), "account_provider_link", accountID, "faas_csrf_account_provider_link")
	if err != nil {
		t.Fatal(err)
	}
	rec := flowForm(srv.Handler(), "/dashboard/account/providers/github/link", url.Values{"csrf_token": {tok}}, cookie,
		&http.Cookie{Name: "faas_csrf_account_provider_link", Value: tok})
	return rec.Header().Get("Location")
}

// Linking a provider is a sign-in method change: an unverified account (a
// possible squatter) cannot start one, and a stale session signs in again
// first.
func TestProviderLinkNeedsVerifiedEmailAndPresence(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	mgr := *srv.Sessions()

	squatter, _ := flowAccountKey(t, store, "squatted@example.com", false)
	if loc := linkForm(t, srv, flowSession(t, store, mgr, squatter.ID), squatter.ID); !strings.Contains(loc, "account_error=verify_email") {
		t.Errorf("unverified account starting a link = %q, want verify_email", loc)
	}

	owner, _ := flowAccountKey(t, store, "linking-owner@example.com", true)
	session := flowSession(t, store, mgr, owner.ID)
	if loc := linkForm(t, srv, session, owner.ID); strings.Contains(loc, "verify_email") || strings.Contains(loc, "reauth") {
		t.Errorf("verified account with a recent sign-in = %q, want the link to start", loc)
	}
	apidserver.AgeSignInsForTest(t, api.RecentSignInWindow+time.Minute)
	if loc := linkForm(t, srv, session, owner.ID); loc != "/login?reauth=1&next=/dashboard/account" {
		t.Errorf("stale session starting a link = %q, want the sign-in page", loc)
	}
}

// A provider sign-in proves the provider's address, not the account's: it
// verifies the account only when the two match.
func TestProviderSignInVerifiesOnlyItsOwnAddress(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	acct, _ := flowAccountKey(t, store, "victim@example.com", false)
	got, err := srv.VerifyOAuthAccountEmail(t.Context(), acct, "squatter@example.net")
	if err != nil || got.EmailVerified() {
		t.Fatalf("a provider under another address verified the account: %+v %v", got, err)
	}
	if fresh, err := store.AccountByID(t.Context(), acct.ID); err != nil || fresh.EmailVerified() {
		t.Fatalf("account verified after a foreign provider sign-in: %v", err)
	}
	got, err = srv.VerifyOAuthAccountEmail(t.Context(), acct, "Victim@Example.com")
	if err != nil || !got.EmailVerified() {
		t.Fatalf("the provider proving the account's own address did not verify it: %+v %v", got, err)
	}
}

// Recovery removes provider sign-ins under other addresses, keeps the ones
// under the account's own, and mails each removal.
func TestRecoveryRemovesForeignProviderLinks(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	acct, _ := flowAccountKey(t, store, "recovered@example.com", true)
	if err := store.UpsertOAuthLink(t.Context(), acct.ID, "google", "google-own", acct.Email, true); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertOAuthLink(t.Context(), acct.ID, "github", "github-thief", "thief@example.net", true); err != nil {
		t.Fatal(err)
	}
	srv.RemoveForeignProviderLinks(t.Context(), acct.ID, "password_reset")

	flow := store.(state.AccountFlowStore)
	links, err := flow.ListOAuthLinks(t.Context(), acct.ID)
	if err != nil || len(links) != 1 || links[0].Provider != "google" {
		t.Fatalf("links after recovery = %+v %v, want only the account's own Google", links, err)
	}
	if unlinked, err := flow.OAuthProviderUnlinked(t.Context(), acct.ID, "github"); err != nil || !unlinked {
		t.Errorf("removed provider not recorded as unlinked (%v %v): it would re-link by email", unlinked, err)
	}
	srv.WaitPasswordResetMail()
	removed := false
	for _, body := range flowMailsTo(mailer, acct.Email) {
		removed = removed || strings.Contains(body, `"Continue with GitHub" was removed`)
	}
	if !removed {
		t.Errorf("no removal notice; mails to the account: %v", flowMailsTo(mailer, acct.Email))
	}
}
