// adr: 1528
package sched

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

type staleNodePlacementStore struct {
	MemStore     *state.MemStore
	calls, reads atomic.Int32
	before       func(context.Context, string, int)
	loseReply    bool
}

func (s *staleNodePlacementStore) ActiveComputeNodes(ctx context.Context) ([]state.ComputeNode, error) {
	s.reads.Add(1)
	return s.MemStore.ActiveComputeNodes(ctx)
}

func (s *staleNodePlacementStore) CreateInstanceWithMode(ctx context.Context, app, dep, current string, ramMB int, node, wake, mode string) (state.Instance, error) {
	call := int(s.calls.Add(1))
	if s.before != nil {
		s.before(ctx, node, call)
	}
	ins, err := s.MemStore.CreateInstanceWithMode(ctx, app, dep, current, ramMB, node, wake, mode)
	if err == nil && s.loseReply {
		return state.Instance{}, errors.New("commit response lost")
	}
	return ins, err
}

func (s *staleNodePlacementStore) CreateAndClaimJobInstance(ctx context.Context, id, job, run string, index int, current string, ramMB int, node, wake, token string, expires time.Time, owner string) (state.Instance, error) {
	call := int(s.calls.Add(1))
	if s.before != nil {
		s.before(ctx, node, call)
	}
	return s.MemStore.CreateAndClaimJobInstance(ctx, id, job, run, index, current, ramMB, node, wake, token, expires, owner)
}

