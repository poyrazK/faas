// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// fakeVMM models vmmd plus guest-init: volumes, running VMs and whether a
// volume was bootstrapped.
type fakeVMM struct {
	mu         sync.Mutex
	volumes    map[string]bool // id -> bootstrapped
	running    map[string]bool
	calls      []string
	startErr   error
	controlErr error
	// archive makes basebackup succeed; otherwise the guest reports
	// archiving off.
	archive      bool
	adminResp    dbguest.Response
	lastAdminSQL string
	// restoreBoots are the targets of restore boots; restoreFail makes
	// them fail, and restoreHold keeps them recovering until closed.
	restoreBoots []time.Time
	untilEnd     []bool
	recoveredTo  time.Time
	restoring    map[string]bool
	restoreFail  bool
	restoreHold  chan struct{}
	flushes      map[string]time.Time
}

func newFakeVMM() *fakeVMM {
	return &fakeVMM{volumes: map[string]bool{}, running: map[string]bool{}, restoring: map[string]bool{}, flushes: map[string]time.Time{}}
}

func (f *fakeVMM) record(call string) {
	f.calls = append(f.calls, call)
}

func (f *fakeVMM) CreateVolume(_ context.Context, _ Node, id string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("create")
	if _, ok := f.volumes[id]; !ok {
		f.volumes[id] = false
	}
	return nil
}

func (f *fakeVMM) DeleteVolume(_ context.Context, _ Node, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("delete")
	delete(f.volumes, id)
	return nil
}

func (f *fakeVMM) Start(_ context.Context, _ Node, vm VM) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("start")
	if f.startErr != nil {
		return f.startErr
	}
	if _, ok := f.volumes[vm.DatabaseID]; !ok {
		return errors.New("no volume")
	}
	f.running[vm.DatabaseID] = true
	if vm.RestoreSource != "" && !vm.Bootstrapped {
		f.restoreBoots = append(f.restoreBoots, vm.RestoreTarget)
		f.untilEnd = append(f.untilEnd, vm.RestoreUntilEnd)
		f.restoring[vm.DatabaseID] = true
	}
	return nil
}

func (f *fakeVMM) Stop(_ context.Context, _ Node, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("stop")
	delete(f.running, id)
	delete(f.restoring, id)
	return nil
}

func (f *fakeVMM) Control(_ context.Context, _ Node, id string, req dbguest.Request) (dbguest.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.controlErr != nil {
		return dbguest.Response{}, f.controlErr
	}
	if !f.running[id] {
		return dbguest.Response{}, ErrVMNotRunning
	}
	switch req.Op {
	case dbguest.OpBootstrap:
		f.record("bootstrap")
		f.volumes[id] = true
		return dbguest.Response{State: dbguest.StateReady}, nil
	case dbguest.OpBaseBackup:
		if !f.archive {
			return dbguest.Response{State: dbguest.StateReady, Error: dbguest.ErrCodeArchiveOff}, nil
		}
		f.record("basebackup")
		return dbguest.Response{State: dbguest.StateReady, Backup: "20261013T000000Z"}, nil
	case dbguest.OpAdminSQL:
		f.record("admin_sql")
		f.lastAdminSQL = req.SQL
		return f.adminResp, nil
	case dbguest.OpFlushWAL:
		if !f.archive {
			return dbguest.Response{State: dbguest.StateReady, Error: dbguest.ErrCodeArchiveOff}, nil
		}
		f.record("flush")
		f.flushes[id] = req.After
		return dbguest.Response{State: dbguest.StateReady}, nil
	}
	if f.restoring[id] {
		switch {
		case f.restoreFail:
			return dbguest.Response{State: dbguest.StateFailed, Error: dbguest.ErrCodeRestoreFailed}, nil
		case f.restoreHold != nil:
			select {
			case <-f.restoreHold:
			default:
				return dbguest.Response{State: dbguest.StateStarting}, nil
			}
		}
		f.volumes[id] = true
		return dbguest.Response{State: dbguest.StateReady, RecoveredTo: f.recoveredTo}, nil
	}
	if !f.volumes[id] {
		return dbguest.Response{State: dbguest.StateNeedsBootstrap}, nil
	}
	return dbguest.Response{State: dbguest.StateReady}, nil
}

func (f *fakeVMM) callCount(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func newTestScheduler(t *testing.T, ceilingMB int) (*Scheduler, *MemStore, *fakeVMM) {
	t.Helper()
	store, vmm := NewMemStore(), newFakeVMM()
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: ceilingMB, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive})
	s := New(store, vmm, Config{IdleTimeout: time.Minute, ReadyTimeout: 2 * time.Second, WakeWait: time.Second}, nil)
	return s, store, vmm
}

