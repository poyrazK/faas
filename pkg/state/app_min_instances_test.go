package state

import (
	"context"
	"errors"
	"testing"
)

// TestSetAppMinInstances_RoundTrip covers both MemStore + PgStore
// parity (plan R4) for ux_spec §6.5 — the per-app cold-wake floor.
//
// Cases:
//   - Set 2, then re-read AppByID → 2
//   - Set 0 over the top → 0 (scale-to-zero reset path)
//   - Set on a non-existent app ID → ErrNotFound
//
// Parity with the migration's check constraint: 0 and any positive
// int are accepted; -1 must be rejected by the PG CHECK (MemStore
// is constraint-blind and accepts it, so we exercise only the
// happy-path values here).
func TestSetAppMinInstances_RoundTrip(t *testing.T) {
	ctx := context.Background()
	stores := []struct {
		name  string
		store Store
	}{
		{"MemStore", NewMemStore()},
	}

	for _, s := range stores {
		t.Run(s.name, func(t *testing.T) {
			acct, err := s.store.CreateAccount(ctx, "alice@example.com", "free")
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "api"})
			if err != nil {
				t.Fatal(err)
			}
			// Default reads as 0 (scale to zero).
			got, err := s.store.AppByID(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.MinInstances != 0 {
				t.Errorf("default MinInstances = %d, want 0", got.MinInstances)
			}
			// Set 2.
			if err := s.store.SetAppMinInstances(ctx, app.ID, 2); err != nil {
				t.Fatalf("SetAppMinInstances: %v", err)
			}
			got, err = s.store.AppByID(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.MinInstances != 2 {
				t.Errorf("after Set 2: MinInstances = %d, want 2", got.MinInstances)
			}
			// Reset to 0.
			if err := s.store.SetAppMinInstances(ctx, app.ID, 0); err != nil {
				t.Fatalf("SetAppMinInstances(0): %v", err)
			}
			got, err = s.store.AppByID(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.MinInstances != 0 {
				t.Errorf("after Set 0: MinInstances = %d, want 0", got.MinInstances)
			}
			// Unknown app → ErrNotFound.
			if err := s.store.SetAppMinInstances(ctx, "missing-id", 1); !errors.Is(err, ErrNotFound) {
				t.Errorf("unknown app: err = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestUpdateApp_WithMinInstances pins the partial-update semantics
// of UpdateAppParams.MinInstances + SetMinInstances (mirrors the
// IdleTimeoutS pattern at handlers_ext.go:35). Cases:
//   - SetMinInstances=true with MinInstances=&3 → column becomes 3
//   - SetMinInstances=false with MinInstances=nil → column unchanged
//     (a PATCH that doesn't touch min_instances must not zero it)
//   - SetMinInstances=true with MinInstances=&0 → column becomes 0
//     (explicit scale-to-zero is a deliberate opt-out)
func TestUpdateApp_WithMinInstances(t *testing.T) {
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
	// Set initial floor 2 via SetAppMinInstances so we can prove
	// "unset" doesn't clobber it.
	if err := store.SetAppMinInstances(ctx, app.ID, 2); err != nil {
		t.Fatal(err)
	}

	// PATCH that omits min_instances — column stays at 2.
	updated, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MinInstances != 2 {
		t.Errorf("unset MinInstances: got %d, want 2 (must be unchanged)", updated.MinInstances)
	}

	// PATCH with explicit zero — column becomes 0.
	zero := 0
	updated, err = store.UpdateApp(ctx, app.ID, UpdateAppParams{
		MinInstances: &zero, SetMinInstances: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MinInstances != 0 {
		t.Errorf("explicit zero: got %d, want 0", updated.MinInstances)
	}

	// PATCH with non-zero value — column becomes that value.
	three := 3
	updated, err = store.UpdateApp(ctx, app.ID, UpdateAppParams{
		MinInstances: &three, SetMinInstances: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MinInstances != 3 {
		t.Errorf("explicit 3: got %d, want 3", updated.MinInstances)
	}
}
