// adr: 1027 — production class: both stores place a replicated database's
// standby on a second node, account for it there, and only let a
// synchronized standby take over.
package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"
)

// runningReplicated places a replicated database and brings it to running.
func runningReplicated(t *testing.T, s Store, mem int) VM {
	t.Helper()
	ctx := context.Background()
	vm := newVM(1<<30, mem)
	vm.Replicated = true
	placed, err := s.Place(ctx, vm)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, placed.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitWake(ctx, placed.DatabaseID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, placed.DatabaseID, []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, placed.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStoreStandbyPlacementAndAccounting(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 3 << 30, Lifecycle: NodeActive})
			lonely := newVM(1<<30, 384)
			lonely.Replicated = true
			if _, err := s.Place(ctx, lonely); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("a replicated database on one node = %v, want ErrNoCapacity", err)
			}
			seedNodes(t, s,
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 4 << 30, Lifecycle: NodeActive},
				Node{ID: "db-c", TargetURL: "unix:///c", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeDraining},
			)
			vm := newVM(2<<30, 384)
			vm.Replicated = true
			placed, err := s.Place(ctx, vm)
			if err != nil || placed.NodeID != "db-b" || placed.StandbyNode != "db-a" || !placed.Replicated || placed.StandbyRunning || placed.StandbySynced {
				t.Fatalf("placed %+v, %v; want primary on db-b and its standby on db-a", placed, err)
			}
			// The standby's volume counts on db-a: 1 GiB free there, 2 GiB on
			// db-b. Without it db-a would look emptier.
			if next, err := s.Place(ctx, newVM(2<<30, 384)); err != nil || next.NodeID != "db-b" {
				t.Fatalf("next placement = %+v, %v; want db-b", next, err)
			}
			if _, err := s.Place(ctx, newVM(2<<30, 384)); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("placement over a standby's volume = %v, want ErrNoCapacity", err)
			}
			if _, err := s.BeginMove(ctx, placed.DatabaseID, "db-a", time.Minute); err == nil {
				t.Fatal("a database moved onto its own standby's node")
			}
		})
	}
}

func TestStoreStandbyClaimAndSync(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			// db-a fits one 384 MiB standby (392 MB billable), not two.
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 700, VolumeCapacityBytes: 50 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeActive},
			)
			first := runningReplicated(t, s, 384)
			second := runningReplicated(t, s, 384)
			if first.NodeID != "db-b" || second.StandbyNode != "db-a" {
				t.Fatalf("placements %+v / %+v", first, second)
			}
			if err := s.MarkStandbySynced(ctx, first.DatabaseID, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("synced without a running standby = %v, want ErrConflict", err)
			}
			claimed, err := s.ClaimStandby(ctx, first.DatabaseID)
			if err != nil || !claimed.StandbyRunning {
				t.Fatalf("claim = %+v, %v", claimed, err)
			}
			if _, err := s.ClaimStandby(ctx, first.DatabaseID); !errors.Is(err, ErrConflict) {
				t.Fatalf("second claim = %v, want ErrConflict", err)
			}
			if _, err := s.ClaimStandby(ctx, second.DatabaseID); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("claim over the standby node's ceiling = %v, want ErrNoCapacity", err)
			}
			if err := s.MarkStandbySynced(ctx, first.DatabaseID, true); err != nil {
				t.Fatal(err)
			}
			// Parking stops the standby cleanly after its primary: still synced.
			if err := s.Transition(ctx, first.DatabaseID, []State{StateRunning}, StateParking, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.ReleaseStandby(ctx, first.DatabaseID, true); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, first.DatabaseID, []State{StateParking}, StateParked, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, first.DatabaseID); !got.StandbySynced || got.StandbyRunning {
				t.Fatalf("after a clean park: %+v; want synced, stopped", got)
			}
			// The freed standby RAM admits the other standby.
			if _, err := s.ClaimStandby(ctx, second.DatabaseID); err != nil {
				t.Fatalf("claim after the first standby stopped: %v", err)
			}
			// A wake that resumes the standby keeps trusting it.
			if _, err := s.AdmitWake(ctx, first.DatabaseID, true); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, first.DatabaseID); !got.StandbySynced || got.State != StateWaking {
				t.Fatalf("a resuming wake: %+v; want waking with its standby synced", got)
			}
			if err := s.Transition(ctx, first.DatabaseID, []State{StateWaking}, StateParked, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			// Any other wake makes the primary asynchronous: no longer synced.
			if _, err := s.AdmitWake(ctx, first.DatabaseID, false); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, first.DatabaseID); got.StandbySynced {
				t.Fatal("a waking database kept its standby synced")
			}
			if err := s.ReleaseStandby(ctx, second.DatabaseID, false); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkStandbySynced(ctx, second.DatabaseID, false); err != nil {
				t.Fatalf("clearing always works: %v", err)
			}
		})
	}
}