func provision(t *testing.T, s *Scheduler) VM {
	t.Helper()
	vm, err := s.Provision(context.Background(), ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestProvisionBootstrapsAndParks(t *testing.T) {
	s, _, vmm := newTestScheduler(t, 4096)
	vm := provision(t, s)
	if vm.State != StateParked || !vm.Bootstrapped || vm.NodeID != "db-a" {
		t.Fatalf("provisioned = %+v", vm)
	}
	for _, call := range []string{"create", "start", "bootstrap", "stop"} {
		if vmm.callCount(call) != 1 {
			t.Fatalf("%s called %d times; calls %v", call, vmm.callCount(call), vmm.calls)
		}
	}
	again, err := s.Provision(context.Background(), ProvisionRequest{DatabaseID: vm.DatabaseID, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil || again.State != StateParked || vmm.callCount("start") != 1 {
		t.Fatalf("re-provision = %+v, %v; must not boot again", again, err)
	}
}

func TestProvisionFailureMarksFailedAndStops(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	vmm.startErr = errors.New("jailer exploded")
	id := uuid.NewString()
	if _, err := s.Provision(context.Background(), ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err == nil {
		t.Fatal("provision succeeded")
	}
	vm, _ := store.Get(context.Background(), id)
	if vm.State != StateFailed || vm.LastError != errCodeBoot || vmm.callCount("stop") != 1 {
		t.Fatalf("after failure = %+v, stops %d", vm, vmm.callCount("stop"))
	}
}

func TestWakeParkAndTouch(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provision(t, s)
	woke, err := s.Wake(ctx, vm.DatabaseID)
	if err != nil || woke.State != StateRunning || woke.LastActiveAt.IsZero() || woke.NodeTarget != "unix:///a" {
		t.Fatalf("wake = %+v, %v; want running with the node target", woke, err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil || vmm.callCount("start") != 2 {
		t.Fatalf("wake of a running database must not boot again: starts=%d err=%v", vmm.callCount("start"), err)
	}
	if err := s.Park(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateParked || vmm.running[vm.DatabaseID] {
		t.Fatalf("after park = %+v running=%v", got, vmm.running[vm.DatabaseID])
	}
	if err := s.Park(ctx, vm.DatabaseID); err != nil {
		t.Fatalf("parking a parked database must be a no-op: %v", err)
	}
	if err := s.Touch(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("touch of a parked database = %v", err)
	}
}

func TestWakeRefusedOverNodeRAM(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 500) // fits one 384 MiB VM
	ctx := context.Background()
	a, b := provision(t, s), provision(t, s)
	if _, err := s.Wake(ctx, a.DatabaseID); err != nil {
		t.Fatal(err)
	}
	starts := vmm.callCount("start")
	if _, err := s.Wake(ctx, b.DatabaseID); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("second wake = %v, want ErrNoCapacity", err)
	}
	if vmm.callCount("start") != starts {
		t.Fatal("a refused wake booted a VM")
	}
	if got, _ := store.Get(ctx, b.DatabaseID); got.State != StateParked {
		t.Fatalf("refused database = %+v, want parked", got)
	}
}

func TestWakeFailureParksWithError(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provision(t, s)
	vmm.startErr = errors.New("boot failed")
	if _, err := s.Wake(ctx, vm.DatabaseID); err == nil {
		t.Fatal("wake succeeded")
	}
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.State != StateParked || got.LastError != errCodeBoot {
		t.Fatalf("after failed wake = %+v", got)
	}
	vmm.startErr = nil
	if woke, err := s.Wake(ctx, vm.DatabaseID); err != nil || woke.State != StateRunning || woke.LastError != "" {
		t.Fatalf("retry wake = %+v, %v; error must clear", woke, err)
	}
}

func TestAdminWakesAndRelays(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provision(t, s)
	vmm.adminResp = dbguest.Response{State: dbguest.StateReady, Error: dbguest.ErrCodeAdminSQLFailed, SQLState: "42710"}
	resp, err := s.Admin(ctx, vm.DatabaseID, "CREATE ROLE app LOGIN")
	if err != nil || resp.SQLState != "42710" || vmm.lastAdminSQL != "CREATE ROLE app LOGIN" {
		t.Fatalf("admin = %+v, %v (sql %q)", resp, err, vmm.lastAdminSQL)
	}
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateRunning {
		t.Fatalf("admin did not wake: %+v", got)
	}
}

func TestParkIdleParksOnlyIdle(t *testing.T) {
	s, store, _ := newTestScheduler(t, 4096)
	ctx := context.Background()
	idle, busy := provision(t, s), provision(t, s)
	for _, vm := range []VM{idle, busy} {
		if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
			t.Fatal(err)
		}
	}
	_ = store.Touch(ctx, idle.DatabaseID, time.Now().Add(-2*time.Minute))
	n, err := s.ParkIdle(ctx)
	if err != nil || n != 1 {
		t.Fatalf("ParkIdle = %d, %v", n, err)
	}
	if got, _ := store.Get(ctx, idle.DatabaseID); got.State != StateParked {
		t.Fatalf("idle = %+v", got)
	}
	if got, _ := store.Get(ctx, busy.DatabaseID); got.State != StateRunning {
		t.Fatalf("busy = %+v", got)
	}
}

func TestDeleteStopsAndRemoves(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provision(t, s)
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, vm.DatabaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row survived delete: %v", err)
	}
	if vmm.running[vm.DatabaseID] || vmm.callCount("delete") != 1 {
		t.Fatalf("vm running=%v volume deletes=%d", vmm.running[vm.DatabaseID], vmm.callCount("delete"))
	}
	if err := s.Delete(ctx, vm.DatabaseID); err != nil {
		t.Fatalf("deleting a missing database must succeed: %v", err)
	}
}

func TestRecoverParksInFlightDatabases(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provision(t, s)
	if _, err := store.AdmitWake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	stops := vmm.callCount("stop")
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateParked || vmm.callCount("stop") != stops+1 {
		t.Fatalf("after recover = %+v, stops %d", got, vmm.callCount("stop")-stops)
	}
}
