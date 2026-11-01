// adr: 1027 — moving a database to another node: the volume is copied while
// it is parked and the record switched only after the copy and the archive
// handoff; any failure leaves it parked where it was.
package dbsched

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStoreMoveLifecycle(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive},
				Node{ID: "db-c", TargetURL: "unix:///c", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeDraining},
			)
			vm := newVM(4<<30, 384)
			vm.NodeID = "db-a"
			vm, err := s.Place(ctx, vm)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.BeginMove(ctx, vm.DatabaseID, "", time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("move of a provisioning database = %v, want ErrConflict", err)
			}
			if err := s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BeginMove(ctx, vm.DatabaseID, "db-a", time.Minute); !errors.Is(err, ErrInvalid) {
				t.Fatalf("move onto its own node = %v, want ErrInvalid", err)
			}
			moving, err := s.BeginMove(ctx, vm.DatabaseID, "", time.Minute)
			if err != nil || moving.State != StateMoving || moving.MoveNode != "db-b" || moving.MoveLeaseUntil.IsZero() || moving.NodeID != "db-a" {
				t.Fatalf("begin move = %+v, %v; want moving to db-b (db-c is draining)", moving, err)
			}
			// The volume counts on both nodes while it moves.
			if _, err := s.Place(ctx, VM{DatabaseID: uuid.NewString(), NodeID: "db-b", Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 7 << 30}); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("placement into the space a move reserved = %v, want ErrNoCapacity", err)
			}
			if _, err := s.AdmitWake(ctx, vm.DatabaseID, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("wake while moving = %v, want ErrConflict", err)
			}
			if _, err := s.BeginMove(ctx, vm.DatabaseID, "", time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("second move = %v, want ErrConflict", err)
			}
			if err := s.AbortMove(ctx, vm.DatabaseID, time.Time{}, errCodeMove); !errors.Is(err, ErrConflict) {
				t.Fatalf("abort of a live lease without holding it = %v, want ErrConflict", err)
			}
			stale := moving.MoveLeaseUntil
			held, err := s.RenewMove(ctx, vm.DatabaseID, stale, time.Minute)
			if err != nil || !held.After(stale) && !held.Equal(stale) {
				t.Fatalf("renew = %v, %v", held, err)
			}
			if _, err := s.RenewMove(ctx, vm.DatabaseID, stale.Add(-time.Second), time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("renew with a lease not held = %v, want ErrConflict", err)
			}
			if err := s.FinishMove(ctx, vm.DatabaseID, held.Add(-time.Second)); !errors.Is(err, ErrConflict) {
				t.Fatalf("finish with a lease not held = %v, want ErrConflict", err)
			}
			if err := s.FinishMove(ctx, vm.DatabaseID, held); err != nil {
				t.Fatal(err)
			}
			got, _ := s.Get(ctx, vm.DatabaseID)
			if got.NodeID != "db-b" || got.State != StateParked || got.MoveNode != "" || !got.MoveLeaseUntil.IsZero() {
				t.Fatalf("after the switch = %+v", got)
			}
			// Back again, aborted by the holder, then by anyone once lapsed.
			moving, err = s.BeginMove(ctx, vm.DatabaseID, "db-a", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.AbortMove(ctx, vm.DatabaseID, moving.MoveLeaseUntil, errCodeMove); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, vm.DatabaseID); got.NodeID != "db-b" || got.State != StateParked || got.LastError != errCodeMove {
				t.Fatalf("after the abort = %+v", got)
			}
			if _, err := s.BeginMove(ctx, vm.DatabaseID, "db-a", time.Millisecond); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
			if err := s.AbortMove(ctx, vm.DatabaseID, time.Time{}, errCodeMove); err != nil {
				t.Fatalf("abort of a lapsed move = %v", err)
			}
		})
	}
}

