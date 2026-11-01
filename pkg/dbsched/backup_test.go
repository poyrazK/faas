// adr: 1027
package dbsched

import (
	"context"
	"testing"
	"time"
)

func TestProvisionTakesTheFirstBackupWhenArchiving(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	vmm.archive = true
	vm := provision(t, s)
	got, _ := store.Get(context.Background(), vm.DatabaseID)
	if vmm.callCount("basebackup") != 1 || got.LastBackupAt.IsZero() {
		t.Fatalf("provision with archiving: backups %d, last %v", vmm.callCount("basebackup"), got.LastBackupAt)
	}

	s2, store2, vmm2 := newTestScheduler(t, 4096) // archiving off on the node
	vm2 := provision(t, s2)
	if got, _ := store2.Get(context.Background(), vm2.DatabaseID); !got.LastBackupAt.IsZero() || vmm2.callCount("basebackup") != 0 {
		t.Fatalf("a node without archiving recorded a backup: %+v", got)
	}
	if got.State != StateParked {
		t.Fatalf("provision ended %s", got.State)
	}
}

func TestBackupDueBacksUpOnlyStaleRunningDatabases(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	vmm.archive = true
	ctx := context.Background()
	stale, fresh, parked := provision(t, s), provision(t, s), provision(t, s)
	for _, vm := range []VM{stale, fresh} {
		if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
			t.Fatal(err)
		}
	}
	_ = store.RecordBackup(ctx, stale.DatabaseID, time.Now().Add(-25*time.Hour))
	_ = store.RecordBackup(ctx, parked.DatabaseID, time.Now().Add(-25*time.Hour))
	before := vmm.callCount("basebackup")
	n, err := s.BackupDue(ctx)
	if err != nil || n != 1 || vmm.callCount("basebackup") != before+1 {
		t.Fatalf("BackupDue = %d, %v (backups %d -> %d)", n, err, before, vmm.callCount("basebackup"))
	}
	if got, _ := store.Get(ctx, stale.DatabaseID); time.Since(got.LastBackupAt) > time.Minute {
		t.Fatalf("stale database not re-stamped: %v", got.LastBackupAt)
	}
	if got, _ := store.Get(ctx, parked.DatabaseID); got.State != StateParked {
		t.Fatalf("the backup sweep woke a parked database: %s", got.State)
	}
}
