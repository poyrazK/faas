package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Sixty days after signup an account nobody verified, which never created an
// app and has no billing customer, is scheduled for deletion with the usual
// restore window; one with an app, a verified one, and a younger one are
// left alone (ADR 20261110120000).
func TestUnverifiedAccountsExpire(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	abandoned, _ := flowAccountKey(t, store, "abandoned@example.com", false)
	withApp, _ := flowAccountKey(t, store, "has-app@example.com", false)
	verified, _ := flowAccountKey(t, store, "verified-old@example.com", true)
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: withApp.ID, Slug: "kept-app", Status: state.AppActive, RAMMB: 256}); err != nil {
		t.Fatal(err)
	}

	start := abandoned.CreatedAt
	if start.Before(api.EmailVerificationEnforcedFrom) {
		start = api.EmailVerificationEnforcedFrom
	}
	srv.SetVerificationClock(func() time.Time { return start.Add(api.UnverifiedAccountDeleteAfter - time.Hour) })
	if n := srv.ExpireUnverifiedAccounts(t.Context()); n != 0 {
		t.Fatalf("before 60 days scheduled %d deletions, want 0", n)
	}

	srv.SetVerificationClock(func() time.Time { return start.Add(api.UnverifiedAccountDeleteAfter + time.Hour) })
	if n := srv.ExpireUnverifiedAccounts(t.Context()); n != 1 {
		t.Fatalf("after 60 days scheduled %d deletions, want 1", n)
	}
	want := map[string]state.AccountStatus{abandoned.ID: state.AccountDeletedPending, withApp.ID: state.AccountActive, verified.ID: state.AccountActive}
	for id, status := range want {
		acct, err := store.AccountByID(t.Context(), id)
		if err != nil || acct.Status != status {
			t.Errorf("account %s status = %s (err %v), want %s", acct.Email, acct.Status, err, status)
		}
	}
	if n := srv.ExpireUnverifiedAccounts(t.Context()); n != 0 {
		t.Errorf("a second sweep scheduled %d more, want 0", n)
	}
}