func admissionPeer(t *testing.T, s *state.MemStore) state.ComputeNode {
	t.Helper()
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "admission-peer", Lifecycle: state.NodeLifecycleActive, TargetURL: "unix:///tmp/peer.sock", VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestNodeAdmissionWakeRefreshesCachedClosedNode(t *testing.T) {
	fake := &staleNodePlacementStore{MemStore: state.NewMemStore()}
	_, app, _ := seedApp(t, fake.MemStore, api.PlanPro, 256, 3)
	old, err := fake.MemStore.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	peer := admissionPeer(t, fake.MemStore)
	fake.before = func(ctx context.Context, node string, call int) {
		if call == 1 {
			if err := fake.MemStore.NodeSetLifecycle(ctx, node, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
				t.Fatal(err)
			}
		}
	}
	vmm := &fakeVMM{}
	e := newEngine(t, statetest.NewMem(fake), vmm, &fakeNotifier{}, "1.10.0").WithNodeRegistry(NewNodeRegistry([]state.ComputeNode{old}))
	fake.reads.Store(0)
	result, err := e.Wake(t.Context(), app.ID, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeID != peer.ID || fake.calls.Load() != 2 || fake.reads.Load() == 0 {
		t.Fatalf("did not reselect fresh fleet: %+v calls=%d reads=%d", result, fake.calls.Load(), fake.reads.Load())
	}
	rows, err := fake.MemStore.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(rows) != 1 || rows[0].NodeID != peer.ID || vmm.coldBoots != 1 {
		t.Fatalf("retry leaked/booted old placement: %+v boots=%d %v", rows, vmm.coldBoots, err)
	}
}

func TestNodeAdmissionRestoreRetryReleasesPressure(t *testing.T) {
	fake := &staleNodePlacementStore{MemStore: state.NewMemStore()}
	_, app, dep := seedApp(t, fake.MemStore, api.PlanPro, 256, 3)
	old, err := fake.MemStore.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	peer := admissionPeer(t, fake.MemStore)
	fake.before = func(ctx context.Context, node string, call int) {
		if call == 1 {
			if err := fake.MemStore.NodeSetLifecycle(ctx, node, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
				t.Fatal(err)
			}
		}
	}
	e := newEngine(t, statetest.NewMem(fake), &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithNodeRegistry(NewNodeRegistry([]state.ComputeNode{old}))
	wake := uuid.NewString()
	ins, p, release, err := e.createWithNodePlacement(t.Context(), Request{AppID: app.ID, Plan: api.PlanPro, RAMMB: 256, VCPU: 2, MaxConcurrency: 3}, true, func(ctx context.Context, p Placement) (state.Instance, error) {
		return e.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, "waking", 256, p.NodeID, wake, "normal")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if p.NodeID != peer.ID || ins.WakeID != wake || fake.calls.Load() != 2 {
		t.Fatalf("restore retry: %+v %+v", ins, p)
	}
	readCounts := func() map[string]int {
		session, err := fake.MemStore.AcquireSnapshotRestorePressure(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		counts, err := session.ActiveSnapshotRestoreCounts(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return counts
	}
	counts := readCounts()
	if counts[old.ID] != 0 || counts[peer.ID] != 1 {
		t.Fatalf("refused placement kept restore pressure: %v", counts)
	}
	release()
	if counts := readCounts(); counts[old.ID] != 0 || counts[peer.ID] != 0 {
		t.Fatalf("successful reservation not releasable: %v", counts)
	}
}

func TestNodeAdmissionRetryIsBoundedAndDoesNotReplayUncertainInserts(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "repeated_cycles"
		if lost {
			name = "lost_commit_response"
		}
		t.Run(name, func(t *testing.T) {
			fake := &staleNodePlacementStore{MemStore: state.NewMemStore(), loseReply: lost}
			_, app, dep := seedApp(t, fake.MemStore, api.PlanPro, 256, 3)
			if !lost {
				fake.before = func(ctx context.Context, node string, _ int) {
					if err := fake.MemStore.NodeSetLifecycle(ctx, node, state.NodeLifecycleActive, state.NodeLifecycleUnavailable); err != nil {
						t.Fatal(err)
					}
					if err := fake.MemStore.NodeSetLifecycle(ctx, node, state.NodeLifecycleUnavailable, state.NodeLifecycleActive); err != nil {
						t.Fatal(err)
					}
				}
			}
			e := newEngine(t, statetest.NewMem(fake), &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			_, _, _, err := e.createWithNodePlacement(t.Context(), Request{AppID: app.ID, Plan: api.PlanPro, RAMMB: 256, VCPU: 2, MaxConcurrency: 3}, false, func(ctx context.Context, p Placement) (state.Instance, error) {
				return e.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, "cold_booting", 256, p.NodeID, uuid.NewString(), "normal")
			})
			if err == nil {
				t.Fatal("expected refusal/uncertain result")
			}
			wantCalls, wantRows := int32(api.NodeAdmissionPlacementAttempts), 0
			if lost {
				wantCalls, wantRows = 1, 1
			}
			rows, readErr := fake.MemStore.ListInstancesForApp(t.Context(), app.ID)
			if fake.calls.Load() != wantCalls || readErr != nil || len(rows) != wantRows {
				t.Fatalf("unsafe retry: calls=%d rows=%d %v", fake.calls.Load(), len(rows), readErr)
			}
		})
	}
}

func TestNodeAdmissionJobRetryReleasesLeaseAndLedger(t *testing.T) {
	fake := &staleNodePlacementStore{MemStore: state.NewMemStore()}
	account, err := fake.MemStore.CreateAccount(t.Context(), "job-placement@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	job, err := fake.MemStore.JobCreate(t.Context(), account.ID, "placement-job", "batch", "ghcr.io/onebox-faas/test:latest", []string{"/bin/true"}, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.MemStore.JobSetImageMaterialization(t.Context(), job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
		t.Fatal(err)
	}
	run, _, err := fake.MemStore.JobRunCreate(t.Context(), job.ID, account.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	old, err := fake.MemStore.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	peer := admissionPeer(t, fake.MemStore)
	fake.before = func(ctx context.Context, node string, call int) {
		if call == 1 {
			if err := fake.MemStore.NodeSetLifecycle(ctx, node, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
				t.Fatal(err)
			}
		}
	}
	leaser := NewMemLeaser(nil)
	e := newEngine(t, statetest.NewMem(fake), &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithNodeRegistry(NewNodeRegistry([]state.ComputeNode{old})).WithJobLeaser(AdaptJobLeaser(leaser))
	result, err := e.WakeJob(t.Context(), account.ID, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeID != peer.ID || fake.calls.Load() != 2 || leaser.Size() != 1 {
		t.Fatalf("stale job leaked lease or placement: %+v calls=%d leases=%d", result, fake.calls.Load(), leaser.Size())
	}
	rows, err := fake.MemStore.ListJobInstances(t.Context())
	if err != nil || len(rows) != 1 || rows[0].NodeID != peer.ID {
		t.Fatalf("stale job leaked instance: %+v %v", rows, err)
	}
	if used := e.ledger.ResidentRAMForNode(old.ID); used != 0 {
		t.Fatalf("stale job leaked ledger reservation: %d", used)
	}
}

func TestNodeAdmissionMigrationRejectsChangedDestinationAndCleansUp(t *testing.T) {
	for _, duringAdopt := range []bool{false, true} {
		name := "closed_before_pause"
		if duringAdopt {
			name = "cycle_during_adopt"
		}
		t.Run(name, func(t *testing.T) {
			s := state.NewMemStore()
			destination := admissionPeer(t, s)
			id := seedInstanceForMigration(t, s, "dying")
			vmm := &fakeVMM{}
			if duringAdopt {
				vmm.adoptHook = func(string) {
					if err := s.NodeSetLifecycle(t.Context(), destination.ID, state.NodeLifecycleActive, state.NodeLifecycleUnavailable); err != nil {
						t.Fatal(err)
					}
					if err := s.NodeSetLifecycle(t.Context(), destination.ID, state.NodeLifecycleUnavailable, state.NodeLifecycleActive); err != nil {
						t.Fatal(err)
					}
				}
			} else if err := s.NodeSetLifecycle(t.Context(), destination.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
				t.Fatal(err)
			}
			ledger := NewNodeLedger()
			h := NewMigrationHarness(t.Context(), s, vmm, wire.NewOpsMetrics("schedd"), testLog(), destination.ID, stubSpecBuilder, ledger, nil)
			if err := h.MigrateOne(t.Context(), id, "dying"); !errors.Is(err, state.ErrNodeAdmissionStale) {
				t.Fatalf("changed destination accepted: %v", err)
			}
			row, err := s.InstanceByID(t.Context(), id)
			if err != nil || row.NodeID != "dying" || ledger.ResidentFor(id) {
				t.Fatalf("migration leaked destination ownership/reservation: %+v %v", row, err)
			}
			if duringAdopt {
				if vmm.destroys != 1 || vmm.cancels != 1 || vmm.acks != 0 {
					t.Fatalf("adopted VM not rolled back: destroys=%d cancels=%d acks=%d", vmm.destroys, vmm.cancels, vmm.acks)
				}
			} else if vmm.prepares != 0 || vmm.adopts != 0 {
				t.Fatal("closed destination caused VM side effects")
			}
		})
	}
}
