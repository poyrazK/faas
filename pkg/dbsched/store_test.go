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
				if vm, err := s.AdmitWake(ctx, id, false); err != nil || vm.State != StateWaking {
					t.Fatalf("admit %s = %+v, %v", id, vm, err)
				}
			}
			if _, err := s.AdmitWake(ctx, ids[2], false); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("third admit = %v, want ErrNoCapacity", err)
			}
			if vm, err := s.AdmitWake(ctx, ids[0], false); err != nil || vm.State != StateWaking {
				t.Fatalf("re-admit of a waking database = %+v, %v; want unchanged", vm, err)
			}
			if err := s.Transition(ctx, ids[0], []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, ids[0], []State{StateRunning}, StateParked, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if vm, err := s.AdmitWake(ctx, ids[2], false); err != nil || vm.State != StateWaking {
				t.Fatalf("admit after a park = %+v, %v", vm, err)
			}
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 800, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeUnavailable})
			if _, err := s.AdmitWake(ctx, ids[0], false); !errors.Is(err, ErrNodeUnavailable) {
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
					if _, err := s.AdmitWake(ctx, id, false); err == nil {
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

// TestStoreRestorePlacementAndLease pins a restore to its node, keeps its
// lineage and lets one holder at a time run it.
func TestStoreRestorePlacementAndLease(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 50 << 30, Lifecycle: NodeActive},
			)
			source := newVM(1<<30, 384)
			restored := newVM(1<<30, 384)
			restored.NodeID, restored.RestoreSource, restored.RestoreWindowSeconds = "db-a", source.DatabaseID, 86400
			restored.RestoreTarget = time.Date(2026, 10, 13, 9, 30, 15, 123456000, time.UTC)
			vm, err := s.Place(ctx, restored)
			if err != nil || vm.NodeID != "db-a" {
				t.Fatalf("pinned placement = %+v, %v; want db-a despite db-b's free space", vm, err)
			}
			got, err := s.Get(ctx, vm.DatabaseID)
			if err != nil || got.RestoreSource != source.DatabaseID || !got.RestoreTarget.Equal(restored.RestoreTarget) || got.RestoreWindowSeconds != 86400 {
				t.Fatalf("lineage = %+v, %v", got, err)
			}
			pinnedFull := newVM(20<<30, 384)
			pinnedFull.NodeID = "db-a"
			if _, err := s.Place(ctx, pinnedFull); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("pinned to a full node = %v, want ErrNoCapacity", err)
			}

			lease, err := s.ClaimRestore(ctx, vm.DatabaseID, time.Time{}, time.Minute)
			if err != nil || lease.IsZero() {
				t.Fatalf("claim = %v, %v", lease, err)
			}
			if _, err := s.ClaimRestore(ctx, vm.DatabaseID, time.Time{}, time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("second claim = %v, want ErrConflict", err)
			}
			renewed, err := s.ClaimRestore(ctx, vm.DatabaseID, lease, time.Minute)
			if err != nil || renewed.Before(lease) {
				t.Fatalf("renew = %v, %v", renewed, err)
			}
			if _, err := s.ClaimRestore(ctx, vm.DatabaseID, lease, time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("renew with a stale lease = %v, want ErrConflict", err)
			}
			short, err := s.ClaimRestore(ctx, vm.DatabaseID, renewed, time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
			if _, err := s.ClaimRestore(ctx, vm.DatabaseID, time.Time{}, time.Minute); err != nil {
				t.Fatalf("claim after the lease %v lapsed = %v", short, err)
			}
			recovered := restored.RestoreTarget.Add(-time.Second)
			if err := s.RecordRestore(ctx, vm.DatabaseID, recovered); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, vm.DatabaseID); !got.RestoreRecoveredTo.Equal(recovered) {
				t.Fatalf("recovered point = %v", got.RestoreRecoveredTo)
			}
			plain, err := s.Place(ctx, newVM(1<<30, 384))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.RecordRestore(ctx, plain.DatabaseID, recovered); !errors.Is(err, ErrConflict) {
				t.Fatalf("recording a restore point on a plain database = %v", err)
			}
			if _, err := s.ClaimRestore(ctx, plain.DatabaseID, time.Time{}, time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("claim on a database that is not a restore = %v", err)
			}
			if _, err := s.ClaimRestore(ctx, uuid.NewString(), time.Time{}, time.Minute); !errors.Is(err, ErrNotFound) {
				t.Fatalf("claim on a missing database = %v", err)
			}
		})
	}
}

