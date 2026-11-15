package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Moving an account off an address removes the Google or GitHub sign-ins made
// under it: whoever still controls that identity (an old employer, say) could
// otherwise keep signing in (F11-1, ADR-20261115100000). Links under any other
// address stay.
func TestEmailChangeRemovesOldAddressProviderLinks(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	acct, _ := flowAccountKey(t, store, "me@personal.example", true)
	if err := store.UpsertOAuthLink(t.Context(), acct.ID, "google", "google-work", "me@work.example", true); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertOAuthLink(t.Context(), acct.ID, "github", "github-mine", "me@other.example", true); err != nil {
		t.Fatal(err)
	}
	srv.RemoveOldAddressProviderLinks(t.Context(), acct, "Me@Work.example")

	flow := store.(state.AccountFlowStore)
	links, err := flow.ListOAuthLinks(t.Context(), acct.ID)
	if err != nil || len(links) != 1 || links[0].Provider != "github" {
		t.Fatalf("links after the email change = %+v %v, want only GitHub", links, err)
	}
	if unlinked, err := flow.OAuthProviderUnlinked(t.Context(), acct.ID, "google"); err != nil || !unlinked {
		t.Errorf("removed provider not recorded as unlinked (%v %v): it would re-link by email", unlinked, err)
	}
	waitForMail(t, mailer, acct.Email, "Continue with Google")
}
