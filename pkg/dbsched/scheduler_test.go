// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	// marks are each database's restore drill mark, which a restore copies
	// when the target is past it; markAt is the guest clock a mark reports
	// (zero: now), and markErr the error a mark answers.
	marks   map[string]fakeMark
	markAt  time.Time
	markErr string
	// promoteErr is the error a standby answers a promotion with.
	promoteErr string
	// storage is what a ready guest reports about its volume, and
	// standbyStorage what a standby reports; stale marks "node/id" VMs on
	// an outdated base.
	storage        map[string]*dbguest.StorageStatus
	standbyStorage map[string]*dbguest.StorageStatus
	stale          map[string]bool
	// onNode records node-scoped calls as "op@node"; volNode is where each
	// volume was created.
	onNode       []string
	volNode      map[string]string
	pruneErr     map[string]error
	archiveBytes map[string]int64
	growErr      error
	// copied is where each database's volume was copied by a move not yet
	// switched over; copyErr and syncErr fail those steps.
	copied           map[string]string
	copyErr, syncErr error
	// copyGate, when set, holds every copy until it is closed.
	copyGate chan struct{}
	// startDelay is how long a boot takes, outside the lock; booting and
	// maxBooting count the boots in flight.
	startDelay          time.Duration
	booting, maxBooting int
	// Standbys: primaryOn is where each database's VM was started,
	// standbyOn where its standby runs, standbyVol where its standby
	// volume is; replMode is its primary's replication, unsynced keeps a
	// standby from becoming synchronous, promoted is where it was
	// promoted. standbyErr fails standby boots.
	primaryOn  map[string]string
	standbyOn  map[string]string
	standbyVol map[string]string
	replMode   map[string]dbguest.ReplicationMode
	unsynced   map[string]bool
	promoted   map[string]string
	standbyErr error
	// imports is each database's import, as its guest reports it.
	imports map[string]dbguest.ImportStatus
	// down are nodes whose vmmd does not answer.
	down map[string]bool
	// holds are archive holds by id/name, as a shared bucket keeps them.
	holds map[string]ArchiveHold
	// Majors: majors is each bootstrapped volume's cluster major and
	// runMajor the major each running VM booted. upgrading are upgrade
	// boots in flight (id -> major). Their guest upgrades unless
	// upgradeCode fails it, and upgradeHold keeps it upgrading until
	// closed. upgradeFroms records each boot's UpgradeFrom.
	majors       map[string]int
	runMajor     map[string]int
	upgrading    map[string]int
	upgradeCode  string
	upgradeHold  chan struct{}
	upgradeFroms []int
}