// TestStoreUsageLedger: storage from placement to deletion, compute while
// the VM is active, sampled history and hourly egress, all answerable after
// the record is gone.
func TestStoreUsageLedger(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive})
			if _, err := s.Usage(ctx, uuid.NewString(), time.Now().Add(-time.Hour), time.Now()); !errors.Is(err, ErrNotFound) {
				t.Fatalf("usage of an unknown database = %v", err)
			}
			vm := newVM(1<<30, 384)
			vm.VcpuCount = 2
			t0 := time.Now()
			if _, err := s.Place(ctx, vm); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AdmitWake(ctx, vm.DatabaseID, false); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, vm.DatabaseID, []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(1100 * time.Millisecond)
			if err := s.SampleUsage(ctx, vm.DatabaseID, MeterHistory, 1000, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := s.SampleUsage(ctx, vm.DatabaseID, MeterHistory, 1000, time.Now()); err != nil {
				t.Fatal(err)
			}
			for _, step := range []State{StateParking, StateParked} {
				from := []State{StateRunning, StateParking}
				if err := s.Transition(ctx, vm.DatabaseID, from, step, TransitionOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.AddEgress(ctx, vm.DatabaseID, 300, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := s.AddEgress(ctx, vm.DatabaseID, 200, time.Now()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(1100 * time.Millisecond)
			if err := s.Transition(ctx, vm.DatabaseID, []State{StateParked}, StateDeleting, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, vm.DatabaseID); err != nil {
				t.Fatal(err)
			}
			t2 := time.Now()

			from, to := usageHour(t0), usageHour(t2).Add(time.Hour)
			u, err := s.Usage(ctx, vm.DatabaseID, from, to)
			if err != nil {
				t.Fatal(err)
			}
			lived := t2.Sub(t0).Seconds()
			if u.ActiveSeconds < 1 || u.ActiveSeconds > 2 || u.ComputeUnitSeconds < 2*u.ActiveSeconds || u.ComputeUnitSeconds > 2*(u.ActiveSeconds+1) {
				t.Fatalf("compute = %+v", u)
			}
			if float64(u.StorageByteSeconds) < float64(int64(1)<<30)*(lived-0.5) || float64(u.StorageByteSeconds) > float64(int64(1)<<30)*(lived+0.5) {
				t.Fatalf("storage = %d over %.2f s", u.StorageByteSeconds, lived)
			}
			if u.HistoryByteSeconds < 1000 || u.HistoryByteSeconds > 3000 || u.EgressBytes != 500 {
				t.Fatalf("history/egress = %+v", u)
			}
			// After the deletion nothing accrues.
			if later, err := s.Usage(ctx, vm.DatabaseID, to, to.Add(time.Hour)); err != nil || later != (Usage{}) {
				t.Fatalf("usage after deletion = %+v, %v", later, err)
			}
			if n, err := s.PruneUsage(ctx, to.Add(time.Hour)); err != nil || n < 4 {
				t.Fatalf("prune = %d, %v", n, err)
			}
			if _, err := s.Usage(ctx, vm.DatabaseID, from, to); !errors.Is(err, ErrNotFound) {
				t.Fatalf("usage after prune = %v", err)
			}
		})
	}
}

// TestStoreResize: a resize must fit the node's free volume capacity (and
// RAM while active), never shrinks, marks the record pending until
// finished, and is a no-op for the same shape.
func TestStoreResize(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 2048, VolumeCapacityBytes: 4 << 30, Lifecycle: NodeActive})
			vm, err := s.Place(ctx, newVM(1<<30, 384))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 2, MemMiB: 1024, VolumeBytes: 2 << 30}, time.Now()); !errors.Is(err, ErrConflict) {
				t.Fatalf("resize while provisioning = %v", err)
			}
			if err := s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			if err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 5 << 30}, time.Now()); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("past the node's volume capacity = %v", err)
			}
			if err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 512 << 20}, time.Now()); !errors.Is(err, ErrInvalid) {
				t.Fatalf("shrink = %v", err)
			}
			shape := Shape{VcpuCount: 2, MemMiB: 1024, VolumeBytes: 4 << 30}
			if err := s.Resize(ctx, vm.DatabaseID, shape, time.Now()); err != nil {
				t.Fatal(err)
			}
			got, _ := s.Get(ctx, vm.DatabaseID)
			if got.shape() != shape || !got.ResizePending {
				t.Fatalf("after resize = %+v", got)
			}
			if err := s.FinishResize(ctx, vm.DatabaseID); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, vm.DatabaseID); got.ResizePending {
				t.Fatal("finish left the resize pending")
			}
			if err := s.Resize(ctx, vm.DatabaseID, shape, time.Now()); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, vm.DatabaseID); got.ResizePending {
				t.Fatal("the same shape marked a resize pending")
			}
			// Running, the new memory must fit the RAM ceiling.
			if _, err := s.AdmitWake(ctx, vm.DatabaseID, false); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, vm.DatabaseID, []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 2, MemMiB: 4096, VolumeBytes: 4 << 30}, time.Now()); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("past the RAM ceiling while running = %v", err)
			}
		})
	}
}

