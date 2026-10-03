package sched

// spec: §6.2 — terminal instances must not strand admission capacity.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeRepairsTerminalAdmissionBelowAppCap(t *testing.T) {
	for _, resource := range []string{"ram", "vcpu", "cpu"} {
		for _, terminal := range []state.State{state.StateParked, state.StateStopped, state.StateFailed} {
			for _, restore := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/restore=%t", resource, terminal, restore), func(t *testing.T) {
					ctx := context.Background()
					store := state.NewMemStore()
					_, app, dep := seedApp(t, store, api.PlanScale, 128, 20)
					limits, _ := api.LimitsFor(api.PlanScale)
					node := state.ComputeNode{
						Name: state.DefaultLocalNodeName, TargetURL: "unix:///run/faas/vmmd.sock",
						VPCPUs: 1, MemMB: 56000, MaxConcurrency: 200, Active: true,
						AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: api.VCPUSlots,
					}
					count := 1
					switch resource {
					case "ram":
						node.AdmissionCeilingMB = api.BillableRAMMBWithSidecars(app.RAMMB, nil)
					case "vcpu":
						node.VCPUBudget = limits.VCPU
					case "cpu":
						count = int(cpuBudgetMillicores(node)) / api.DefaultAppCPUMillicores
					}
					node, err := store.UpsertComputeNode(ctx, node)
					if err != nil {
						t.Fatal(err)
					}
					if restore {
						if _, err := store.CreateSnapshot(ctx, state.Snapshot{
							DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: int64(app.RAMMB) << 20,
							StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "capacity-recovery"),
						}); err != nil {
							t.Fatal(err)
						}
					}
					vmm := &fakeVMM{}
					e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
					var staleIDs []string
					for range count {
						ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), app.RAMMB, node.ID, "")
						if err != nil {
							t.Fatal(err)
						}
						if err := e.ledger.Admit(Request{
							Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: node.ID,
							Plan: api.PlanScale, RAMMB: app.RAMMB, VCPU: limits.VCPU,
							CPUMillicores: api.DefaultAppCPUMillicores, MaxConcurrency: app.MaxConcurrency,
							NodeCeilingMB: node.AdmissionCeilingMB, VCPUBudget: node.VCPUBudget,
							CPUBudgetMillicores: int(cpuBudgetMillicores(node)),
						}); err != nil {
							t.Fatal(err)
						}
						// A peer finished teardown; this scheduler missed the ledger release.
						if err := store.UpdateInstanceState(ctx, ins.ID, string(terminal)); err != nil {
							t.Fatal(err)
						}
						staleIDs = append(staleIDs, ins.ID)
					}
					if e.ledger.Concurrency(app.ID) >= app.MaxConcurrency {
						t.Fatal("fixture must stay below the existing concurrency-recovery trigger")
					}
					got, err := e.Wake(ctx, app.ID, "", "", "")
					if err != nil {
						t.Fatalf("Wake with terminal %s reservation below app cap: %v", resource, err)
					}
					if got.InstanceID == "" || e.ledger.Concurrency(app.ID) != 1 {
						t.Fatalf("wake=%+v concurrency=%d", got, e.ledger.Concurrency(app.ID))
					}
					for _, id := range staleIDs {
						if e.ledger.ResidentFor(id) {
							t.Fatalf("terminal reservation %s remains resident", id)
						}
					}
					if restore && (vmm.restores != 1 || vmm.coldBoots != 0) {
						t.Fatalf("restores=%d coldBoots=%d", vmm.restores, vmm.coldBoots)
					}
					if !restore && (vmm.restores != 0 || vmm.coldBoots != 1) {
						t.Fatalf("restores=%d coldBoots=%d", vmm.restores, vmm.coldBoots)
					}
					if len(e.restorePlacementInFlight) != 0 {
						t.Fatalf("restore pressure leaked: %v", e.restorePlacementInFlight)
					}
				})
			}
		}
	}
}

type capacityRecoveryStore struct {
	*state.MemStore
	readErr error
	reads   int
}

func (s *capacityRecoveryStore) ListInstancesForApp(ctx context.Context, appID string) ([]state.Instance, error) {
	s.reads++
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.MemStore.ListInstancesForApp(ctx, appID)
}

