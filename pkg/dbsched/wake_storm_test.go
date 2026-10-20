// adr: 1027 — wake storms: boot slots cap a node's concurrent boots, wakes
// over the RAM ceiling or boot slots queue, and an idle database nobody is
// connected to is parked to make room.
package dbsched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStoreAdmitWakeHonoursBootSlots(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 1 << 20, VolumeCapacityBytes: 100 << 30, BootSlots: 3, Lifecycle: NodeActive})
			if nodes, err := s.Nodes(ctx); err != nil || nodes[0].BootSlots != 3 {
				t.Fatalf("nodes = %+v, %v", nodes, err)
			}
			var ids []string
			for i := 0; i < 8; i++ {
				vm, err := s.Place(ctx, newVM(1<<30, 384))
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, vm.DatabaseID)
			}
			// Concurrent admissions never take more slots than the node has.
			var wg sync.WaitGroup
			var mu sync.Mutex
			var admitted []string
			for _, id := range ids {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					_, err := s.AdmitWake(ctx, id, false)
					mu.Lock()
					defer mu.Unlock()
					switch {
					case err == nil:
						admitted = append(admitted, id)
					case !errors.Is(err, ErrBootSlotsBusy):
						t.Errorf("admit %s = %v, want ErrBootSlotsBusy", id, err)
					}
				}(id)
			}
			wg.Wait()
			if len(admitted) != 3 {
				t.Fatalf("%d databases admitted to boot on a node with 3 boot slots", len(admitted))
			}
			// A finished boot frees its slot; running databases take none.
			if err := s.Transition(ctx, admitted[0], []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			waiting := ""
			for _, id := range ids {
				if vm, _ := s.Get(ctx, id); vm.State == StateParked {
					waiting = id
					break
				}
			}
			if vm, err := s.AdmitWake(ctx, waiting, false); err != nil || vm.State != StateWaking {
				t.Fatalf("admit after a boot finished = %+v, %v", vm, err)
			}
		})
	}
}

func TestStoreBootSlotsDefault(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive})
			if nodes, err := s.Nodes(ctx); err != nil || nodes[0].BootSlots != DefaultBootSlots {
				t.Fatalf("nodes = %+v, %v; want %d boot slots", nodes, err, DefaultBootSlots)
			}
		})
	}
}

func TestWakeStormBootsWithinSlotsAndAllWake(t *testing.T) {
	store, vmm := NewMemStore(), newFakeVMM()
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 1 << 20, VolumeCapacityBytes: 100 << 30, BootSlots: 2, Lifecycle: NodeActive})
	s := New(store, vmm, Config{ReadyTimeout: 2 * time.Second, WakeWait: time.Second}, nil)
	var vms []VM
	for i := 0; i < 6; i++ {
		vms = append(vms, provision(t, s))
	}
	vmm.startDelay = 40 * time.Millisecond
	var wg sync.WaitGroup
	errs := make(chan error, len(vms))
	for _, vm := range vms {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := s.Wake(context.Background(), id); err != nil {
				errs <- err
			}
		}(vm.DatabaseID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("wake in a storm: %v", err)
	}
	if vmm.maxBooting != 2 {
		t.Fatalf("at most %d boots at once; want the node's 2 boot slots in use", vmm.maxBooting)
	}
	running, _ := store.List(context.Background(), StateRunning)
	if len(running) != len(vms) {
		t.Fatalf("%d of %d databases running after the storm", len(running), len(vms))
	}
}

func TestWakeQueuesUntilRAMFrees(t *testing.T) {
	s, store, _ := newTestScheduler(t, 500) // fits one 384 MiB VM
	s.cfg.WakeQueueWait = 5 * time.Second
	ctx := context.Background()
	a, b := provision(t, s), provision(t, s)
	if _, err := s.Wake(ctx, a.DatabaseID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Wake(ctx, b.DatabaseID)
		done <- err
	}()
	time.Sleep(150 * time.Millisecond)
	if got, _ := store.Get(ctx, b.DatabaseID); got.State != StateParked {
		t.Fatalf("queued database = %+v, want parked while it waits", got)
	}
	if err := s.Park(ctx, a.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("queued wake = %v", err)
	}
	if got, _ := store.Get(ctx, b.DatabaseID); got.State != StateRunning {
		t.Fatalf("queued database = %+v, want running", got)
	}
}