// Drills start once per id, finish once, summarize per node and prune.
func TestStoreRestoreDrills(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			d := Drill{ID: uuid.NewString(), NodeID: "db-a", SourceID: uuid.NewString(), TargetAt: time.Now().UTC().Truncate(time.Microsecond)}
			if ok, err := s.StartDrill(ctx, d); err != nil || !ok {
				t.Fatalf("start = %v, %v", ok, err)
			}
			if ok, err := s.StartDrill(ctx, d); err != nil || ok {
				t.Fatalf("second start of the same drill = %v, %v; want false", ok, err)
			}
			other := Drill{ID: uuid.NewString(), NodeID: "db-b", SourceID: uuid.NewString(), TargetAt: d.TargetAt}
			if _, err := s.StartDrill(ctx, other); err != nil {
				t.Fatal(err)
			}
			running, err := s.RunningDrills(ctx)
			if err != nil || len(running) != 2 || running[0].Result != DrillRunning || !running[0].TargetAt.Equal(d.TargetAt) {
				t.Fatalf("running = %+v, %v", running, err)
			}
			recovered := d.TargetAt.Add(-time.Second)
			if err := s.FinishDrill(ctx, d.ID, DrillOK, recovered, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.FinishDrill(ctx, other.ID, DrillFailed, time.Time{}, "restore_failed"); err != nil {
				t.Fatal(err)
			}
			// A finished drill stays finished.
			if err := s.FinishDrill(ctx, d.ID, DrillFailed, time.Time{}, "late"); err != nil {
				t.Fatal(err)
			}
			if running, _ := s.RunningDrills(ctx); len(running) != 0 {
				t.Fatalf("running after finishing = %+v", running)
			}
			sums, err := s.DrillsByNode(ctx)
			if err != nil || sums["db-a"].LastOK.IsZero() || sums["db-a"].LastStarted.IsZero() || !sums["db-b"].LastOK.IsZero() {
				t.Fatalf("summaries = %+v, %v", sums, err)
			}
			if n, err := s.PruneDrills(ctx, time.Now().Add(time.Hour)); err != nil || n != 2 {
				t.Fatalf("prune = %d, %v", n, err)
			}
			if sums, _ := s.DrillsByNode(ctx); len(sums) != 0 {
				t.Fatalf("summaries after pruning = %+v", sums)
			}
		})
	}
}