func TestCapacityRecoveryPreservesUnprovenReservations(t *testing.T) {
	for _, status := range []string{"waking", "cold_booting", "running", "draining", "snapshotting", "migrating", "warm", "evicting_account_deleting", "missing", "read_failure", "other_app", "other_owner"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			store := &capacityRecoveryStore{MemStore: state.NewMemStore()}
			_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
			e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			rowState := status
			if status == "missing" || status == "read_failure" || status == "other_app" || status == "other_owner" {
				rowState = "parked"
			}
			ins, err := store.CreateInstance(ctx, app.ID, dep.ID, rowState, app.RAMMB, state.DefaultLocalNodeName, "")
			if err != nil {
				t.Fatal(err)
			}
			kind := KindWake
			if status == "warm" {
				kind = KindWarmPool
			}
			if err := e.ledger.Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
				RAMMB: app.RAMMB, VCPU: 2, MaxConcurrency: app.MaxConcurrency, NodeID: ins.NodeID, Kind: kind}); err != nil {
				t.Fatal(err)
			}
			recoveryApp := app.ID
			switch status {
			case "missing":
				if err := store.DeleteInstance(ctx, ins.ID); err != nil {
					t.Fatal(err)
				}
			case "read_failure":
				store.readErr = errors.New("database unavailable")
			case "other_app":
				other, err := store.CreateApp(ctx, state.App{AccountID: app.AccountID, Slug: "other-app", RAMMB: 128, MaxConcurrency: 5})
				if err != nil {
					t.Fatal(err)
				}
				recoveryApp = other.ID
			case "other_owner":
				e.ownerNodeID = "local-owner"
				if err := store.SetAppNodeID(ctx, app.ID, "remote-owner"); err != nil {
					t.Fatal(err)
				}
			}
			release := e.lockApp(recoveryApp)
			defer release()
			if e.recoverAppCapacityLocked(ctx, recoveryApp, api.ErrCapacity("full")) {
				t.Fatal("unproven reservation was repaired")
			}
			if !e.ledger.ResidentFor(ins.ID) {
				t.Fatal("reservation was released")
			}
			if got := e.ledger.ResidentRAMForNode(ins.NodeID); got != api.BillableRAMMBWithSidecars(app.RAMMB, nil) {
				t.Fatalf("resident RAM=%d", got)
			}
		})
	}
}

func TestCapacityRecoverySkipsHealthyAndNonCapacityErrors(t *testing.T) {
	store := &capacityRecoveryStore{MemStore: state.NewMemStore()}
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	store.reads = 0
	release := e.lockApp(app.ID)
	defer release()
	limits, _ := api.LimitsFor(api.PlanPro)
	for _, err := range []error{nil, context.Canceled, errors.New("placement read failed"), api.ErrPlanLimitConcurrencyAt(limits, 5, 5)} {
		if e.recoverAppCapacityLocked(context.Background(), app.ID, err) {
			t.Fatal("unexpected repair")
		}
	}
	if store.reads != 0 {
		t.Fatalf("reconciliation reads=%d", store.reads)
	}
}

func TestWakeCapacityRecoveryStillRefusesRealSaturation(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	limits, _ := api.LimitsFor(api.PlanPro)
	node, err := store.UpsertComputeNode(ctx, state.ComputeNode{
		Name: state.DefaultLocalNodeName, TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 4, MemMB: 56000, Active: true, MaxConcurrency: 200,
		AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: 2 * limits.VCPU,
	})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	var residentIDs []string
	for _, status := range []state.State{state.StateRunning, state.StateRunning, state.StateParked} {
		ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(status), app.RAMMB, node.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		// Model stale accounting predating a node budget reduction.
		if err := e.ledger.Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
			RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency, NodeID: node.ID,
			VCPUBudget: api.VCPUSlots}); err != nil {
			t.Fatal(err)
		}
		if status == state.StateRunning {
			residentIDs = append(residentIDs, ins.ID)
		}
	}
	_, err = e.AdmitInstance(ctx, app.ID, "", "", "")
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeCapacity {
		t.Fatalf("admission error=%v", err)
	}
	if e.ledger.UsedVCPUForNode(node.ID) != node.VCPUBudget {
		t.Fatal("live capacity was released or new capacity admitted")
	}
	for _, id := range residentIDs {
		if !e.ledger.ResidentFor(id) {
			t.Fatal("live reservation released")
		}
	}
	if vmm.restores != 0 || vmm.coldBoots != 0 {
		t.Fatal("refused wake dispatched a VM")
	}
}
