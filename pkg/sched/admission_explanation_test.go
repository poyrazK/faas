// adr: 20261018090000
package sched

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func requireWorkerBlocker(t *testing.T, pool state.WorkerRecoveryPool, code string) api.WorkerAdmissionBlocker {
	t.Helper()
	for _, b := range pool.Blockers {
		if b.Code == code {
			return b
		}
	}
	t.Fatalf("missing %s admission blocker: %+v", code, pool)
	return api.WorkerAdmissionBlocker{}
}

func TestWorkerAdmissionPlacementExplainsMixedResourceRefusals(t *testing.T) {
	// One host has RAM, another CPU. The explanation must not imply their
	// combined headroom can fit one VM, or reveal host/tenant reservations.
	a, b := node("private-a", "a", 0, 1000), node("private-b", "b", 0, 1000)
	a.VPCPUs, b.VPCPUs = 1, 1
	request := Request{RAMMB: 128, VCPU: 1, CPUMillicores: 1000}
	_, err := choosePlacementWithCPU([]state.ComputeNode{a, b}, map[string]int64{b.ID: 900}, nil,
		map[string]int64{a.ID: cpuBudgetMillicores(a)}, request)
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeCapacity || problem.Status != 503 {
		t.Fatalf("capacity Problem compatibility lost: %v", err)
	}
	blockers := admissionBlockers(err)
	if len(blockers) != 2 || blockers[0].Code != "node_ram" || blockers[1].Code != "node_cpu" {
		t.Fatalf("mixed placement causes lost: %+v", blockers)
	}
	for _, blocker := range blockers {
		if blocker.Requested == nil || blocker.Used != nil || blocker.Limit != nil {
			t.Fatalf("unsafe/incomplete platform constraint: %+v", blocker)
		}
	}
	// Returning capacity must still choose a node despite other refusals.
	if _, err := choosePlacementWithCPU([]state.ComputeNode{a, b}, nil, nil, nil, request); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerAdmissionPlacementDistinguishesUnavailableAndVCPUNodes(t *testing.T) {
	n := node("private", "node", 0, 1000)
	n.Lifecycle, n.Active = state.NodeLifecycleMaintenance, false
	_, err := ChoosePlacement([]state.ComputeNode{n}, nil, nil, Request{RAMMB: 128, VCPU: 1})
	if b := admissionBlockers(err); len(b) != 1 || b[0].Code != "no_admitting_nodes" {
		t.Fatalf("maintenance was reported as resource exhaustion: %+v", b)
	}
	n.Lifecycle, n.Active, n.VCPUBudget = state.NodeLifecycleActive, true, 1
	_, err = ChoosePlacement([]state.ComputeNode{n}, nil, map[string]int64{n.ID: 1}, Request{RAMMB: 128, VCPU: 1})
	if b := admissionBlockers(err); len(b) != 1 || b[0].Code != "node_vcpu" {
		t.Fatalf("vCPU refusal lost: %+v", b)
	}
}

func TestWorkerAdmissionLedgerExplainsConcurrencyAndStartupCPU(t *testing.T) {
	ledger := NewNodeLedger()
	r := Request{Instance: "first", AppID: "app", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1, NodeID: "node", CPUBudgetMillicores: 1000}
	if err := ledger.Admit(r); err != nil {
		t.Fatal(err)
	}
	r.Instance = "next"
	err := ledger.Admit(r)
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodePlanLimitConcur {
		t.Fatalf("concurrency Problem lost: %v", err)
	}
	b := admissionBlockers(err)
	if len(b) != 1 || b[0].Code != "concurrency_limit" || b[0].Used == nil || *b[0].Used != 1 || b[0].Limit == nil || *b[0].Limit != 1 {
		t.Fatalf("wrong concurrency explanation: %+v", b)
	}
	r.MaxConcurrency, r.CPUMillicores, r.CPUStartupBoostMillicores = 5, 500, 1500
	err = ledger.Admit(r)
	b = admissionBlockers(err)
	if len(b) != 1 || b[0].Code != "node_cpu" || b[0].Requested == nil || *b[0].Requested != 1500 {
		t.Fatalf("startup CPU boost omitted: %+v", b)
	}
	if ledger.Concurrency("app") != 1 {
		t.Fatal("refused reservations changed concurrency")
	}
}

func TestWorkerRecoveryCooldownRechecksAccountBeforeRetainingRefusals(t *testing.T) {
	for _, desired := range []int{2, 3} {
		t.Run(strconv.Itoa(desired), func(t *testing.T) {
			store := state.NewMemStore()
			_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
			neighbor := seedScopedWorker(t, store, app, deps["staging"])
			seedScopedWorker(t, store, app, deps["staging"])
			e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			e.workerReconcileSubmit = func(_ context.Context, _ string) {}
			now := time.Now().UTC()
			e.now = func() time.Time { return now }
			node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			node.AdmissionCeilingMB = 1
			if _, err := store.UpsertComputeNodeFromOperator(t.Context(), node); err != nil {
				t.Fatal(err)
			}
			if err := e.ReconcileWorkerPoolForScope(t.Context(), app.ID, "default", desired, TriggerWorkerPool); err != nil {
				t.Fatal(err)
			}
			before, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
			if err != nil || len(before.Pools) != 1 {
				t.Fatalf("missing refusal: %+v %v", before, err)
			}
			requireWorkerBlocker(t, before.Pools[0], "account_worker_limit")
			refused := requireWorkerBlocker(t, before.Pools[0], "node_ram")
			// Physical teardown released a neighboring account slot. The
			// cooldown still holds node admission; the account gate runs again.
			if err := store.DeleteInstance(t.Context(), neighbor.ID); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Second)
			if err := e.ReconcileWorkerPoolForScope(t.Context(), app.ID, "default", desired, TriggerWorkerPool); err != nil {
				t.Fatal(err)
			}
			after, err := store.WorkerRecoveryByApp(t.Context(), app.ID)
			if err != nil || len(after.Pools) != 1 {
				t.Fatalf("missing held evidence: %+v %v", after, err)
			}
			requireWorkerBlocker(t, after.Pools[0], "retry_cooldown")
			if b := requireWorkerBlocker(t, after.Pools[0], "node_ram"); !b.ObservedAt.Equal(refused.ObservedAt) {
				t.Fatal("cooldown refreshed physical refusal")
			}
			if desired == 3 {
				b := requireWorkerBlocker(t, after.Pools[0], "account_worker_limit")
				if b.Used == nil || *b.Used != 1 || !b.ObservedAt.Equal(now) {
					t.Fatalf("old account refusal replaced current decision: %+v", b)
				}
			} else {
				for _, b := range after.Pools[0].Blockers {
					if b.Code == "account_worker_limit" {
						t.Fatal("resolved account limit retained during cooldown")
					}
				}
			}
		})
	}
}
