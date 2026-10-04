package sched

// adr: 497
import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalRuntimeSchedulerInitialIssuance(t *testing.T) {
	for _, purpose := range []string{runtimequota.CapacityServing, runtimequota.CapacityWarm, runtimequota.CapacityPrime, runtimequota.CapacityAppTask} {
		t.Run(purpose, func(t *testing.T) {
			store, account, app, dep := regionalQuotaSchedulerFixture(t)
			freezeRegionalSchedulerApp(t, store, account, app)
			head, err := store.RegionalRuntimeQuotaHead(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "regional-node", TargetURL: "unix:///run/faas/vmmd-test.sock", Active: true})
			if err != nil {
				t.Fatal(err)
			}
			epoch := runtimequota.HostEpoch{NodeID: node.ID, HostEpochID: uuid.NewString()}
			vmm := &fakeVMM{statsHook: func(_ context.Context, requested string) (*StatsSnapshot, error) {
				if requested != node.ID {
					t.Fatal("discovery used an unregistered destination")
				}
				return &StatsSnapshot{RegionalRuntimeHostEpoch: &epoch}, nil
			}}
			// The mandatory Store interface carries issuance through decorators.
			engine := &Engine{store: struct{ state.Store }{store}, vmm: vmm}
			spec := AppSpec{AppID: app.ID, AccountID: account.ID, DeploymentID: dep.ID, Plan: account.Plan, PrimeOnly: purpose == runtimequota.CapacityPrime}
			instance, run := uuid.NewString(), uuid.NewString()
			issued, err := engine.issueRegionalRuntimeLaunch(t.Context(), node.ID, instance, run, purpose, spec, head.Grant)
			if err != nil || issued.RegionalRuntime == nil {
				t.Fatal("initial bookkeeping was not issued", err)
			}
			again, err := engine.issueRegionalRuntimeLaunch(t.Context(), node.ID, instance, run, purpose, issued, head.Grant)
			if err != nil || again.RegionalRuntime.Permit != issued.RegionalRuntime.Permit || !sameInitialServingSeat(again.RegionalRuntime.Seat, issued.RegionalRuntime.Seat) {
				t.Fatal("retry minted another attempt", err)
			}
			usage, err := store.RegionalRuntimeCapacityUsage(t.Context(), app.ID, head.Grant.AllocationID)
			if err != nil || usage.TotalHeld != 1 || usage.IssuedHeld != 1 {
				t.Fatal("retry charged more physical holds", usage, err)
			}
			if _, err := engine.prepareRegionalRuntimeLaunch(t.Context(), node.ID, instance, run, purpose, issued); !errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
				t.Fatal("issued bookkeeping activated dispatch", err)
			}
			if _, err := store.FenceRegionalRuntimeCapacity(t.Context(), app.ID, issued.RegionalRuntime.Permit.ID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.issueRegionalRuntimeLaunch(t.Context(), node.ID, instance, run, purpose, issued, head.Grant); !errors.Is(err, state.ErrRegionalRuntimePlacementUnavailable) {
				t.Fatal("scheduler reopened a fenced physical attempt", err)
			}
			usage, err = store.RegionalRuntimeCapacityUsage(t.Context(), app.ID, head.Grant.AllocationID)
			if err != nil || usage.IssuedHeld != 1 || vmm.coldBoots != 0 || vmm.restores != 0 {
				t.Fatal("issuance dispatched or released work", usage, err)
			}
		})
	}
}

func TestRegionalRuntimeSchedulerLaunchRefusesUnknownEpochAndIdentity(t *testing.T) {
	for _, fail := range []string{"missing-epoch", "cross-node", "stats-error", "unknown-node", "plan", "prime-purpose", "account", "deployment", "receipt-error"} {
		t.Run(fail, func(t *testing.T) {
			store, account, app, dep := regionalQuotaSchedulerFixture(t)
			freezeRegionalSchedulerApp(t, store, account, app)
			head, err := store.RegionalRuntimeQuotaHead(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "regional-node", TargetURL: "unix:///run/faas/vmmd-test.sock", Active: true})
			if err != nil {
				t.Fatal(err)
			}
			epoch := runtimequota.HostEpoch{NodeID: node.ID, HostEpochID: uuid.NewString()}
			unavailable := errors.New("regional receipt store unavailable")
			vmm := &fakeVMM{statsHook: func(context.Context, string) (*StatsSnapshot, error) {
				switch fail {
				case "missing-epoch":
					return &StatsSnapshot{}, nil
				case "cross-node":
					epoch.NodeID = uuid.NewString()
				case "stats-error":
					return nil, unavailable
				}
				return &StatsSnapshot{RegionalRuntimeHostEpoch: &epoch}, nil
			}}
			var decorated state.Store = struct{ state.Store }{store}
			if fail == "receipt-error" {
				decorated = failingRegionalPlacementWriter{Store: decorated, err: unavailable}
			}
			engine := &Engine{store: decorated, vmm: vmm}
			spec := AppSpec{AppID: app.ID, AccountID: account.ID, DeploymentID: dep.ID, Plan: account.Plan}
			switch fail {
			case "unknown-node":
				node.ID = uuid.NewString()
			case "plan":
				spec.Plan = api.PlanScale
			case "prime-purpose":
				spec.PrimeOnly = true
			case "account":
				spec.AccountID = uuid.NewString()
			case "deployment":
				spec.DeploymentID = "missing"
			}
			if _, err := engine.issueRegionalRuntimeLaunch(t.Context(), node.ID, uuid.NewString(), uuid.NewString(), runtimequota.CapacityServing, spec, head.Grant); err == nil {
				t.Fatal("incomplete regional launch was issued")
			} else if (fail == "stats-error" || fail == "receipt-error") && !errors.Is(err, unavailable) {
				t.Fatal("storage/transport error hidden", err)
			}
			usage, err := store.RegionalRuntimeCapacityUsage(t.Context(), app.ID, head.Grant.AllocationID)
			if err != nil || usage.TotalHeld != 0 {
				t.Fatal("refusal left a physical hold", usage, err)
			}
		})
	}
}

type failingRegionalPlacementWriter struct {
	state.Store
	err error
}

func (s failingRegionalPlacementWriter) IssueRegionalRuntimePlacement(context.Context, runtimequota.Placement) error {
	return s.err
}

func TestIndependentSchedulerLaunchSkipsRegionalDiscovery(t *testing.T) {
	store, account, app, dep := regionalQuotaSchedulerFixture(t)
	vmm := &fakeVMM{statsHook: func(context.Context, string) (*StatsSnapshot, error) {
		t.Fatal("independent launch queried regional discovery")
		return nil, nil
	}}
	engine := &Engine{store: struct{ state.Store }{store}, vmm: vmm}
	spec := AppSpec{AppID: app.ID, AccountID: account.ID, DeploymentID: dep.ID, Plan: account.Plan}
	prepared, err := engine.prepareRegionalRuntimeLaunch(t.Context(), "default-local", "opaque-instance", "", runtimequota.CapacityServing, spec)
	if err != nil || prepared.RegionalRuntime != nil || prepared.AppID != spec.AppID {
		t.Fatal("independent launch changed", err)
	}
	spec.RegionalRuntime = &runtimequota.Placement{}
	if _, err := engine.prepareRegionalRuntimeLaunch(t.Context(), "default-local", "opaque-instance", "", runtimequota.CapacityServing, spec); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal("orphan placement silently discarded", err)
	}
}
