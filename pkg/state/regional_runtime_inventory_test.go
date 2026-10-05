package state

// adr: 635
import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func runRegionalInventoryContract(t *testing.T, s Store, account Account, cell string) {
	t.Helper()
	ledger := s.(RegionalRuntimeCapacityLedger)
	resources := RegionalRuntimeResources{RAMMB: 128, VCPU: 2, CPUMillicores: 1000}
	a := newRegionalCapacityFixture(t, s, ledger, account, cell)
	b := newRegionalCapacityFixture(t, s, ledger, account, cell)
	c := newRegionalCapacityFixture(t, s, ledger, account, cell)
	other := newRegionalCapacityFixture(t, s, ledger, account, uuid.NewString())
	sharedID := uuid.NewString() // Per-app permit IDs are deliberately repeated.
	pa, pb, pc := a.permit(runtimequota.CapacityServing), b.permit(runtimequota.CapacityWarm), c.permit(runtimequota.CapacityAppTask)
	pa.ID, pb.ID, pc.ID = sharedID, sharedID, sharedID
	source := runtimequota.LaunchSource{Permit: pa, InstanceKey: pa.InstanceID}
	placement := runtimequota.Placement{Permit: pa, Seat: &runtimequota.ServingSeat{Permit: pa, ID: uuid.NewString()}}
	if err := ledger.IssueRegionalRuntimeLaunchResources(t.Context(), placement, source, resources); err != nil {
		t.Fatal(err)
	}
	if err := ledger.IssueRegionalRuntimeLaunchResources(t.Context(), placement, source, resources); err != nil {
		t.Fatal("same resource retry", err)
	}
	for _, changed := range []RegionalRuntimeResources{{RAMMB: 256, VCPU: 2, CPUMillicores: 1000}, {RAMMB: 128, VCPU: 4, CPUMillicores: 1000}, {RAMMB: 128, VCPU: 2, CPUMillicores: 500}} {
		if err := ledger.IssueRegionalRuntimeLaunchResources(t.Context(), placement, source, changed); !errors.Is(err, ErrConflict) {
			t.Fatal("changed resources rewrote original launch", changed, err)
		}
	}
	if err := ledger.IssueRegionalRuntimeLaunch(t.Context(), placement, source); !errors.Is(err, ErrConflict) {
		t.Fatal("legacy retry discarded resource provenance", err)
	}
	legacySource := runtimequota.LaunchSource{Permit: pb, InstanceKey: pb.InstanceID}
	if err := ledger.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: pb}, legacySource); err != nil {
		t.Fatal(err)
	}
	if err := ledger.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: pb}, legacySource, resources); !errors.Is(err, ErrConflict) {
		t.Fatal("historical unknown resources were invented on retry", err)
	}
	c.reserve(t, pc)
	c.issue(t, pc) // Historical issued hold without any retained lookup source.
	reserved := c.permit(runtimequota.CapacityPrime)
	c.reserve(t, reserved) // Reserved but not dispatched; no native resident.
	po := other.permit(runtimequota.CapacityPrime)
	other.reserve(t, po)
	other.issue(t, po)
	if err := s.DeleteApp(t.Context(), b.app()); err != nil {
		t.Fatal(err)
	}
	fence := uuid.NewString()
	if _, err := ledger.BeginRegionalRuntimeRetirement(t.Context(), pa, fence); err != nil {
		t.Fatal(err)
	}
	rows, err := ledger.ListRegionalRuntimeInventory(t.Context(), cell, RegionalRuntimeRetirementCursor{}, 10)
	if err != nil || len(rows) != 3 {
		t.Fatal("inventory missed issued, unfenced or collected holds", rows, err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		p := row.Receipt.Permit
		seen[p.Grant.Binding.RegionalAppID] = true
		switch p.Grant.Binding.RegionalAppID {
		case a.app():
			if row.Source == nil || *row.Source != source || row.Resources != resources || row.ServingSeatsHeld != 1 || row.Receipt.FenceID != fence {
				t.Fatal("inventory lost original resource or fenced serving hold", row)
			}
		case b.app():
			if row.Source == nil || *row.Source != legacySource || row.Resources != (RegionalRuntimeResources{}) {
				t.Fatal("historical source lost or filled from current rows", row)
			}
		case c.app():
			if row.Source != nil || row.Resources != (RegionalRuntimeResources{}) {
				t.Fatal("inventory fabricated historical native lookup", row)
			}
		default:
			t.Fatal("another cell entered inventory", row)
		}
	}
	if len(seen) != 3 {
		t.Fatal("duplicate permit IDs collapsed across apps", seen)
	}
	var cursor RegionalRuntimeRetirementCursor
	for i, want := range rows {
		page, err := ledger.ListRegionalRuntimeInventory(t.Context(), cell, cursor, 1)
		if err != nil || len(page) != 1 || page[0].Receipt.Permit != want.Receipt.Permit {
			t.Fatal("composite inventory pagination", i, page, err)
		}
		cursor = RegionalRuntimeRetirementCursor{AppID: page[0].Receipt.Permit.Grant.Binding.RegionalAppID, PermitID: page[0].Receipt.Permit.ID}
	}
	page, err := ledger.ListRegionalRuntimeInventory(t.Context(), cell, cursor, 1)
	if err != nil || len(page) != 0 {
		t.Fatal("inventory traversal did not terminate", page, err)
	}
	a.stop(t, pa, fence)
	rows, err = ledger.ListRegionalRuntimeInventory(t.Context(), cell, RegionalRuntimeRetirementCursor{}, 10)
	if err != nil || len(rows) != 2 {
		t.Fatal("accepted native stop still charged physical inventory", rows, err)
	}
	invalid := a.permit(runtimequota.CapacityPrime)
	badSource := runtimequota.LaunchSource{Permit: invalid, InstanceKey: invalid.InstanceID}
	if err := ledger.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: invalid}, badSource, RegionalRuntimeResources{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("unknown resources authorized new dispatch", err)
	}
	if _, err := ledger.RegionalRuntimeCapacity(t.Context(), a.app(), invalid.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("invalid resources created partial capacity", err)
	}
}

func TestMemRegionalRuntimeInventoryContract(t *testing.T) {
	s := NewMemStore()
	a, err := s.CreateAccount(t.Context(), "regional-inventory@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRegionalInventoryContract(t, s, a, uuid.NewString())
}