func TestStoreFailoverOnlyFromASyncedStandby(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 50 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeActive},
				Node{ID: "db-c", TargetURL: "unix:///c", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30, Lifecycle: NodeActive},
			)
			running := runningReplicated(t, s, 384)
			if _, err := s.Failover(ctx, running.DatabaseID); !errors.Is(err, ErrConflict) {
				t.Fatalf("failover to an unsynced standby = %v, want ErrConflict", err)
			}
			if _, err := s.ClaimStandby(ctx, running.DatabaseID); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkStandbySynced(ctx, running.DatabaseID, true); err != nil {
				t.Fatal(err)
			}
			after, err := s.Failover(ctx, running.DatabaseID)
			if err != nil || after.NodeID != running.StandbyNode || after.StandbyNode != "" || after.State != StateRunning ||
				after.PromotePending || after.StandbySynced || after.LastError != errCodeFailedOver {
				t.Fatalf("failover of a running database = %+v, %v; want running on its standby's node", after, err)
			}

			parked := runningReplicated(t, s, 384)
			if _, err := s.ClaimStandby(ctx, parked.DatabaseID); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkStandbySynced(ctx, parked.DatabaseID, true); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, parked.DatabaseID, []State{StateRunning}, StateParking, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.ReleaseStandby(ctx, parked.DatabaseID, true); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, parked.DatabaseID, []State{StateParking}, StateParked, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			after, err = s.Failover(ctx, parked.DatabaseID)
			if err != nil || after.NodeID != parked.StandbyNode || after.State != StateParked || !after.PromotePending {
				t.Fatalf("failover of a parked database = %+v, %v; want parked there with its promotion pending", after, err)
			}
			if err := s.FinishPromotion(ctx, parked.DatabaseID); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, parked.DatabaseID); got.PromotePending {
				t.Fatal("promotion still pending")
			}
			// The failed-over database gets a new standby elsewhere.
			replaced, err := s.ReplaceStandby(ctx, parked.DatabaseID)
			if err != nil || replaced.StandbyNode == "" || replaced.StandbyNode == replaced.NodeID || replaced.StandbySynced {
				t.Fatalf("replace = %+v, %v", replaced, err)
			}
			again, err := s.ReplaceStandby(ctx, parked.DatabaseID)
			if err != nil || again.StandbyNode == replaced.StandbyNode || again.StandbyNode == again.NodeID {
				t.Fatalf("second replace = %+v, %v; want a third node", again, err)
			}
			plain, err := s.Place(ctx, newVM(1<<30, 384))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReplaceStandby(ctx, plain.DatabaseID); !errors.Is(err, ErrConflict) {
				t.Fatalf("replace on an unreplicated database = %v, want ErrConflict", err)
			}
		})
	}
}

// Both stores turn replication on with a standby node placed like
// Place's, refuse to turn it off while the standby runs, and owe the
// primary's slot until it is recorded dropped.
func TestStoreResizeChangesReplication(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx, at := factory(t), context.Background(), time.Now()
			seedNodes(t, s, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 4 << 30, Lifecycle: NodeActive})
			placed, err := s.Place(ctx, newVM(1<<30, 384))
			if err != nil {
				t.Fatal(err)
			}
			id := placed.DatabaseID
			if err := s.Transition(ctx, id, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			production := Shape{VcpuCount: 2, MemMiB: 768, VolumeBytes: 2 << 30, Replicated: true}
			if err := s.Resize(ctx, id, production, at); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("replication without a second node = %v, want ErrNoCapacity", err)
			}
			seedNodes(t, s, Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 3 << 30, Lifecycle: NodeActive})
			if err := s.Resize(ctx, id, production, at); err != nil {
				t.Fatal(err)
			}
			got, _ := s.Get(ctx, id)
			if !got.Replicated || got.StandbyNode != "db-b" || got.StandbySynced || got.DropStandbySlot || !got.ResizePending || got.VolumeBytes != 2<<30 {
				t.Fatalf("after turning replication on: %+v", got)
			}
			// The standby's 2 GiB counts on db-b now: after db-a's last 2 GiB
			// go, no node has room for another 2 GiB.
			if next, err := s.Place(ctx, newVM(2<<30, 384)); err != nil || next.NodeID != "db-a" {
				t.Fatalf("placement = %+v, %v; want db-a", next, err)
			}
			if _, err := s.Place(ctx, newVM(2<<30, 384)); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("placement over the standby's volume = %v, want ErrNoCapacity", err)
			}
			if err := s.FinishResize(ctx, id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AdmitWake(ctx, id, false); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, id, []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimStandby(ctx, id); err != nil {
				t.Fatal(err)
			}
			standalone := Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 2 << 30}
			if err := s.Resize(ctx, id, standalone, at); !errors.Is(err, ErrConflict) {
				t.Fatalf("turning replication off with the standby running = %v, want ErrConflict", err)
			}
			if err := s.ReleaseStandby(ctx, id, false); err != nil {
				t.Fatal(err)
			}
			if err := s.Resize(ctx, id, standalone, at); err != nil {
				t.Fatal(err)
			}
			got, _ = s.Get(ctx, id)
			if got.Replicated || got.StandbyNode != "" || got.StandbySynced || !got.DropStandbySlot {
				t.Fatalf("after turning replication off: %+v", got)
			}
			if err := s.ClearStandbySlotDrop(ctx, id); err != nil {
				t.Fatal(err)
			}
			if got, _ = s.Get(ctx, id); got.DropStandbySlot {
				t.Fatalf("slot drop still owed: %+v", got)
			}
		})
	}
}

