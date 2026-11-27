package apidserver

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Observe audit 2026-11-21 (finding 17): request rows, HTTP logs and
// invocations are stored under each app's own account, so the account-wide
// trace lookup reads under every owner of the caller's visible apps, not
// only under the caller's id.
func TestTraceOwnerAccountsCoversTeammateApps(t *testing.T) {
	apps := []state.App{
		{ID: "a1", AccountID: "me"},
		{ID: "a2", AccountID: "teammate"},
		{ID: "a3", AccountID: "teammate"},
		{ID: "a4"},
	}
	owners := traceOwnerAccounts("org-or-me", apps)
	if !slices.Equal(owners, []string{"org-or-me", "me", "teammate"}) {
		t.Fatalf("owners = %v", owners)
	}
	ids := func(apps []state.App) []string {
		var out []string
		for _, a := range apps {
			out = append(out, a.ID)
		}
		return out
	}
	if got := ids(appsOwnedBy(apps, "teammate", "org-or-me")); !slices.Equal(got, []string{"a2", "a3"}) {
		t.Fatalf("teammate apps = %v", got)
	}
	if got := ids(appsOwnedBy(apps, "org-or-me", "org-or-me")); !slices.Equal(got, []string{"a4"}) {
		t.Fatalf("caller's read covers apps without an account: got %v", got)
	}
}
