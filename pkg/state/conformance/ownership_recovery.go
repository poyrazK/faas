package conformance

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func ownershipDestination(t *testing.T, fx *Fixture) state.ComputeNode {
	t.Helper()
	n := fx.Node
	n.ID, n.Name = "", "ownership-"+uuid.NewString()
	n, err := fx.Store.CreateComputeNode(fx.Ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// ADR-421: paging is exclusive, scoped before LIMIT, and does not mutate work.
func testOwnershipRecoveryPages(t *testing.T, fx *Fixture) {
	var ids []string
	for range 4 {
		a := fx.App
		a.ID, a.Slug = "", "ownership-"+uuid.NewString()
		a, err := fx.Store.CreateApp(fx.Ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.Store.SetAppNodeID(fx.Ctx, a.ID, fx.Node.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fx.Node.ID, false); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	for i, after := 0, ""; i < len(ids); i++ {
		rows, err := fx.Store.ListOrphanedAppsPage(fx.Ctx, 60, 1, after, fx.Node.ID)
		if err != nil || len(rows) != 1 || rows[0].ID != ids[i] || rows[0].NodeID != fx.Node.ID || rows[0].RAMMB != fx.App.RAMMB {
			t.Fatalf("page %d: %+v err=%v, want %s", i, rows, err, ids[i])
		}
		after = rows[0].ID
	}
	for _, tc := range []struct {
		after, source string
		limit         int
	}{
		{ids[len(ids)-1], "", 1}, {"", uuid.NewString(), 1}, {"", "", 0},
	} {
		rows, err := fx.Store.ListOrphanedAppsPage(fx.Ctx, 60, tc.limit, tc.after, tc.source)
		if err != nil || len(rows) != 0 {
			t.Fatalf("empty page %+v: %+v err=%v", tc, rows, err)
		}
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fx.Node.ID, true); err != nil {
		t.Fatal(err)
	}
	if rows, err := fx.Store.ListOrphanedAppsPage(fx.Ctx, 60, 10, "", ""); err != nil || len(rows) != 0 {
		t.Fatalf("recovered source still orphaned: %+v err=%v", rows, err)
	}
}

// ADR-421: transfer health and cooldown are checked again after discovery.
func testOwnershipRecoveryTransfer(t *testing.T, fx *Fixture) {
	destination := ownershipDestination(t, fx)
	if err := fx.Store.SetAppNodeID(fx.Ctx, fx.App.ID, fx.Node.ID); err != nil {
		t.Fatal(err)
	}
	transfer := func() error {
		return fx.Store.ReassignOrphanedAppOwner(fx.Ctx, fx.App.ID, fx.Node.ID, destination.ID, 60)
	}
	if err := transfer(); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("live source stolen: %v", err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fx.Node.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleRecovering, state.NodeLifecycleMaintenance, state.NodeLifecycleUnavailable} {
		if err := fx.Store.NodeSetLifecycle(fx.Ctx, destination.ID, state.NodeLifecycleActive, lifecycle); err != nil {
			t.Fatal(err)
		}
		if err := transfer(); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("destination %s accepted: %v", lifecycle, err)
		}
		if err := fx.Store.NodeSetLifecycle(fx.Ctx, destination.ID, lifecycle, state.NodeLifecycleActive); err != nil {
			t.Fatal(err)
		}
	}
	const contenders = 8
	var wg sync.WaitGroup
	var wins atomic.Int32
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := transfer()
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, state.ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("transfer winners=%d, want 1", wins.Load())
	}
	app, err := fx.Store.AppByID(fx.Ctx, fx.App.ID)
	if err != nil || app.NodeID != destination.ID || app.ReassignedAt == nil {
		t.Fatalf("ownership not persisted: %+v err=%v", app, err)
	}
	// A second host loss must still respect the persisted reassignment cooldown.
	if err := fx.Store.ReassignAppOwner(fx.Ctx, fx.App.ID, destination.ID, fx.Node.ID); err != nil {
		t.Fatal(err)
	}
	if err := transfer(); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cooldown bypassed: %v", err)
	}
	if rows, err := fx.Store.ListOrphanedAppsPage(fx.Ctx, 60, 10, "", ""); err != nil || len(rows) != 0 {
		t.Fatalf("cooldown candidate returned: %+v err=%v", rows, err)
	}
	if err := fx.Store.ReassignOrphanedAppOwner(fx.Ctx, fx.App.ID, fx.Node.ID, destination.ID, -1); err != nil {
		t.Fatalf("explicit cooldown override: %v", err)
	}
	// A deletion between discovery and transfer cannot revive ownership work.
	if _, err := fx.Store.SoftDeleteAppCascade(fx.Ctx, fx.App.ID); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, destination.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fx.Node.ID, true); err != nil {
		t.Fatal(err)
	}
	if rows, err := fx.Store.ListOrphanedAppsPage(fx.Ctx, -1, 10, "", ""); err != nil || len(rows) != 0 {
		t.Fatalf("deleted app rediscovered: %+v %v", rows, err)
	}
	if err := fx.Store.ReassignOrphanedAppOwner(fx.Ctx, fx.App.ID, destination.ID, fx.Node.ID, -1); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("deleted app ownership changed: %v", err)
	}
}
