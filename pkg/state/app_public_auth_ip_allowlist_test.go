package state_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"net/netip"
	"reflect"
	"testing"
)

// TestUpdateApp_WithPublicAuthIPAllowlist pins the partial-update
// semantics of UpdateAppParams.PublicAuthIPAllowlist +
// SetPublicAuthIPAllowlist (ADR-118). Mirrors the egress allowlist
// pattern at app_egress_allowlist_test.go:30 (TestUpdateApp_WithEgressAllowlist)
// verbatim — the same nil-pointer + Set-bit convention that distinguishes
// "don't touch" from "explicit empty" applies to both columns.
//
// Cases:
//   - SetPublicAuthIPAllowlist=false (param nil)  → column unchanged
//   - SetPublicAuthIPAllowlist=true with non-empty slice → column
//     replaced atomically
//   - SetPublicAuthIPAllowlist=true with non-nil EMPTY slice → column
//     becomes empty (the canonical "no rule" state; gateway gate
//     returns false on empty list + non-ip_allowlist mode)
//   - AppByID deserialises the column back to []netip.Prefix
//
// Like the egress hermetic suite, MemStore is enough here — pgstore
// tests are gated behind //go:build pg and the e2e migration test
// at migrations/00308_apps_public_auth_ip_allowlist_test.go pins
// the column shape + CHECK + trigger.
func TestUpdateApp_WithPublicAuthIPAllowlist(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "alice@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "api"})
	if err != nil {
		t.Fatal(err)
	}

	// Default is empty — no allowlist.
	if got, err := store.AppByID(ctx, app.ID); err != nil {
		t.Fatal(err)
	} else if len(got.PublicAuthIPAllowlist) != 0 {
		t.Errorf("default PublicAuthIPAllowlist = %v, want empty", got.PublicAuthIPAllowlist)
	}

	// PATCH that pins a 3-entry list.
	three := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	updated, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		PublicAuthIPAllowlist:    &three,
		SetPublicAuthIPAllowlist: true,
	})
	if err != nil {
		t.Fatalf("set 3-entry allowlist: %v", err)
	}
	if !reflect.DeepEqual(updated.PublicAuthIPAllowlist, three) {
		t.Errorf("after Set non-empty:\n got  %v\n want %v", updated.PublicAuthIPAllowlist, three)
	}

	// Re-read via AppByID — make sure the deserialise path keeps it.
	readBack, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readBack.PublicAuthIPAllowlist, three) {
		t.Errorf("AppByID round-trip:\n got  %v\n want %v", readBack.PublicAuthIPAllowlist, three)
	}

	// PATCH with unset (no SetPublicAuthIPAllowlist) — column stays at 3.
	updated, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{})
	if err != nil {
		t.Fatalf("unset update: %v", err)
	}
	if !reflect.DeepEqual(updated.PublicAuthIPAllowlist, three) {
		t.Errorf("unset PublicAuthIPAllowlist: got %v, want %v (must be unchanged)",
			updated.PublicAuthIPAllowlist, three)
	}

	// PATCH with empty list — column becomes empty (the
	// "no rule" state). An empty slice is a deliberate
	// "clear", not a no-op.
	empty := []netip.Prefix{}
	updated, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		PublicAuthIPAllowlist:    &empty,
		SetPublicAuthIPAllowlist: true,
	})
	if err != nil {
		t.Fatalf("set empty allowlist: %v", err)
	}
	if len(updated.PublicAuthIPAllowlist) != 0 {
		t.Errorf("after Set empty: PublicAuthIPAllowlist = %v, want empty",
			updated.PublicAuthIPAllowlist)
	}
}