// newMoveScheduler is a scheduler over two active nodes with a shared
// archive.
func newMoveScheduler(t *testing.T) (*Scheduler, *MemStore, *fakeVMM) {
	t.Helper()
	store, vmm := NewMemStore(), newFakeVMM()
	seedNodes(t, store,
		Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive},
		Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive},
	)
	s := New(store, vmm, Config{ReadyTimeout: 2 * time.Second, WakeWait: time.Second, WakeQueueWait: 300 * time.Millisecond, SharedArchive: true}, nil)
	return s, store, vmm
}

func calls(vmm *fakeVMM, prefixes ...string) []string {
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	var out []string
	for _, c := range vmm.onNode {
		for _, p := range prefixes {
			if strings.HasPrefix(c, p) {
				out = append(out, c)
			}
		}
	}
	return out
}

func TestMoveParkedDatabase(t *testing.T) {
	s, _, vmm := newMoveScheduler(t)
	ctx := context.Background()
	vm := provisionOn(t, s, "db-a")
	moved, err := s.Move(ctx, vm.DatabaseID, "db-b")
	if err != nil || moved.NodeID != "db-b" || moved.State != StateParked || moved.NodeTarget != "unix:///b" {
		t.Fatalf("move = %+v, %v; want parked on db-b", moved, err)
	}
	want := []string{"discard@db-b", "copy@db-a>db-b", "sync@db-a", "release@db-a"}
	if got := calls(vmm, "discard@", "copy@", "sync@", "release@"); !slices.Equal(got, want) {
		t.Fatalf("move steps = %v, want %v", got, want)
	}
	if vmm.volNode[vm.DatabaseID] != "db-b" || len(vmm.copied) != 0 {
		t.Fatalf("volume on %s, copies %v", vmm.volNode[vm.DatabaseID], vmm.copied)
	}
	woke, err := s.Wake(ctx, vm.DatabaseID)
	if err != nil || woke.State != StateRunning || woke.NodeTarget != "unix:///b" {
		t.Fatalf("wake after the move = %+v, %v", woke, err)
	}
}

func TestMoveRunningDatabaseWakesOnItsNewNode(t *testing.T) {
	s, _, vmm := newMoveScheduler(t)
	ctx := context.Background()
	vm := provisionOn(t, s, "db-a")
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	moved, err := s.Move(ctx, vm.DatabaseID, "")
	if err != nil || moved.NodeID != "db-b" || moved.State != StateRunning {
		t.Fatalf("move of a running database = %+v, %v; want running on db-b", moved, err)
	}
	if !vmm.running[vm.DatabaseID] || vmm.volNode[vm.DatabaseID] != "db-b" {
		t.Fatalf("running=%v volume on %s", vmm.running[vm.DatabaseID], vmm.volNode[vm.DatabaseID])
	}
}

func TestMoveFailureLeavesTheDatabaseWhereItWas(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(*fakeVMM)
	}{
		{"copy fails", func(v *fakeVMM) { v.copyErr = errors.New("import digest mismatch") }},
		{"archive handoff fails", func(v *fakeVMM) { v.syncErr = errors.New("bucket down") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, vmm := newMoveScheduler(t)
			ctx := context.Background()
			vm := provisionOn(t, s, "db-a")
			if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
				t.Fatal(err)
			}
			tc.fail(vmm)
			after, err := s.Move(ctx, vm.DatabaseID, "db-b")
			if err == nil {
				t.Fatal("a failed move reported success")
			}
			got, _ := store.Get(ctx, vm.DatabaseID)
			if after.NodeID != "db-a" || got.NodeID != "db-a" || got.State != StateRunning || got.MoveNode != "" {
				t.Fatalf("after a failed move = %+v; want running again on db-a", got)
			}
			if steps := calls(vmm, "discard@", "release@"); steps[len(steps)-1] != "discard@db-b" || slices.Contains(steps, "release@db-a") {
				t.Fatalf("cleanup = %v; want the copy discarded and the source kept", steps)
			}
			if vmm.volNode[vm.DatabaseID] != "db-a" || len(vmm.copied) != 0 {
				t.Fatalf("volume on %s, copies %v", vmm.volNode[vm.DatabaseID], vmm.copied)
			}
		})
	}
}

