package state

import (
	"context"
	"reflect"
	"testing"

	"net/netip"
)

// TestUpdateApp_WithEgressAllowlist pins the partial-update semantics
// of UpdateAppParams.EgressAllowlist + SetEgressAllowlist (ADR-031,
// tier-2 of the network roadmap). Mirrors the MinInstances pattern at
// app_min_instances_test.go:86. Cases:
//
//   - SetEgressAllowlist=false (param nil)  → column unchanged
//     (a PATCH that doesn't touch allowlist must not clobber it)
//   - SetEgressAllowlist=true with non-empty slice → column replaced
//     atomically with the new list
//   - SetEgressAllowlist=true with non-nil EMPTY slice → column
//     becomes nil (back to chain-default-accept); an empty list on
//     the wire MUST mean "clear", not "no-op"
//   - Re-read AppByID deserialises the column back to []netip.Prefix
//     so the read path matches the write path round-trip.
//
// Like the MinInstances hermetic suite, MemStore is enough here —
// pgstore_test.go:247 already mirrors this for the PG round-trip,
// and the e2e migration test at migrations/00029_app_egress_allowlist_
// test.go pins the column shape + CHECK.
func TestUpdateApp_WithEgressAllowlist(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "api"})
	if err != nil {
		t.Fatal(err)
	}

	// Default is nil — no allowlist.
	if got, err := store.AppByID(ctx, app.ID); err != nil {
		t.Fatal(err)
	} else if got.EgressAllowlist != nil {
		t.Errorf("default EgressAllowlist = %v, want nil", got.EgressAllowlist)
	}

	// PATCH that pins a 3-entry list.
	three := []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("8.8.8.0/24"),
		netip.MustParsePrefix("9.9.9.0/24"),
	}
	updated, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{
		EgressAllowlist:    &three,
		SetEgressAllowlist: true,
	})
	if err != nil {
		t.Fatalf("set 3-entry allowlist: %v", err)
	}
	if !reflect.DeepEqual(updated.EgressAllowlist, three) {
		t.Errorf("after Set non-empty:\n got  %v\n want %v", updated.EgressAllowlist, three)
	}

	// Re-read via AppByID — make sure the deserialise path keeps it.
	readBack, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readBack.EgressAllowlist, three) {
		t.Errorf("AppByID round-trip:\n got  %v\n want %v", readBack.EgressAllowlist, three)
	}

	// PATCH with unset (no SetEgressAllowlist) — column stays at 3.
	updated, err = store.UpdateApp(ctx, app.ID, UpdateAppParams{})
	if err != nil {
		t.Fatalf("unset update: %v", err)
	}
	if !reflect.DeepEqual(updated.EgressAllowlist, three) {
		t.Errorf("unset EgressAllowlist: got %v, want %v (must be unchanged)", updated.EgressAllowlist, three)
	}

	// PATCH with empty list — column becomes nil (chain-default-
	// accept contract). An empty slice is a deliberate "clear",
	// not a no-op.
	empty := []netip.Prefix{}
	updated, err = store.UpdateApp(ctx, app.ID, UpdateAppParams{
		EgressAllowlist:    &empty,
		SetEgressAllowlist: true,
	})
	if err != nil {
		t.Fatalf("set empty allowlist: %v", err)
	}
	if len(updated.EgressAllowlist) != 0 {
		t.Errorf("after Set empty: EgressAllowlist = %v, want empty", updated.EgressAllowlist)
	}
}
