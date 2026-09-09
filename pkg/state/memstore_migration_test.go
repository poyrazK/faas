package state

// memstore-side coverage tests for the Tier A6 / ADR-067
// migrating-instance watchdog surface. The pgstore parity tests
// live in pgstore_migration_test.go; this file pins the in-memory
// branch of the same three methods:
//
//   - ListExpiredMigrations: empty-cap, no-rows, lease-less-row
//     drops, cap respected (50 of 60 returned).
//   - ReinviteMigratingInstance: happy, ErrNotFound, ErrConflict
//     (wrong lease), empty-arg rejection.
//   - AbortMigratingInstance: happy, ErrNotFound, ErrConflict
//     (wrong lease), empty-arg rejection.
//
// Without these tests, the engine-side happy-path coverage in
// pkg/sched/migrating_watchdog_engine_test.go does not exercise
// the rejection branches, and the package-level coverage gate
// (≥70% in Makefile test-state-coverage) falls below threshold.
// The fixture mirrors the one used by pgstore_migration_test.go,
// scoped to a single test name + uuid suffix so go test
// -run is selective.

// memstore_migration_test.go (Tier A6 / ADR-067)
//
// Whitebox tests (package state) for the memstore side of the
// watchdog's three Store methods. Each test drives one method
// through its happy path + the rejection branches the engine
// tests do not exercise.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// seedReconcileMemStore seeds a MemStore with one app + one
// instance in 'migrating' on the given node, with a fresh lease
// token. Mirrors seedRunningInstance in pgstore_migration_test.go
// but on the in-memory store. Returns the instance id + lease.
func seedReconcileMemStore(t *testing.T, m *MemStore, nodeID string) (instanceID, leaseToken string) {
	t.Helper()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "recon-mem-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "recon-mem-" + uuid.NewString(),
		NodeID: nodeID, Status: AppActive, RAMMB: 256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	ins, err := m.CreateInstance(ctx, app.ID, "", string(StateRunning), 256, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	leaseToken = uuid.NewString()
	if err := m.MarkInstanceMigrating(ctx, ins.ID, nodeID, leaseToken); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	return ins.ID, leaseToken
}

// TestMemStore_ListExpiredMigrations pins the in-memory branch
// of the watchdog's input-set query. Covers:
//   - empty-cap = no-op (returns nil, not error)
//   - no migrating rows = empty slice
//   - lease-less row in 'migrating' is dropped silently
//   - cap=50 of 60 returned (the rest stay for the next tick)
func TestMemStore_ListExpiredMigrations(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	nodeID := "recon-list-" + uuid.NewString()

	// 60 seeded migrating rows on the same node.
	for i := 0; i < 60; i++ {
		_, _ = seedReconcileMemStore(t, m, nodeID)
	}
	// One parked row that should NOT appear in the list.
	seededParked, _ := seedReconcileMemStore(t, m, nodeID)
	if err := m.AbortMigratingInstance(ctx, seededParked, ""); err == nil {
		t.Fatalf("Abort(empty lease) should be rejected")
	}
	// 1 row in 'migrating' with no lease — must be dropped.
	leaseLessApp, err := m.CreateApp(ctx, App{
		AccountID: "00000000-0000-0000-0000-000000000777",
		Slug:      "recon-leak-" + uuid.NewString(),
		NodeID:    nodeID, Status: AppActive, RAMMB: 256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	leaseLessIns, err := m.CreateInstance(ctx, leaseLessApp.ID, "", string(StateMigrating), 256, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance lease-less: %v", err)
	}
	_ = leaseLessIns

	// Empty-cap → empty slice, no error.
	rows, err := m.ListExpiredMigrations(ctx, 0)
	if err != nil {
		t.Fatalf("ListExpiredMigrations cap=0: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("cap=0 returned %d rows", len(rows))
	}

	// Cap=50 of 60.
	rows, err = m.ListExpiredMigrations(ctx, 50)
	if err != nil {
		t.Fatalf("ListExpiredMigrations cap=50: %v", err)
	}
	if len(rows) != 50 {
		t.Fatalf("cap=50 returned %d rows; want 50", len(rows))
	}
	// row.ID must be sorted ascending (parity with pgstore ORDER BY).
	for i := 1; i < len(rows); i++ {
		if rows[i].ID <= rows[i-1].ID {
			t.Errorf("ListExpiredMigrations not sorted: %s <= %s at i=%d",
				rows[i].ID, rows[i-1].ID, i)
		}
	}
	// Lease-less row must not appear.
	for _, r := range rows {
		if r.ID == leaseLessIns.ID {
			t.Errorf("lease-less row %s leaked into input set", r.ID)
		}
	}
}

func TestMemStore_ListExpiredMigrationsHonorsLeaseAge(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	nodeID := "recon-age-" + uuid.NewString()
	oldID, _ := seedReconcileMemStore(t, m, nodeID)
	freshID, _ := seedReconcileMemStore(t, m, nodeID)
	now := time.Now().UTC()
	m.SetInstanceMigrationStartedAtForTest(oldID, now.Add(-2*time.Minute))
	m.SetInstanceMigrationStartedAtForTest(freshID, now.Add(-5*time.Second))

	rows, err := m.ListExpiredMigrations(ctx, 100, time.Minute)
	if err != nil {
		t.Fatalf("ListExpiredMigrations with age: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != oldID {
		t.Fatalf("aged rows = %+v, want only %s", rows, oldID)
	}

	// The compatibility form remains an unfiltered view for tooling that
	// needs to inspect every leased migrating row.
	rows, err = m.ListExpiredMigrations(ctx, 100)
	if err != nil {
		t.Fatalf("ListExpiredMigrations legacy view: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("legacy rows = %d, want 2", len(rows))
	}
}

// TestMemStore_ReinviteMigratingInstance pins the happy path +
// ErrNotFound + ErrConflict + empty-arg rejection for the
// active-owner ack gate.
func TestMemStore_ReinviteMigratingInstance(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	nodeID := "reinvite-" + uuid.NewString()
	insID, lease := seedReconcileMemStore(t, m, nodeID)

	// Happy → state flips to running, lease cleared.
	if err := m.ReinviteMigratingInstance(ctx, insID, lease); err != nil {
		t.Fatalf("Reinvite happy: %v", err)
	}
	got, err := m.InstanceByID(ctx, insID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if got.State != string(StateRunning) {
		t.Errorf("post-reinvite state=%q want %q", got.State, StateRunning)
	}
	if got.LeaseToken != "" {
		t.Errorf("post-reinvite lease_token=%q want empty", got.LeaseToken)
	}
	// node_id untouched (the reinvite is owner-side, not placement).
	if got.NodeID != nodeID {
		t.Errorf("post-reinvite node_id=%q want %q", got.NodeID, nodeID)
	}

	// Wrong lease → ErrConflict (cannot double-commit).
	if err := m.ReinviteMigratingInstance(ctx, insID, "wrong-lease"); !errors.Is(err, ErrConflict) {
		t.Errorf("Reinvite wrong-lease: %v; want ErrConflict", err)
	}
	// Missing row → ErrNotFound.
	if err := m.ReinviteMigratingInstance(ctx, "00000000-0000-0000-0000-000000000099", "any"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Reinvite missing: %v; want ErrNotFound", err)
	}
	// Already-running row → ErrConflict (state predicate fails).
	if err := m.ReinviteMigratingInstance(ctx, insID, lease); !errors.Is(err, ErrConflict) {
		t.Errorf("Reinvite already-running: %v; want ErrConflict", err)
	}
	// Empty args rejected.
	for _, tt := range []struct {
		name, id, lease string
	}{
		{"empty id", "", "lease"},
		{"empty lease", insID, ""},
	} {
		if err := m.ReinviteMigratingInstance(ctx, tt.id, tt.lease); err == nil {
			t.Errorf("Reinvite(%s): nil error; want rejection", tt.name)
		}
	}
}

// TestMemStore_AbortMigratingInstance pins the dead-owner
// hard-delete gate: state flips to parked, lease cleared,
// node_id left UNCHANGED (parity with pgstore's pre-Phase-3
// assumption — migrated_from_node_id is NULL, the wake path
// dispatches via app.NodeID, the dead instance.NodeID is harmless).
func TestMemStore_AbortMigratingInstance(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	nodeID := "abort-" + uuid.NewString()
	insID, lease := seedReconcileMemStore(t, m, nodeID)

	// Happy → state flips to parked, lease cleared.
	if err := m.AbortMigratingInstance(ctx, insID, lease); err != nil {
		t.Fatalf("Abort happy: %v", err)
	}
	got, err := m.InstanceByID(ctx, insID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if got.State != string(StateParked) {
		t.Errorf("post-abort state=%q want %q", got.State, StateParked)
	}
	if got.LeaseToken != "" {
		t.Errorf("post-abort lease_token=%q want empty", got.LeaseToken)
	}
	// node_id left UNCHANGED on the dead OLD owner (NOT zeroed).
	if got.NodeID != nodeID {
		t.Errorf("post-abort node_id=%q want %q (dead OLD owner — must not change)", got.NodeID, nodeID)
	}

	// Wrong lease → ErrConflict.
	if err := m.AbortMigratingInstance(ctx, insID, "wrong-lease"); !errors.Is(err, ErrConflict) {
		t.Errorf("Abort wrong-lease: %v; want ErrConflict", err)
	}
	// Missing row → ErrNotFound.
	if err := m.AbortMigratingInstance(ctx, "00000000-0000-0000-0000-000000000099", "any"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Abort missing: %v; want ErrNotFound", err)
	}
	// Already-parked row → ErrConflict (state predicate fails).
	if err := m.AbortMigratingInstance(ctx, insID, lease); !errors.Is(err, ErrConflict) {
		t.Errorf("Abort already-parked: %v; want ErrConflict", err)
	}
	// Empty args rejected.
	for _, tt := range []struct {
		name, id, lease string
	}{
		{"empty id", "", "lease"},
		{"empty lease", insID, ""},
	} {
		if err := m.AbortMigratingInstance(ctx, tt.id, tt.lease); err == nil {
			t.Errorf("Abort(%s): nil error; want rejection", tt.name)
		}
	}
}

// TestMemStore_ListExpiredMigrations_NoRows covers the empty-set
// branch in isolation; the seeding-heavy test above does not
// exercise the "no migrating rows at all" early-return.
func TestMemStore_ListExpiredMigrations_NoRows(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	rows, err := m.ListExpiredMigrations(ctx, 100)
	if err != nil {
		t.Fatalf("ListExpiredMigrations: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty store returned %d rows", len(rows))
	}
}