func TestMoveGuards(t *testing.T) {
	s, store, _ := newMoveScheduler(t)
	ctx := context.Background()
	vm := provisionOn(t, s, "db-a")
	s.cfg.SharedArchive = false
	if _, err := s.Move(ctx, vm.DatabaseID, "db-b"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move without a shared archive = %v, want ErrInvalid", err)
	}
	s.cfg.SharedArchive = true
	if _, err := s.Move(ctx, vm.DatabaseID, "db-z"); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("move to an unknown node = %v, want ErrNoCapacity", err)
	}
	_ = store.Transition(ctx, vm.DatabaseID, []State{StateParked}, StateFailed, TransitionOptions{})
	if _, err := s.Move(ctx, vm.DatabaseID, "db-b"); !errors.Is(err, ErrConflict) {
		t.Fatalf("move of a failed database = %v, want ErrConflict", err)
	}
}

func TestSettleMovesAbortsAnAbandonedMove(t *testing.T) {
	s, store, vmm := newMoveScheduler(t)
	ctx := context.Background()
	vm := provisionOn(t, s, "db-a")
	// A replica began this move and died: its lease lapses unrenewed.
	if _, err := store.BeginMove(ctx, vm.DatabaseID, "db-b", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wake while moving = %v, want ErrConflict", err)
	}
	time.Sleep(10 * time.Millisecond)
	n, err := s.SettleMoves(ctx)
	if err != nil || n != 1 {
		t.Fatalf("settle = %d, %v", n, err)
	}
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateParked || got.NodeID != "db-a" || got.LastError != errCodeMove {
		t.Fatalf("after settling = %+v", got)
	}
	if !slices.Contains(calls(vmm, "discard@"), "discard@db-b") {
		t.Fatal("the abandoned copy was not discarded")
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatalf("wake after settling = %v", err)
	}
}

