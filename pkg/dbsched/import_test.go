// adr: 1027 — schedd starts an import in the database's own VM, reports
// it, and does not park a database while it imports.
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func testImportSpec() ImportSpec {
	return ImportSpec{Host: "ep-quiet-1.us-east-2.aws.neon.tech", Port: 5432, Source: dbguest.ImportSource{Database: "gregale", User: "gregale_mig_ab",
		Password: "p", TargetDatabase: "gregale", Owner: "gregale_schema_owner", OperationID: "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"}}
}

func TestImportWakesAndIdleParkingWaitsForIt(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provision(t, s)
	if status, err := s.ImportStatus(ctx, vm.DatabaseID); err != nil || status.State != dbguest.ImportNone {
		t.Fatalf("status of a parked database = %+v, %v", status, err)
	}
	status, err := s.Import(ctx, vm.DatabaseID, testImportSpec())
	if err != nil || status.State != dbguest.ImportRunning {
		t.Fatalf("import = %+v, %v", status, err)
	}
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateRunning {
		t.Fatalf("import did not wake the database: %s", got.State)
	}
	if calls := vmm.nodeCalls(); !strings.Contains(calls, "import:ep-quiet-1.us-east-2.aws.neon.tech:5432@db-a") {
		t.Fatalf("calls %s", calls)
	}
	// Idle by its clock, but importing: idle parking leaves it running.
	_ = store.Touch(ctx, vm.DatabaseID, time.Now().Add(-time.Hour))
	if n, err := s.ParkIdle(ctx); err != nil || n != 0 {
		t.Fatalf("ParkIdle = %d, %v; an importing database stays up", n, err)
	}
	vmm.setImport(vm.DatabaseID, dbguest.ImportDone)
	if status, _ := s.ImportStatus(ctx, vm.DatabaseID); status.State != dbguest.ImportDone {
		t.Fatalf("status = %+v", status)
	}
	_ = store.Touch(ctx, vm.DatabaseID, time.Now().Add(-time.Hour))
	if n, err := s.ParkIdle(ctx); err != nil || n != 1 {
		t.Fatalf("ParkIdle after the import = %d, %v", n, err)
	}
	bad := testImportSpec()
	bad.Source.Database = "app; drop"
	if _, err := s.Import(ctx, vm.DatabaseID, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid source = %v, want ErrInvalid", err)
	}
}
