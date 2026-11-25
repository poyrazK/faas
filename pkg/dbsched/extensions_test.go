package dbsched

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

type extensionTestVMM struct {
	VMM
	catalog *pgextension.Catalog
	files   []string
}

func (v extensionTestVMM) Control(ctx context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error) {
	if req.Op == dbguest.OpExtensions {
		return dbguest.Response{State: dbguest.StateReady, Extensions: v.catalog}, nil
	}
	return v.VMM.Control(ctx, node, id, req)
}

func (v extensionTestVMM) extensionFiles(context.Context, Node, string, int) ([]string, error) {
	return v.files, nil
}

func TestUpgradeChecksExtensionVersionBeforeStoppingPrimary(t *testing.T) {
	s, _, vmm := newTestScheduler(t, 4096)
	vm := provisionMajor(t, s, 15)
	ctx := t.Context()
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	trgm := pgextension.Installed{Name: "pg_trgm", Version: "1.5"}
	s.vmm = extensionTestVMM{
		VMM:     vmm,
		catalog: &pgextension.Catalog{Installed: []pgextension.Installed{trgm}, Available: []pgextension.Available{}},
		files:   []string{"pg_trgm.control", "pg_trgm--1.6.sql"},
	}
	stops := vmm.callCount("stop")
	if _, blockers, err := s.Extensions(ctx, vm.DatabaseID, "gregale", "", 17); err != nil || !slices.Equal(blockers, []pgextension.Installed{trgm}) {
		t.Fatalf("extension blockers = %v, %v", blockers, err)
	}
	if _, err := s.StartUpgradeChecked(ctx, vm.DatabaseID, 17, uuid.NewString(), "gregale"); !errors.Is(err, ErrUpgradeExtensions) {
		t.Fatalf("upgrade accepted incompatible extensions: %v", err)
	}
	got, err := s.Get(ctx, vm.DatabaseID)
	if err != nil || got.State != StateRunning || got.Major != 15 || vmm.callCount("stop") != stops {
		t.Fatalf("extension preflight interrupted the primary: %+v, %v", got, err)
	}
}
