// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRejectedResizePreservesProductionDatabase(t *testing.T) {
	for _, reason := range []string{"shrink", "capacity", "success"} {
		t.Run(reason, func(t *testing.T) {
			s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
			seedNodes(t, store,
				Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 3 << 30, Lifecycle: NodeActive},
				Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 2 << 30, Lifecycle: NodeActive},
			)
			req := provisionRequest()
			req.Replicated = true
			req.VcpuCount = 2
			req.MemMiB = 2048
			req.VolumeBytes = 2 << 30
			vm, err := s.Provision(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Wake(t.Context(), vm.DatabaseID); err != nil {
				t.Fatal(err)
			}
			s.standbyWG.Wait()
			before, err := store.Get(t.Context(), vm.DatabaseID)
			if err != nil {
				t.Fatal(err)
			}
			if before.State != StateRunning || !before.StandbySynced || !before.StandbyRunning {
				t.Fatalf("bad fixture %+v", before)
			}
			stops := vmm.callCount("stop")
			discards := strings.Count(vmm.nodeCalls(), "discard@")
			volume := int64(1 << 30)
			want := ErrInvalid
			if reason == "capacity" {
				volume = 4 << 30
				want = ErrNoCapacity
			}
			if reason == "success" {
				volume = 2 << 30
				want = nil
			}
			_, err = s.Resize(t.Context(), vm.DatabaseID, Shape{VcpuCount: 1, MemMiB: 512, VolumeBytes: volume, Replicated: false})
			if !errors.Is(err, want) {
				t.Fatalf("resize error=%v want %v", err, want)
			}
			after, getErr := store.Get(t.Context(), vm.DatabaseID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			t.Logf("error=%v before=%s synced=%t running=%t; after=%s synced=%t running=%t; stops=%d discards=%d", err, before.State, before.StandbySynced, before.StandbyRunning, after.State, after.StandbySynced, after.StandbyRunning, vmm.callCount("stop")-stops, strings.Count(vmm.nodeCalls(), "discard@")-discards)
			if reason == "success" {
				if after.State != StateRunning || after.Replicated {
					t.Fatalf("valid downgrade failed: %+v", after)
				}
				return
			}
			if after.State != StateRunning || !after.StandbySynced || !after.StandbyRunning || vmm.callCount("stop") != stops || strings.Count(vmm.nodeCalls(), "discard@") != discards {
				t.Fatal("a rejected resize stopped the primary and retired its healthy standby")
			}
		})
	}
}

type rejectingResizeStore struct{ *MemStore }

func (s rejectingResizeStore) Resize(context.Context, string, Shape, time.Time) error {
	return ErrNoCapacity
}

func TestLateResizeRejectionResumesProductionDatabase(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	vm := wakeReplicated(t, s)
	discards := strings.Count(vmm.nodeCalls(), "discard@")
	s.store = rejectingResizeStore{store}
	_, err := s.Resize(t.Context(), vm.DatabaseID, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: vm.VolumeBytes, Replicated: false})
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("admission error=%v", err)
	}
	s.standbyWG.Wait()
	after, err := store.Get(t.Context(), vm.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != StateRunning || !after.Replicated || !after.StandbySynced || !after.StandbyRunning || after.shape() != vm.shape() {
		t.Fatalf("late rejection did not restore service: %+v", after)
	}
	if strings.Count(vmm.nodeCalls(), "discard@") != discards {
		t.Fatal("late rejection discarded the original standby")
	}
}

func TestPostgresResizeAdmissionPreservesStandbyUntilCommit(t *testing.T) {
	store := storeFactories(t)["pg"](t)
	vmm := newFakeVMM()
	seedNodes(t, store,
		Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 3 << 30, Lifecycle: NodeActive},
		Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 2 << 30, Lifecycle: NodeActive},
	)
	s := New(store, orderedVMM{fakeVMM: vmm, t: t, store: store}, Config{IdleTimeout: time.Minute, ReadyTimeout: 5 * time.Second, WakeWait: time.Second, StandbySeedTimeout: 5 * time.Second, StandbySyncTimeout: time.Second}, nil)
	t.Cleanup(s.standbyWG.Wait)
	req := provisionRequest()
	req.Replicated = true
	req.VcpuCount = 2
	req.MemMiB = 2048
	req.VolumeBytes = 2 << 30
	vm, err := s.Provision(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Wake(t.Context(), vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	stops := vmm.callCount("stop")
	shape := Shape{VcpuCount: 1, MemMiB: 512, VolumeBytes: 4 << 30, Replicated: false}
	if _, err := s.Resize(t.Context(), vm.DatabaseID, shape); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("capacity rejection=%v", err)
	}
	after, err := store.Get(t.Context(), vm.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != StateRunning || !after.StandbySynced || !after.StandbyRunning || vmm.callCount("stop") != stops {
		t.Fatalf("rejection altered live production database: %+v", after)
	}
	shape.VolumeBytes = 2 << 30
	after, err = s.Resize(t.Context(), vm.DatabaseID, shape)
	if err != nil || after.State != StateRunning || after.Replicated || after.StandbyNode != "" || after.StandbySynced {
		t.Fatalf("admitted downgrade=%+v err=%v", after, err)
	}
	if !strings.Contains(vmm.nodeCalls(), "discard@db-b") {
		t.Fatal("admitted downgrade did not discard the retired standby")
	}
}