func (f *fakeVMM) PutHold(_ context.Context, node Node, id, name string, point time.Time) (ArchiveHold, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[node.ID] {
		return ArchiveHold{}, errNodeDown
	}
	f.onNode = append(f.onNode, "hold:"+name+"@"+node.ID)
	if h, ok := f.holds[id+"/"+name]; ok {
		if !h.PointInTime.Equal(point) {
			return ArchiveHold{}, ErrConflict
		}
		return h, nil
	}
	if f.holds == nil {
		f.holds = map[string]ArchiveHold{}
	}
	h := ArchiveHold{PointInTime: point, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	f.holds[id+"/"+name] = h
	return h, nil
}

func (f *fakeVMM) GetHold(_ context.Context, node Node, id, name string) (ArchiveHold, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[node.ID] {
		return ArchiveHold{}, errNodeDown
	}
	h, ok := f.holds[id+"/"+name]
	if !ok {
		return ArchiveHold{}, ErrNotFound
	}
	return h, nil
}

func (f *fakeVMM) DeleteHold(_ context.Context, node Node, id, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[node.ID] {
		return errNodeDown
	}
	f.onNode = append(f.onNode, "unhold:"+name+"@"+node.ID)
	delete(f.holds, id+"/"+name)
	return nil
}

func newFakeVMM() *fakeVMM {
	return &fakeVMM{volumes: map[string]bool{}, running: map[string]bool{}, restoring: map[string]bool{}, flushes: map[string]time.Time{}, volNode: map[string]string{}, storage: map[string]*dbguest.StorageStatus{}, stale: map[string]bool{},
		primaryOn: map[string]string{}, standbyOn: map[string]string{}, standbyVol: map[string]string{}, replMode: map[string]dbguest.ReplicationMode{},
		unsynced: map[string]bool{}, promoted: map[string]string{}, imports: map[string]dbguest.ImportStatus{}, down: map[string]bool{},
		majors: map[string]int{}, runMajor: map[string]int{}, upgrading: map[string]int{}, marks: map[string]fakeMark{}, standbyStorage: map[string]*dbguest.StorageStatus{}}
}

type fakeMark struct {
	drill string
	at    time.Time
}

func (f *fakeVMM) record(call string) {
	f.calls = append(f.calls, call)
}

func (f *fakeVMM) CreateVolume(_ context.Context, node Node, id string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("create")
	if _, ok := f.volumes[id]; !ok {
		f.volumes[id] = false
		f.volNode[id] = node.ID
	} else if f.volNode[id] != node.ID {
		f.standbyVol[id] = node.ID
	}
	return nil
}

func (f *fakeVMM) DeleteVolume(_ context.Context, node Node, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("delete")
	f.onNode = append(f.onNode, "delete@"+node.ID)
	delete(f.volumes, id)
	return nil
}

func (f *fakeVMM) Start(_ context.Context, node Node, vm VM) error {
	if f.startDelay > 0 {
		f.mu.Lock()
		f.booting++
		f.maxBooting = max(f.maxBooting, f.booting)
		f.mu.Unlock()
		time.Sleep(f.startDelay)
		defer func() { f.mu.Lock(); f.booting--; f.mu.Unlock() }()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("start")
	if f.startErr != nil {
		return f.startErr
	}
	if _, ok := f.volumes[vm.DatabaseID]; !ok {
		return errors.New("no volume")
	}
	if vm.UpgradeFrom != 0 && !f.running[vm.DatabaseID] {
		f.upgrading[vm.DatabaseID] = vm.Major
	}
	f.upgradeFroms = append(f.upgradeFroms, vm.UpgradeFrom)
	f.running[vm.DatabaseID] = true
	f.runMajor[vm.DatabaseID] = vm.Major
	f.primaryOn[vm.DatabaseID] = node.ID
	// Every primary boot starts asynchronous, as guest-init's does.
	if f.replMode[vm.DatabaseID] == dbguest.ReplicationSync {
		f.replMode[vm.DatabaseID] = dbguest.ReplicationAsync
	}
	if vm.RestoreSource != "" && !vm.Bootstrapped {
		f.restoreBoots = append(f.restoreBoots, vm.RestoreTarget)
		f.untilEnd = append(f.untilEnd, vm.RestoreUntilEnd)
		f.restoring[vm.DatabaseID] = true
		if mark, ok := f.marks[vm.RestoreSource]; ok && !mark.at.After(vm.RestoreTarget) {
			f.marks[vm.DatabaseID] = mark
		}
	}
	return nil
}

func (f *fakeVMM) DropArchive(_ context.Context, node Node, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("drop_archive")
	f.onNode = append(f.onNode, "drop_archive@"+node.ID)
	return nil
}

func (f *fakeVMM) PruneArchive(_ context.Context, node Node, id string, windowSeconds int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onNode = append(f.onNode, fmt.Sprintf("prune:%s:%d@%s", id, windowSeconds, node.ID))
	return f.pruneErr[node.ID]
}

func (f *fakeVMM) GrowVolume(_ context.Context, node Node, id string, size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running[id] && f.volNode[id] == node.ID || f.standbyOn[id] == node.ID {
		return errors.New("grow a running volume")
	}
	f.onNode = append(f.onNode, fmt.Sprintf("grow:%d@%s", size, node.ID))
	return f.growErr
}

func (f *fakeVMM) ArchiveBytes(_ context.Context, node Node, ids []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onNode = append(f.onNode, "size@"+node.ID)
	out := map[string]int64{}
	for _, id := range ids {
		if n, ok := f.archiveBytes[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

func (f *fakeVMM) ListVolumes(_ context.Context, node Node) ([]NodeVolume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onNode = append(f.onNode, "list@"+node.ID)
	if f.down[node.ID] {
		return nil, errNodeDown
	}
	var out []NodeVolume
	for id := range f.volumes {
		if f.volNode[id] == node.ID {
			out = append(out, NodeVolume{DatabaseID: id, Running: f.running[id]})
		}
		if f.standbyVol[id] == node.ID {
			out = append(out, NodeVolume{DatabaseID: id, Running: f.standbyOn[id] == node.ID})
		}
	}
	return out, nil
}

func (f *fakeVMM) nodeCalls() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.onNode, " ")
}

func (f *fakeVMM) Stop(_ context.Context, node Node, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("stop")
	f.onNode = append(f.onNode, "stop@"+node.ID)
	if f.down[node.ID] {
		return errNodeDown
	}
	if f.standbyOn[id] == node.ID {
		delete(f.standbyOn, id)
		return nil
	}
	if on, ok := f.primaryOn[id]; ok && on != node.ID {
		return nil
	}
	delete(f.running, id)
	delete(f.restoring, id)
	delete(f.upgrading, id)
	return nil
}

func (f *fakeVMM) Control(_ context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.controlErr != nil {
		return dbguest.Response{}, f.controlErr
	}
	if f.down[node.ID] {
		return dbguest.Response{}, errNodeDown
	}
	if f.standbyOn[id] == node.ID && f.primaryOn[id] != node.ID {
		resp := f.standbyControl(node, id, req)
		resp.Stale = f.stale[node.ID+"/"+id]
		return resp, nil
	}
	if !f.running[id] {
		return dbguest.Response{}, ErrVMNotRunning
	}
	switch req.Op {
	case dbguest.OpBootstrap:
		f.record("bootstrap")
		f.volumes[id] = true
		f.majors[id] = f.runMajor[id]
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
	case dbguest.OpImportStatus:
		status, ok := f.imports[id]
		if !ok {
			status = noImport
		}
		return dbguest.Response{State: dbguest.StateReady, Import: &status}, nil
	case dbguest.OpFlushWAL:
		if !f.archive {
			return dbguest.Response{State: dbguest.StateReady, Error: dbguest.ErrCodeArchiveOff}, nil
		}
		f.record("flush")
		f.flushes[id] = req.After
		return dbguest.Response{State: dbguest.StateReady}, nil
	case dbguest.OpMarkDrill:
		f.record("mark_drill")
		if f.markErr != "" {
			return dbguest.Response{State: dbguest.StateReady, Error: f.markErr}, nil
		}
		at := f.markAt
		if at.IsZero() {
			at = time.Now()
		}
		f.marks[id] = fakeMark{drill: req.Drill, at: at}
		return dbguest.Response{State: dbguest.StateReady, MarkedAt: at}, nil
	case dbguest.OpCheckDrill:
		f.record("check_drill")
		if f.marks[id].drill != req.Drill {
			return dbguest.Response{State: dbguest.StateReady, Error: dbguest.ErrCodeDrillMarkMissing}, nil
		}
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
	if to, ok := f.upgrading[id]; ok {
		if f.upgradeHold != nil {
			select {
			case <-f.upgradeHold:
			default:
				return dbguest.Response{State: dbguest.StateStarting}, nil
			}
		}
		if f.upgradeCode != "" {
			return dbguest.Response{State: dbguest.StateFailed, Error: f.upgradeCode}, nil
		}
		f.record("upgraded")
		f.majors[id] = to
		delete(f.upgrading, id)
	}
	if major, ok := f.majors[id]; ok && major != f.runMajor[id] {
		return dbguest.Response{State: dbguest.StateFailed, Error: dbguest.ErrCodeMajorMismatch}, nil
	}
	return dbguest.Response{State: dbguest.StateReady, Storage: f.storage[id], Stale: f.stale[node.ID+"/"+id]}, nil
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
	s := New(store, vmm, Config{IdleTimeout: time.Minute, ReadyTimeout: 2 * time.Second, WakeWait: time.Second, WakeQueueWait: 300 * time.Millisecond}, nil)
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
	if _, err := store.AdmitWake(ctx, vm.DatabaseID, false); err != nil {
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

func (f *fakeVMM) CopyVolume(_ context.Context, from, to Node, id string) error {
	f.mu.Lock()
	gate := f.copyGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onNode = append(f.onNode, "copy@"+from.ID+">"+to.ID)
	if f.copyErr != nil {
		return f.copyErr
	}
	if f.running[id] || f.volNode[id] != from.ID {
		return errors.New("volume not stopped on the source")
	}
	if f.copied == nil {
		f.copied = map[string]string{}
	}
	f.copied[id] = to.ID
	return nil
}

func (f *fakeVMM) SyncArchive(_ context.Context, node Node, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onNode = append(f.onNode, "sync@"+node.ID)
	return f.syncErr
}

func (f *fakeVMM) DiscardVolume(_ context.Context, node Node, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onNode = append(f.onNode, "discard@"+node.ID)
	if f.standbyVol[id] == node.ID {
		delete(f.standbyVol, id)
	}
	if f.copied[id] == node.ID {
		delete(f.copied, id)
	}
	return nil
}

// ReleaseVolume drops the source volume; the copy becomes the volume.
func (f *fakeVMM) ReleaseVolume(_ context.Context, node Node, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onNode = append(f.onNode, "release@"+node.ID)
	if to, ok := f.copied[id]; ok && f.volNode[id] == node.ID {
		f.volNode[id] = to
		delete(f.copied, id)
	}
	return nil
}

func (f *fakeVMM) StartStandby(_ context.Context, node, primary Node, vm VM) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("start_standby")
	f.onNode = append(f.onNode, "start_standby@"+node.ID+"<"+primary.ID)
	if f.standbyErr != nil {
		return f.standbyErr
	}
	if f.standbyOn[vm.DatabaseID] != "" {
		return errors.New("standby already running")
	}
	f.standbyOn[vm.DatabaseID] = node.ID
	// A standby boots its node's current base.
	delete(f.stale, node.ID+"/"+vm.DatabaseID)
	return nil
}

func (f *fakeVMM) Replication(_ context.Context, node Node, id string, mode dbguest.ReplicationMode, standbyNode string) (dbguest.ReplicationStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[node.ID] {
		return dbguest.ReplicationStatus{}, errNodeDown
	}
	if !f.running[id] || f.primaryOn[id] != node.ID {
		return dbguest.ReplicationStatus{}, ErrVMNotRunning
	}
	if mode != "" {
		if (mode == dbguest.ReplicationOff) != (standbyNode == "") {
			return dbguest.ReplicationStatus{}, errors.New("standby node goes with async and sync")
		}
		f.replMode[id] = mode
		f.onNode = append(f.onNode, "repl:"+string(mode)+"@"+node.ID)
	}
	status := dbguest.ReplicationStatus{Mode: f.replMode[id], Streaming: f.standbyOn[id] != ""}
	if status.Mode == "" {
		status.Mode = dbguest.ReplicationOff
	}
	status.Synchronous = status.Mode == dbguest.ReplicationSync && status.Streaming && !f.unsynced[id]
	return status, nil
}

// standbyControl answers a standby's guest; f.mu is held.
func (f *fakeVMM) standbyControl(node Node, id string, req dbguest.Request) dbguest.Response {
	switch req.Op {
	case dbguest.OpPromote:
		if f.promoteErr != "" {
			return dbguest.Response{State: dbguest.StateStandby, Error: f.promoteErr}
		}
		// The standby becomes the database's VM on its node.
		f.record("promote")
		f.promoted[id] = node.ID
		f.primaryOn[id], f.running[id] = node.ID, true
		delete(f.standbyOn, id)
		delete(f.replMode, id)
		return dbguest.Response{State: dbguest.StateReady}
	case dbguest.OpStatus:
		if f.promoted[id] == node.ID {
			return dbguest.Response{State: dbguest.StateReady}
		}
		return dbguest.Response{State: dbguest.StateStandby, Storage: f.standbyStorage[id]}
	}
	return dbguest.Response{State: dbguest.StateStandby, Error: dbguest.ErrCodeNotReady}
}

// replicationOf reports the fake primary's mode and standby node.
func (f *fakeVMM) replicationOf(id string) (dbguest.ReplicationMode, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replMode[id], f.standbyOn[id]
}

func (f *fakeVMM) Import(_ context.Context, node Node, id string, spec ImportSpec) (dbguest.ImportStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running[id] || f.primaryOn[id] != node.ID {
		return noImport, ErrVMNotRunning
	}
	f.onNode = append(f.onNode, fmt.Sprintf("import:%s:%d@%s", spec.Host, spec.Port, node.ID))
	if status, ok := f.imports[id]; ok && status.State != dbguest.ImportFailed {
		return status, nil
	}
	f.imports[id] = dbguest.ImportStatus{State: dbguest.ImportRunning}
	return f.imports[id], nil
}

// setImport sets what the fake guest reports for a database's import.
func (f *fakeVMM) setImport(id string, state dbguest.ImportState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imports[id] = dbguest.ImportStatus{State: state}
}

// errNodeDown is what gRPC reports for a node that does not answer.
var errNodeDown = status.Error(codes.Unavailable, "connection refused")

// setDown makes a node answer or not.
func (f *fakeVMM) setDown(node string, down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down[node] = down
}