// Growing a production database's volume needs room on its standby's
// node too; a standby node that drains is not checked, since its standby
// moves.
func TestStoreResizeChecksTheStandbysNode(t *testing.T) {
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx, at := factory(t), context.Background(), time.Now()
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 3 << 30, Lifecycle: NodeActive},
			)
			vm := newVM(1<<30, 384)
			vm.Replicated = true
			placed, err := s.Place(ctx, vm)
			if err != nil || placed.StandbyNode != "db-b" {
				t.Fatalf("placed %+v, %v", placed, err)
			}
			if err := s.Transition(ctx, placed.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			grown := Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 4 << 30, Replicated: true}
			if err := s.Resize(ctx, placed.DatabaseID, grown, at); !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("growing past the standby's node = %v, want ErrNoCapacity", err)
			}
			if got, _ := s.Get(ctx, placed.DatabaseID); got.VolumeBytes != 1<<30 || got.ResizePending {
				t.Fatalf("after the refusal: %+v", got)
			}
			fits := grown
			fits.VolumeBytes = 3 << 30
			if err := s.Resize(ctx, placed.DatabaseID, fits, at); err != nil {
				t.Fatalf("growing to what the standby's node holds = %v", err)
			}
			seedNodes(t, s, Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 3 << 30, Lifecycle: NodeDraining})
			if err := s.FinishResize(ctx, placed.DatabaseID); err != nil {
				t.Fatal(err)
			}
			if err := s.Resize(ctx, placed.DatabaseID, grown, at); err != nil {
				t.Fatalf("growing with the standby's node draining = %v", err)
			}
		})
	}
}

// A production database's standby counts into compute units while its VM
// runs and into storage while it has a node, in both stores; active
// seconds stay the primary's, and turning replication off stops the
// standby's storage.
func TestStoreStandbyCountsIntoUsage(t *testing.T) {
	const gib = int64(1 << 30)
	for name, factory := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s, ctx := factory(t), context.Background()
			seedNodes(t, s,
				Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive},
			)
			vm := newVM(gib, 384)
			vm.VcpuCount, vm.Replicated = 2, true
			start := time.Now()
			placed, err := s.Place(ctx, vm)
			if err != nil {
				t.Fatal(err)
			}
			id := placed.DatabaseID
			if err := s.Transition(ctx, id, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AdmitWake(ctx, id, false); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, id, []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimStandby(ctx, id); err != nil {
				t.Fatal(err)
			}
			time.Sleep(1100 * time.Millisecond)
			if err := s.ReleaseStandby(ctx, id, true); err != nil {
				t.Fatal(err)
			}
			for _, step := range []State{StateParking, StateParked} {
				if err := s.Transition(ctx, id, []State{StateRunning, StateParking}, step, TransitionOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			u, err := s.Usage(ctx, id, start, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			// Primary: 2 vCPUs for ActiveSeconds; the standby added at
			// least 2 vCPU-seconds more while it ran.
			if u.ActiveSeconds < 1 || u.ComputeUnitSeconds < 2*u.ActiveSeconds+2 {
				t.Fatalf("usage %+v: the standby's vCPUs are not counted", u)
			}
			if u.StorageByteSeconds < 2*gib {
				t.Fatalf("usage %+v: the standby's volume is not counted", u)
			}

			// Out of production: only one copy is stored from then on.
			if err := s.Resize(ctx, id, Shape{VcpuCount: 2, MemMiB: 384, VolumeBytes: gib}, time.Now()); err != nil {
				t.Fatal(err)
			}
			mark := time.Now()
			time.Sleep(1100 * time.Millisecond)
			after, err := s.Usage(ctx, id, mark, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if after.StorageByteSeconds > 2*gib || after.StorageByteSeconds < gib || after.ComputeUnitSeconds != 0 {
				t.Fatalf("usage after leaving production %+v; want one volume's storage and no compute", after)
			}
		})
	}
}
