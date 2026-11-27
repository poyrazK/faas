package apidserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod hunt 2026-11-21: adding a domain still bound to the account's own
// deleted app answered "Domain taken" / "state: conflict" and nothing more.
// The conflict names the holding app and how to free the domain; another
// account's claim stays undescribed.
func TestDomainConflictNamesTheOwnersHoldingApp(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	app, err := mem.CreateApp(ctx, state.App{AccountID: "acct-1", Slug: "old-web"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.CreateCustomDomain(ctx, "shop.example.test", app.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	store := mem

	hint := apidserver.OwnDomainHolderHint(ctx, store, "acct-1", "shop.example.test")
	if !strings.Contains(hint, "your app old-web") || !strings.Contains(hint, "gregale domain delete shop.example.test") {
		t.Fatalf("live holder hint = %q", hint)
	}
	if _, err := mem.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	hint = apidserver.OwnDomainHolderHint(ctx, store, "acct-1", "shop.example.test")
	if !strings.Contains(hint, "deleted app old-web") || !strings.Contains(hint, "gregale app purge old-web") {
		t.Fatalf("deleted holder hint = %q", hint)
	}
	if hint := apidserver.OwnDomainHolderHint(ctx, store, "acct-2", "shop.example.test"); hint != "" {
		t.Fatalf("another account's claim was described: %q", hint)
	}
}
