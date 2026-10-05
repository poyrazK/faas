package sched_test

// adr: 634
import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failRegionalRowOnce struct {
	state.Store
	failed atomic.Bool
}

func (s *failRegionalRowOnce) FailRegionalRuntimeAttempt(ctx context.Context, source runtimequota.LaunchSource, expected string) error {
	if !s.failed.Swap(true) {
		return errors.New("lost scheduler database write")
	}
	return s.Store.FailRegionalRuntimeAttempt(ctx, source, expected)
}

func TestRegionalRuntimeFailureRetainsUncertainNativeAndRecoversRow(t *testing.T) {
	for _, failure := range []string{"unconfigured", "lost-native-reply", "lost-row-write"} {
		t.Run(failure, func(t *testing.T) {
			var ins state.Instance
			f := newRetirementCell(t, false, func(store *state.MemStore, app state.App) state.Instance {
				dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
				if err != nil {
					t.Fatal(err)
				}
				ins, err = store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateColdBooting), 128, uuid.NewString(), uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				return ins
			})
			source := runtimequota.LaunchSource{Permit: f.p, InstanceKey: ins.ID}
			if err := f.store.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: f.p}, source); err != nil {
				t.Fatal(err)
			}
			ledger := sched.NewNodeLedger()
			if err := ledger.Admit(sched.Request{Instance: ins.ID, AppID: ins.AppID, DeploymentID: ins.DeploymentID, NodeID: ins.NodeID, RuntimeRunID: ins.WakeID, RegionalRuntime: &source, Plan: api.PlanPro, RAMMB: 128, VCPU: 1, MaxConcurrency: 1}); err != nil {
				t.Fatal(err)
			}
			v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store, wantInstance: ins.ID}
			var lost atomic.Bool
			interceptor := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				response, err := handler(ctx, req)
				if failure == "lost-native-reply" && err == nil && !lost.Swap(true) {
					return nil, status.Error(codes.Unavailable, "lost native reply")
				}
				return response, err
			}
			router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, interceptor)
			owner, err := sched.NewRegionalRuntimeRetirement(f.store, runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}, router)
			if err != nil {
				t.Fatal(err)
			}
			var store state.Store = f.store
			if failure == "lost-row-write" {
				store = &failRegionalRowOnce{Store: store}
			}
			engine, err := sched.NewEngine(t.Context(), store, ledger, router, nil, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if failure != "unconfigured" {
				engine.WithRegionalRuntimeRetirement(owner)
			}
			if err := engine.FailOriginalRegionalRuntime(t.Context(), source, "launch_failed"); err == nil {
				t.Fatal("injected uncertainty was ignored")
			}
			before, err := f.store.InstanceByID(t.Context(), ins.ID)
			if err != nil || before.State != ins.State || before.TerminalAt != nil {
				t.Fatal("uncertain cleanup changed row", before, err)
			}
			r, err := f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
			if err != nil || r.FenceID == "" {
				t.Fatal("failure lost its durable first fence", r, err)
			}
			if failure == "lost-row-write" {
				if ledger.ResidentFor(ins.ID) || r.ReleasedAt == nil {
					t.Fatal("accepted native absence still charged RAM", r)
				}
			} else if !ledger.ResidentFor(ins.ID) || r.ReleasedAt != nil {
				t.Fatal("unconfirmed native stop released capacity", r)
			}
			firstFence := r.FenceID
			engine.WithRegionalRuntimeRetirement(owner)
			_, completed, err := engine.RecoverRegionalRuntimeFailures(t.Context(), state.RegionalRuntimeRetirementCursor{})
			if err != nil || completed != 1 {
				t.Fatal("source recovery failed", completed, err)
			}
			after, err := f.store.InstanceByID(t.Context(), ins.ID)
			if err != nil || after.State != string(state.StateFailed) || after.TerminalAt == nil || ledger.ResidentFor(ins.ID) {
				t.Fatal("recovery did not complete original scheduler attempt", after, err)
			}
			r, err = f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
			if err != nil || r.ReleasedAt == nil || r.FenceID != firstFence || r.StopEnvelope == nil {
				t.Fatal("recovery changed or lost original stop", r, err)
			}
			wantCalls := int64(1)
			if failure == "lost-native-reply" {
				wantCalls = 2
			}
			if v.calls.Load() != wantCalls {
				t.Fatal("accepted stop was dispatched again", v.calls.Load(), wantCalls)
			}
		})
	}
}
