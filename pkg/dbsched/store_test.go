// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// storeFactories runs every conformance case against MemStore and, when
// DATABASE_URL is set, against PgStore on a fresh migrated database.
func storeFactories(t *testing.T) map[string]func(t *testing.T) Store {
	return map[string]func(t *testing.T) Store{
		"mem": func(*testing.T) Store { return NewMemStore() },
		"pg": func(t *testing.T) Store {
			pool := pgtest.Open(t)
			if err := db.MigrateUp(context.Background(), pool); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			s, err := NewPgStore(pool)
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
	}
}

func seedNodes(t *testing.T, s Store, nodes ...Node) {
	t.Helper()
	for _, n := range nodes {
		if err := s.UpsertNode(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
}

func newVM(volume int64, mem int) VM {
	return VM{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: mem, VolumeBytes: volume}
}

func TestStorePlacement(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive},
				Node{ID: "db-c", TargetURL: "unix:///c", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeDraining},
			)
			first, err := s.Place(ctx, newVM(8<<30, 384))
			if err != nil || first.NodeID != "db-b" || first.State != StateProvisioning || first.Bootstrapped {
				t.Fatalf("first placement = %+v, %v; want provisioning on db-b (most free, draining skipped)", first, err)
			}
			again, err := s.Place(ctx, VM{DatabaseID: first.DatabaseID, Major: 16, VolumeBytes: 1})
			if err != nil || again.NodeID != first.NodeID || again.Major != 17 {
				t.Fatalf("re-place = %+v, %v; want the existing row unchanged", again, err)
			}
			second, err := s.Place(ctx, newVM(9<<30, 384))
			if err != nil || second.NodeID != "db-b" {
				t.Fatalf("second placement = %+v, %v; want db-b (12 GiB free vs 10)", second, err)
			}
			third, err := s.Place(ctx, newVM(9<<30, 384))
			if err != nil || third.NodeID != "db-a" {
				t.Fatalf("third placement = %+v, %v; want db-a", third, err)
			}
			if _, err := s.Place(ctx, newVM(5<<30, 384)); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("overfull placement = %v, want ErrNoCapacity", err)
			}
		})
	}
}

func TestStoreAdmitWakeEnforcesNodeRAM(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			// 800 MB fits two 384 MiB VMs (392 MB billable each), not three.
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 800, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive})
			var ids []string
			for i := 0; i < 3; i++ {
				vm, err := s.Place(ctx, newVM(1<<30, 384))
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, vm.DatabaseID)
			}
			for _, id := range ids[:2] {
				if vm, err := s.AdmitWake(ctx, id); err != nil || vm.State != StateWaking {
					t.Fatalf("admit %s = %+v, %v", id, vm, err)
				}
			}
			if _, err := s.AdmitWake(ctx, ids[2]); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("third admit = %v, want ErrNoCapacity", err)
			}
			if vm, err := s.AdmitWake(ctx, ids[0]); err != nil || vm.State != StateWaking {
				t.Fatalf("re-admit of a waking database = %+v, %v; want unchanged", vm, err)
			}
			if err := s.Transition(ctx, ids[0], []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, ids[0], []State{StateRunning}, StateParked, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if vm, err := s.AdmitWake(ctx, ids[2]); err != nil || vm.State != StateWaking {
				t.Fatalf("admit after a park = %+v, %v", vm, err)
			}
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 800, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeUnavailable})
			if _, err := s.AdmitWake(ctx, ids[0]); !errors.Is(err, ErrNodeUnavailable) {
				t.Fatalf("admit on an unavailable node = %v, want ErrNodeUnavailable", err)
			}
		})
	}
}

func TestStoreConcurrentAdmissionNeverOvercommits(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 400, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive})
			var ids []string
			for i := 0; i < 8; i++ {
				vm, err := s.Place(ctx, newVM(1<<30, 384))
				if err != nil {
					t.Fatal(err)
				}
				_ = s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{})
				ids = append(ids, vm.DatabaseID)
			}
			var wg sync.WaitGroup
			var mu sync.Mutex
			admitted := 0
			for _, id := range ids {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					if _, err := s.AdmitWake(ctx, id); err == nil {
						mu.Lock()
						admitted++
						mu.Unlock()
					}
				}(id)
			}
			wg.Wait()
			if admitted != 1 {
				t.Fatalf("%d databases admitted under a ceiling that fits one", admitted)
			}
		})
	}
}

func TestStoreTransitionsTouchAndDelete(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive})
			vm, err := s.Place(ctx, newVM(1<<30, 384))
			if err != nil {
				t.Fatal(err)
			}
			id := vm.DatabaseID
			if err := s.Transition(ctx, id, []State{StateParked}, StateRunning, TransitionOptions{}); !errors.Is(err, ErrConflict) {
				t.Fatalf("CAS from the wrong state = %v, want ErrConflict", err)
			}
			if err := s.Transition(ctx, uuid.NewString(), []State{StateParked}, StateRunning, TransitionOptions{}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("transition of a missing database = %v, want ErrNotFound", err)
			}
			if err := s.Transition(ctx, id, []State{StateProvisioning}, StateFailed, TransitionOptions{LastError: "boot_failed", Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, id); got.LastError != "boot_failed" || !got.Bootstrapped {
				t.Fatalf("after failure = %+v", got)
			}
			if err := s.Transition(ctx, id, []State{StateFailed}, StateParked, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, id); got.LastError != "" || !got.Bootstrapped {
				t.Fatalf("bootstrapped must stick and the error clear: %+v", got)
			}
			if err := s.Touch(ctx, id, time.Now()); !errors.Is(err, ErrConflict) {
				t.Fatalf("touch of a parked database = %v, want ErrConflict", err)
			}
			if err := s.Transition(ctx, id, []State{StateParked}, StateRunning, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, id); got.LastActiveAt.IsZero() {
				t.Fatal("running did not stamp last_active_at")
			}
			later := time.Now().Add(time.Hour).Truncate(time.Microsecond)
			if err := s.Touch(ctx, id, later); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, id); !got.LastActiveAt.Equal(later) {
				t.Fatalf("touch = %v, want %v", got.LastActiveAt, later)
			}
			if running, _ := s.List(ctx, StateRunning); len(running) != 1 || running[0].DatabaseID != id {
				t.Fatalf("list running = %+v", running)
			}
			if err := s.Delete(ctx, id); !errors.Is(err, ErrConflict) {
				t.Fatalf("delete of a running database = %v, want ErrConflict", err)
			}
			if err := s.Transition(ctx, id, []State{StateRunning}, StateDeleting, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("get after delete = %v", err)
			}
			if err := s.Delete(ctx, id); err != nil {
				t.Fatalf("deleting a missing database must succeed: %v", err)
			}
		})
	}
}

// adr: 1027 — always-on is placed with the database and toggled in place;
// a deleting database cannot change.
func TestStoreAlwaysOn(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive})
			want := newVM(1<<30, 384)
			want.AlwaysOn = true
			vm, err := s.Place(ctx, want)
			if err != nil || !vm.AlwaysOn {
				t.Fatalf("Place always-on = %+v, %v", vm, err)
			}
			id := vm.DatabaseID
			if err := s.SetAlwaysOn(ctx, id, false); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, id); got.AlwaysOn {
				t.Fatal("always-on did not turn off")
			}
			if err := s.SetAlwaysOn(ctx, uuid.NewString(), true); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing database = %v, want ErrNotFound", err)
			}
			if err := s.Transition(ctx, id, []State{StateProvisioning}, StateDeleting, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.SetAlwaysOn(ctx, id, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("deleting database = %v, want ErrConflict", err)
			}
		})
	}
}