func TestWakeEvictsIdleDatabaseOverRAM(t *testing.T) {
	for _, tc := range []struct {
		name     string
		alwaysOn bool
		idleFor  time.Duration
		evict    time.Duration
		wantWake error
	}{
		{"idle past the eviction age", false, 5 * time.Minute, 0, nil},
		{"recently active", false, 30 * time.Second, 0, ErrNoCapacity},
		{"always on", true, 5 * time.Minute, 0, ErrNoCapacity},
		{"eviction disabled", false, 5 * time.Minute, -1, ErrNoCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, vmm := newTestScheduler(t, 500) // fits one 384 MiB VM
			s.cfg.EvictIdleAfter = tc.evict
			if tc.evict == 0 {
				s.cfg.EvictIdleAfter = 2 * time.Minute
			}
			ctx := context.Background()
			a, b := provision(t, s), provision(t, s)
			if _, err := s.Wake(ctx, a.DatabaseID); err != nil {
				t.Fatal(err)
			}
			if tc.alwaysOn {
				if err := store.SetAlwaysOn(ctx, a.DatabaseID, true); err != nil {
					t.Fatal(err)
				}
			}
			_ = store.Touch(ctx, a.DatabaseID, time.Now().Add(-tc.idleFor))
			_, err := s.Wake(ctx, b.DatabaseID)
			if !errors.Is(err, tc.wantWake) && (tc.wantWake != nil || err != nil) {
				t.Fatalf("wake = %v, want %v", err, tc.wantWake)
			}
			gotA, _ := store.Get(ctx, a.DatabaseID)
			gotB, _ := store.Get(ctx, b.DatabaseID)
			if tc.wantWake == nil {
				if gotA.State != StateParked || vmm.running[a.DatabaseID] || gotB.State != StateRunning {
					t.Fatalf("after eviction a=%s b=%s", gotA.State, gotB.State)
				}
				return
			}
			if gotA.State != StateRunning || gotB.State != StateParked {
				t.Fatalf("without eviction a=%s b=%s", gotA.State, gotB.State)
			}
		})
	}
}

func TestEvictionSkipsBusyAndOtherNodes(t *testing.T) {
	store, vmm := NewMemStore(), newFakeVMM()
	seedNodes(t, store,
		Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 500, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive},
		Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 500, VolumeCapacityBytes: 1 << 30, Lifecycle: NodeActive},
	)
	s := New(store, vmm, Config{ReadyTimeout: 2 * time.Second, WakeWait: time.Second, WakeQueueWait: 300 * time.Millisecond}, nil)
	ctx := context.Background()
	other := provisionOn(t, s, "db-b")
	if _, err := s.Wake(ctx, other.DatabaseID); err != nil {
		t.Fatal(err)
	}
	_ = store.Touch(ctx, other.DatabaseID, time.Now().Add(-time.Hour))
	a, b := provisionOn(t, s, "db-a"), provisionOn(t, s, "db-a")
	if _, err := s.Wake(ctx, a.DatabaseID); err != nil {
		t.Fatal(err)
	}
	_ = store.Touch(ctx, a.DatabaseID, time.Now().Add(-time.Hour))
	// a's lock is held, as by an in-flight backup: it is not evicted.
	unlock := s.lock(a.DatabaseID)
	if _, err := s.Wake(ctx, b.DatabaseID); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("wake with only a busy candidate = %v", err)
	}
	unlock()
	if got, _ := store.Get(ctx, other.DatabaseID); got.State != StateRunning {
		t.Fatalf("a database on another node was evicted: %+v", got)
	}
	if _, err := s.Wake(ctx, b.DatabaseID); err != nil {
		t.Fatalf("wake once the candidate is free = %v", err)
	}
}

// provisionOn provisions a database pinned to a node.
func provisionOn(t *testing.T, s *Scheduler, node string) VM {
	t.Helper()
	vm, err := s.store.Place(context.Background(), VM{DatabaseID: uuid.NewString(), NodeID: node, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	vm, err = s.Provision(context.Background(), ProvisionRequest{DatabaseID: vm.DatabaseID, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil || vm.NodeID != node {
		t.Fatalf("provision on %s = %+v, %v", node, vm, err)
	}
	return vm
}