func TestEvacuateDrainingNode(t *testing.T) {
	s, store, vmm := newMoveScheduler(t)
	s.cfg.EvacuateDraining = true
	ctx := context.Background()
	parked1, parked2 := provisionOn(t, s, "db-a"), provisionOn(t, s, "db-a")
	busy, alwaysOn := provisionOn(t, s, "db-a"), provisionOn(t, s, "db-a")
	if err := store.SetAlwaysOn(ctx, alwaysOn.DatabaseID, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{busy.DatabaseID, alwaysOn.DatabaseID} {
		if _, err := s.Wake(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.EvacuateDraining(ctx); err != nil || n != 0 {
		t.Fatalf("evacuation with no draining node = %d, %v", n, err)
	}
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeDraining})
	n, err := s.EvacuateDraining(ctx)
	if err != nil || n != 3 {
		t.Fatalf("evacuation = %d, %v; want the two parked and the always-on database", n, err)
	}
	for id, want := range map[string]string{parked1.DatabaseID: "db-b", parked2.DatabaseID: "db-b", alwaysOn.DatabaseID: "db-b", busy.DatabaseID: "db-a"} {
		if got, _ := store.Get(ctx, id); got.NodeID != want {
			t.Errorf("%s on %s, want %s", id, got.NodeID, want)
		}
	}
	if got, _ := store.Get(ctx, alwaysOn.DatabaseID); got.State != StateRunning || !vmm.running[alwaysOn.DatabaseID] {
		t.Fatalf("the always-on database after its move = %+v", got)
	}
	// The running one moves once it parks.
	if err := s.Park(ctx, busy.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.EvacuateDraining(ctx); err != nil || n != 1 {
		t.Fatalf("second sweep = %d, %v", n, err)
	}
}

func TestCollectOrphansReleasesAMovedDatabasesOldVolume(t *testing.T) {
	s, _, vmm := newMoveScheduler(t)
	ctx := context.Background()
	vm := provisionOn(t, s, "db-a")
	if _, err := s.Move(ctx, vm.DatabaseID, "db-b"); err != nil {
		t.Fatal(err)
	}
	// The release after the switch failed: the old volume is still there.
	vmm.mu.Lock()
	vmm.volNode[vm.DatabaseID], vmm.copied = "db-a", map[string]string{vm.DatabaseID: "db-b"}
	vmm.onNode = nil
	vmm.mu.Unlock()
	n, err := s.CollectOrphans(ctx)
	if err != nil || n != 1 {
		t.Fatalf("collect = %d, %v", n, err)
	}
	if got := calls(vmm, "release@", "delete@"); !slices.Equal(got, []string{"release@db-a"}) {
		t.Fatalf("cleanup = %v; want the old volume released, never its archive dropped", got)
	}
}

// adr: 1027 — a database lands only on a node with its major's base.
func TestStorePlacesByPostgresMajor(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 50 << 30, Majors: []int{17}, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive},
				Node{ID: "db-c", TargetURL: "unix:///c", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Majors: []int{14, 16}, Lifecycle: NodeActive},
			)
			nodes, err := s.Nodes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				if n.ID == "db-c" && !slices.Equal(n.Majors, []int{14, 16}) || n.ID == "db-b" && len(n.Majors) != 0 {
					t.Fatalf("node %s majors = %v", n.ID, n.Majors)
				}
			}
			place := func(major int) VM {
				t.Helper()
				vm := newVM(1<<30, 384)
				vm.Major = major
				got, err := s.Place(ctx, vm)
				if err != nil {
					t.Fatalf("place major %d: %v", major, err)
				}
				return got
			}
			for major, want := range map[int]string{14: "db-c", 15: "db-b", 17: "db-a"} {
				if got := place(major); got.NodeID != want {
					t.Errorf("major %d placed on %s, want %s", major, got.NodeID, want)
				}
			}
			// A major-15 database can only move to a node offering 15; db-b
			// is the only one, and it is already there.
			vm := place(15)
			if err := s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BeginMove(ctx, vm.DatabaseID, "", time.Minute); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("move with no node offering its major = %v, want ErrNoCapacity", err)
			}
			if _, err := s.BeginMove(ctx, vm.DatabaseID, "db-a", time.Minute); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("move onto a node without its major = %v, want ErrNoCapacity", err)
			}
		})
	}
}

// An operator's move returns once the database is recorded moving and
// finishes in the background; a second request while it moves is busy.
func TestStartMoveFinishesInTheBackground(t *testing.T) {
	s, store, vmm := newMoveScheduler(t)
	s.cfg.StartMoveWait = 100 * time.Millisecond
	ctx := context.Background()
	vm := provisionOn(t, s, "db-a")
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	vmm.mu.Lock()
	vmm.copyGate = release
	vmm.mu.Unlock()
	moving, err := s.StartMove(ctx, vm.DatabaseID, "")
	if err != nil || moving.State != StateMoving || moving.MoveNode != "db-b" || moving.NodeID != "db-a" {
		t.Fatalf("start move = %+v, %v; want moving from db-a to db-b", moving, err)
	}
	if _, err := s.StartMove(ctx, vm.DatabaseID, "db-a"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second move while copying = %v, want ErrConflict", err)
	}
	close(release)
	s.moveWG.Wait()
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.NodeID != "db-b" || got.State != StateRunning || got.MoveNode != "" {
		t.Fatalf("after the background move: %+v; want running on db-b", got)
	}
}

func TestStartMoveRefusesWhatMoveRefuses(t *testing.T) {
	s, _, _ := newMoveScheduler(t)
	ctx := context.Background()
	vm := provisionOn(t, s, "db-a")
	if _, err := s.StartMove(ctx, vm.DatabaseID, "db-a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move onto its own node = %v, want ErrInvalid", err)
	}
	if _, err := s.StartMove(ctx, uuid.NewString(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("move of an unknown database = %v, want ErrNotFound", err)
	}
	s.cfg.SharedArchive = false
	if _, err := s.StartMove(ctx, vm.DatabaseID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move without a shared archive = %v, want ErrInvalid", err)
	}
}
