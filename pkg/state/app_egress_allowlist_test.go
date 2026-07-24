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

// TestUpdateApp_WithEgressAllowlistV6 (ADR-032) is the v6 mirror of
// the parent test: v6 entries round-trip through MemStore exactly
// like v4. The non-/0 contract is held by the DB trigger
// `apps_egress_allowlist_cidr` (migration 00033) — the in-memory
// store is intentionally permissive so it stays useful as a test
// double for code paths that don't care about the family split.
func TestUpdateApp_WithEgressAllowlistV6(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "v6"})
	if err != nil {
		t.Fatal(err)
	}

	v6list := []netip.Prefix{
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	updated, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{
		EgressAllowlist:    &v6list,
		SetEgressAllowlist: true,
	})
	if err != nil {
		t.Fatalf("set v6 allowlist: %v", err)
	}
	if !reflect.DeepEqual(updated.EgressAllowlist, v6list) {
		t.Errorf("after Set v6:\n got  %v\n want %v", updated.EgressAllowlist, v6list)
	}
	readBack, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readBack.EgressAllowlist, v6list) {
		t.Errorf("AppByID v6 round-trip:\n got  %v\n want %v", readBack.EgressAllowlist, v6list)
	}
}

// TestUpdateApp_WithEgressAllowlistMixed (ADR-032) pins that a v4 +
// v6 list round-trips together. The renderer partitions by family
// at emit time; the store cares about neither.
func TestUpdateApp_WithEgressAllowlistMixed(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "mix"})
	if err != nil {
		t.Fatal(err)
	}
	mixed := []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("8.8.8.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	updated, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{
		EgressAllowlist:    &mixed,
		SetEgressAllowlist: true,
	})
	if err != nil {
		t.Fatalf("set mixed allowlist: %v", err)
	}
	if !reflect.DeepEqual(updated.EgressAllowlist, mixed) {
		t.Errorf("after Set mixed:\n got  %v\n want %v", updated.EgressAllowlist, mixed)
	}
	readBack, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readBack.EgressAllowlist, mixed) {
		t.Errorf("AppByID mixed round-trip:\n got  %v\n want %v", readBack.EgressAllowlist, mixed)
	}
}
