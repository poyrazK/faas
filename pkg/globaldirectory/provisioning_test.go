package globaldirectory

// adr: 797
import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type provisionContractStore interface {
	Store
	AppProvisionStore
}

func TestGlobalAppProvisioningMemoryContract(t *testing.T) {
	appProvisionContract(t, NewMemoryStore(), uuid.NewString())
}

func appProvisionContract(t *testing.T, s provisionContractStore, owner string) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateApp(ctx, owner, "provision-shop", 2)
	if err != nil {
		t.Fatal(err)
	}
	var specs []AppProvisionSpec
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		cell, err := s.PutCell(ctx, Cell{ID: uuid.NewString(), Region: region, APIBase: "https://" + region + ".example.com", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(api.CreateAppRequest{Slug: a.Slug, ComputeRegion: region, RAMMB: 128})
		specs = append(specs, AppProvisionSpec{VerifiedCell: cell, Region: region, AccountID: uuid.NewString(), Request: raw})
	}
	plans, err := s.PlanAppProvisioning(ctx, owner, a.Slug, specs)
	if err != nil || len(plans) != 2 {
		t.Fatalf("plan: %+v %v", plans, err)
	}
	again, err := s.PlanAppProvisioning(ctx, owner, a.Slug, specs)
	if err != nil || again[0].ID != plans[0].ID {
		t.Fatalf("immutable replay: %+v %v", again, err)
	}
	changed := append([]AppProvisionSpec(nil), specs...)
	changed[1].AccountID = uuid.NewString()
	if _, err := s.PlanAppProvisioning(ctx, owner, a.Slug, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("owner replacement: %v", err)
	}
	if _, err := s.ListAppProvisioning(ctx, uuid.NewString(), a.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign read: %v", err)
	}
	p := plans[0]
	b := Binding{CellID: p.Cell.ID, Region: p.Region, CellGeneration: p.Cell.Generation, RegionalAccountID: p.AccountID, RegionalAppID: uuid.NewString(), RegionalAppSlug: a.Slug}
	if _, err := s.AttachProvisionTarget(ctx, owner, a.Slug, p.ID, b); err != nil {
		t.Fatal(err)
	}
	if err := s.DetachTarget(ctx, owner, a.Slug, p.Region); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachProvisionTarget(ctx, owner, a.Slug, p.ID, b); !errors.Is(err, ErrConflict) {
		t.Fatalf("detached retry: %v", err)
	}
	retained, err := s.PlanAppProvisioning(ctx, owner, a.Slug, specs)
	if err != nil || retained[0].Status != ProvisionCancelled {
		t.Fatalf("cancellation: %+v %v", retained, err)
	}
	cell := plans[1].Cell
	cell.APIBase = "https://moved.example.com"
	if _, err := s.PutCell(ctx, cell); err != nil {
		t.Fatal(err)
	}
	p = plans[1]
	b = Binding{CellID: p.Cell.ID, Region: p.Region, CellGeneration: p.Cell.Generation, RegionalAccountID: p.AccountID, RegionalAppID: uuid.NewString(), RegionalAppSlug: a.Slug}
	if _, err := s.AttachProvisionTarget(ctx, owner, a.Slug, p.ID, b); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("registry replacement: %v", err)
	}
	retained, err = s.PlanAppProvisioning(ctx, owner, a.Slug, specs)
	if err != nil || retained[1].Cell.APIBase != p.Cell.APIBase {
		t.Fatalf("original origin retention: %+v %v", retained, err)
	}
}

func TestGlobalAppProvisioningMemoryAtomicPlan(t *testing.T) {
	s := NewMemoryStore()
	owner := uuid.NewString()
	_, _ = s.CreateApp(t.Context(), owner, "atomic-shop", 1)
	cell, _ := s.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true})
	raw, _ := json.Marshal(api.CreateAppRequest{Slug: "atomic-shop", ComputeRegion: cell.Region})
	good := AppProvisionSpec{VerifiedCell: cell, Region: cell.Region, AccountID: uuid.NewString(), Request: raw}
	bad := good
	bad.Region = "eu-hel"
	bad.Request, _ = json.Marshal(api.CreateAppRequest{Slug: "atomic-shop", ComputeRegion: bad.Region})
	if _, err := s.PlanAppProvisioning(t.Context(), owner, "atomic-shop", []AppProvisionSpec{good, bad}); !errors.Is(err, ErrStaleCell) {
		t.Fatal(err)
	}
	rows, err := s.ListAppProvisioning(t.Context(), owner, "atomic-shop")
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial plan survived: %+v %v", rows, err)
	}
}