// TestStoreMajorUpgrade: an upgrade holds its VM's memory on the node
// without metering compute, moves only under its lease, and on success
// records the major left and until when it ran.
func TestStoreMajorUpgrade(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 700, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive})
			parked := func(major int) VM {
				t.Helper()
				vm := newVM(1<<30, 384)
				vm.Major = major
				vm, err := s.Place(ctx, vm)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
					t.Fatal(err)
				}
				return vm
			}
			vm, other := parked(14), parked(14)
			if _, err := s.BeginUpgrade(ctx, vm.DatabaseID, 14, uuid.NewString(), time.Minute); !errors.Is(err, ErrInvalid) {
				t.Fatalf("upgrade to the same major = %v, want ErrInvalid", err)
			}
			// The other database's VM fills the node's RAM.
			if _, err := s.AdmitWake(ctx, other.DatabaseID, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BeginUpgrade(ctx, vm.DatabaseID, 17, uuid.NewString(), time.Minute); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("upgrade on a full node = %v, want ErrNoCapacity", err)
			}
			if err := s.Transition(ctx, other.DatabaseID, []State{StateWaking}, StateParked, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			upgrading, err := s.BeginUpgrade(ctx, vm.DatabaseID, 17, uuid.NewString(), time.Minute)
			if err != nil || upgrading.State != StateUpgrading || upgrading.Upgrade.To != 17 || upgrading.Upgrade.From != 14 || upgrading.Upgrade.Result != "" || upgrading.Major != 14 || upgrading.UpgradeLeaseUntil.IsZero() {
				t.Fatalf("begin = %+v, %v", upgrading, err)
			}
			if loads, _ := s.Loads(ctx); loads["db-a"].ActiveMB != billableMB(384) {
				t.Fatalf("an upgrading VM's memory = %d MB on the node, want %d", loads["db-a"].ActiveMB, billableMB(384))
			}
			if _, err := s.AdmitWake(ctx, other.DatabaseID, false); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("wake beside an upgrade on a full node = %v, want ErrNoCapacity", err)
			}
			if _, err := s.AdmitWake(ctx, vm.DatabaseID, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("wake of an upgrading database = %v, want ErrConflict", err)
			}
			if _, err := s.BeginUpgrade(ctx, vm.DatabaseID, 17, uuid.NewString(), time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("second begin = %v, want ErrConflict", err)
			}

			lease := upgrading.UpgradeLeaseUntil
			if _, err := s.ClaimUpgrade(ctx, vm.DatabaseID, time.Time{}, time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("taking a live lease = %v, want ErrConflict", err)
			}
			renewed, err := s.ClaimUpgrade(ctx, vm.DatabaseID, lease, time.Minute)
			if err != nil || renewed.Before(lease) {
				t.Fatalf("renew = %v, %v", renewed, err)
			}
			if _, err := s.ClaimUpgrade(ctx, vm.DatabaseID, lease, time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("renew with a stale lease = %v, want ErrConflict", err)
			}
			if err := s.AbortUpgrade(ctx, vm.DatabaseID, time.Time{}, StateParked, "upgrade_refused"); !errors.Is(err, ErrConflict) {
				t.Fatalf("abort under a live lease = %v, want ErrConflict", err)
			}
			until := time.Now().UTC().Truncate(time.Microsecond)
			backup := until.Add(-time.Second)
			if err := s.FinishUpgrade(ctx, vm.DatabaseID, lease, StateParked, until, backup); !errors.Is(err, ErrConflict) {
				t.Fatalf("finish with a stale lease = %v, want ErrConflict", err)
			}
			if err := s.FinishUpgrade(ctx, vm.DatabaseID, renewed, StateParked, until, backup); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, vm.DatabaseID)
			if err != nil || got.State != StateParked || got.Major != 17 || got.Upgrade.To != 17 || got.Upgrade.Result != UpgradeOK || !got.UpgradeLeaseUntil.IsZero() ||
				!got.LastBackupAt.Equal(backup) || len(got.MajorHistory) != 1 || got.MajorHistory[0].Major != 14 || !got.MajorHistory[0].Until.Equal(until) {
				t.Fatalf("after the upgrade = %+v, %v", got, err)
			}
			if got.MajorAt(until.Add(-time.Microsecond)) != 14 || got.MajorAt(until) != 17 || got.MajorAt(started.Add(-time.Hour)) != 14 {
				t.Fatalf("majors at the boundary: %d, %d", got.MajorAt(until.Add(-time.Microsecond)), got.MajorAt(until))
			}
			if u, err := s.Usage(ctx, vm.DatabaseID, started.Add(-time.Minute), time.Now().Add(time.Minute)); err != nil || u.ActiveSeconds != 0 {
				t.Fatalf("an upgrade metered compute: %+v, %v", u, err)
			}

			// A lapsed lease ends the next upgrade by anyone; a failed
			// database is not upgraded.
			if _, err := s.BeginUpgrade(ctx, vm.DatabaseID, 18, uuid.NewString(), time.Millisecond); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
			if err := s.AbortUpgrade(ctx, vm.DatabaseID, time.Time{}, StateFailed, "upgrade_failed"); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, vm.DatabaseID); got.State != StateFailed || got.Major != 17 || got.LastError != "upgrade_failed" || got.Upgrade.Result != UpgradeFailed || got.Upgrade.From != 17 || got.Upgrade.To != 18 {
				t.Fatalf("after the abort = %+v", got)
			}
			if _, err := s.BeginUpgrade(ctx, vm.DatabaseID, 18, uuid.NewString(), time.Minute); !errors.Is(err, ErrConflict) {
				t.Fatalf("upgrade of a failed database = %v, want ErrConflict", err)
			}

			// Finishing running opens compute and records no backup when
			// none was taken.
			second, err := s.BeginUpgrade(ctx, other.DatabaseID, 16, uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.FinishUpgrade(ctx, other.DatabaseID, second.UpgradeLeaseUntil, StateRunning, time.Now(), time.Time{}); err != nil {
				t.Fatal(err)
			}
			running, _ := s.Get(ctx, other.DatabaseID)
			if running.State != StateRunning || running.Major != 16 || !running.LastBackupAt.IsZero() || running.LastActiveAt.IsZero() {
				t.Fatalf("finished running = %+v", running)
			}
			time.Sleep(1100 * time.Millisecond)
			if u, _ := s.Usage(ctx, other.DatabaseID, started, time.Now().Add(time.Minute)); u.ActiveSeconds < 1 {
				t.Fatalf("a database left running after its upgrade meters no compute: %+v", u)
			}
		})
	}
}
