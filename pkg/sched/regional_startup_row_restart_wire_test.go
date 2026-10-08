package sched_test

// adr: 660
// adr: 661
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
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

func fencedStartupRowFixture(t *testing.T) (retirementCellFixture, state.Instance, runtimequota.LaunchSource, *retirementWireVMM) {
	t.Helper()
	var ins state.Instance
	f := newRetirementCell(t, false, func(store *state.MemStore, app state.App) state.Instance {
		node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "fenced-startup", TargetURL: "unix:///run/faas/fenced-startup.sock", Active: true, AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: api.VCPUSlots, VPCPUs: 4})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		ins, err = store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateColdBooting), 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return ins
	})
	source := runtimequota.LaunchSource{Permit: f.p, InstanceKey: ins.ID}
	if err := f.store.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: f.p}, source, state.RegionalRuntimeResources{RAMMB: 128, VCPU: 1, CPUMillicores: api.DefaultAppCPUMillicores}); err != nil {
		t.Fatal(err)
	}
	return f, ins, source, &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store, firstFence: uuid.NewString(), wantInstance: ins.ID}
}

func startupRestartEngine(t *testing.T, store state.Store, router *sched.VMMRouter, owner *sched.RegionalRuntimeRetirement) (*sched.Engine, *sched.NodeLedger) {
	t.Helper()
	ledger := sched.NewNodeLedger()
	e, err := sched.NewEngine(t.Context(), store, ledger, router, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.WithRegionalRuntimeRetirement(owner)
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	return e, ledger
}

// Native stop simulation runs over real authenticated transport. No native VM
// process is launched, and these checks do not establish lifecycle acceptance.
func TestRegionalStartupRowRestartRecoversOriginalFirstFence(t *testing.T) {
	for _, failure := range []string{"before-native", "lost-native-reply", "lost-row-write"} {
		t.Run(failure, func(t *testing.T) {
			f, ins, source, v := fencedStartupRowFixture(t)
			var lost atomic.Bool
			interceptor := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				response, err := handler(ctx, req)
				if failure == "lost-native-reply" && err == nil && !lost.Swap(true) {
					return nil, status.Error(codes.Unavailable, "lost original stop reply")
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
			initialOwner := owner
			if failure == "before-native" {
				initialOwner = nil
			}
			initial, _ := startupRestartEngine(t, store, router, initialOwner)
			if err := initial.FailOriginalRegionalRuntime(t.Context(), source, "original_failed"); err == nil {
				t.Fatal("injected failure was ignored")
			}
			first, err := f.store.RegionalRuntimeCapacity(t.Context(), ins.AppID, f.p.ID)
			if err != nil || first.FenceID == "" {
				t.Fatal("first cell fence was not retained", err)
			}
			// A new ledger has no in-memory native-source binding from its predecessor.
			restarted, ledger := startupRestartEngine(t, f.store, router, owner)
			want := 2 * api.BillableRAMMB(128)
			wantNativeCalls := int64(1)
			if failure == "lost-row-write" {
				want = api.BillableRAMMB(128)
			}
			if failure == "lost-native-reply" {
				wantNativeCalls = 2
			}
			if ledger.ResidentRAM() != want || !ledger.ResidentFor(ins.ID) {
				t.Fatal("restart discarded unproven startup charges", ledger.ResidentRAM(), want)
			}
			_, completed, err := restarted.RecoverRegionalRuntimeFailures(t.Context(), state.RegionalRuntimeRetirementCursor{})
			if err != nil || completed != 1 || ledger.ResidentRAM() != 0 || ledger.UsedVCPU() != 0 || ledger.UsedCPUMillicoresForNode(ins.NodeID) != 0 || ledger.ResidentFor(ins.ID) || v.calls.Load() != wantNativeCalls {
				t.Fatal("original stop did not retire matching reconstructed charges", completed, err, ledger.ResidentRAM(), v.calls.Load())
			}
			row, err := f.store.InstanceByID(t.Context(), ins.ID)
			if err != nil || row.State != string(state.StateFailed) || row.TerminalAt == nil {
				t.Fatal("matching original row was not completed", row, err)
			}
			receipt, err := f.store.RegionalRuntimeCapacity(t.Context(), ins.AppID, f.p.ID)
			if err != nil || receipt.ReleasedAt == nil || receipt.StopEnvelope == nil || receipt.FenceID != first.FenceID {
				t.Fatal("recovery changed the first accepted receipt", err)
			}
			stop, err := f.authority.Cells.VerifyCapacityStop(*receipt.StopEnvelope)
			if err != nil || stop.Permit != f.p || stop.FenceID != first.FenceID || stop.HostFenceID != v.firstFence {
				t.Fatal("original host/cell fences changed", err)
			}
			if first.StopEnvelope != nil && *first.StopEnvelope != *receipt.StopEnvelope {
				t.Fatal("accepted stop replay was re-signed")
			}
		})
	}
}

type alteredRetirementReplay struct {
	state.RegionalRuntimeCapacityLedger
	alter func(*state.RegionalRuntimeCapacityReceipt)
}

func (s alteredRetirementReplay) BeginRegionalRuntimeRetirement(ctx context.Context, p runtimequota.CapacityPermit, fence string) (state.RegionalRuntimeCapacityReceipt, error) {
	r, err := s.RegionalRuntimeCapacityLedger.BeginRegionalRuntimeRetirement(ctx, p, fence)
	if err == nil && r.ReleasedAt != nil {
		s.alter(&r)
	}
	return r, err
}

func TestRegionalStartupRowRestartRequiresVerifiedAcceptedStopReplay(t *testing.T) {
	for _, scenario := range []string{"valid", "signature", "claims", "other-run", "rotated-trusted", "rotated-without-history"} {
		t.Run(scenario, func(t *testing.T) {
			f, ins, source, v := fencedStartupRowFixture(t)
			router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, nil)
			owner, err := sched.NewRegionalRuntimeRetirement(f.store, runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}, router)
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := owner.Retire(t.Context(), source.InstanceKey, f.p, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			alter := func(r *state.RegionalRuntimeCapacityReceipt) {
				switch scenario {
				case "signature":
					e := *r.StopEnvelope
					e.Signature = strings.Repeat("A", len(e.Signature))
					r.StopEnvelope = &e
				case "claims":
					stop := *r.Stop
					stop.HostFenceID = uuid.NewString()
					r.Stop = &stop
				case "other-run":
					stop := *r.Stop
					stop.Permit.RunID = uuid.NewString()
					e, err := f.signer.SignCapacityStop(stop)
					if err != nil {
						t.Fatal(err)
					}
					r.StopEnvelope = &e
				}
			}
			store := alteredRetirementReplay{RegionalRuntimeCapacityLedger: f.store, alter: alter}
			keys := runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}
			if strings.HasPrefix(scenario, "rotated-") {
				pub, private, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				keys.Signer, err = runtimequota.NewSigner(f.signer.IssuerID(), private)
				if err != nil {
					t.Fatal(err)
				}
				anchors := []runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: f.signer.IssuerID(), Public: pub}}
				if scenario == "rotated-trusted" {
					old, err := f.signer.PublicAnchor(runtimequota.Cell)
					if err != nil {
						t.Fatal(err)
					}
					anchors = append(anchors, old)
				}
				keys.Trust, err = runtimequota.NewTrust(anchors)
				if err != nil {
					t.Fatal(err)
				}
			}
			owner, err = sched.NewRegionalRuntimeRetirement(store, keys, router)
			if err != nil {
				t.Fatal(err)
			}
			restarted, ledger := startupRestartEngine(t, f.store, router, owner)
			_, completed, err := restarted.RecoverRegionalRuntimeFailures(t.Context(), state.RegionalRuntimeRetirementCursor{})
			valid := scenario == "valid" || scenario == "rotated-trusted"
			if valid {
				if err != nil || completed != 1 || ledger.ResidentRAM() != 0 {
					t.Fatal("trusted replay did not clear the original row charge", completed, err)
				}
			} else {
				row, rowErr := f.store.InstanceByID(t.Context(), ins.ID)
				if err == nil || completed != 0 || ledger.ResidentRAM() != api.BillableRAMMB(128) || rowErr != nil || row.State != ins.State {
					t.Fatal("unverified replay changed startup accounting or row", completed, err, rowErr)
				}
			}
			if v.calls.Load() != 1 {
				t.Fatal("accepted replay dispatched another native stop", v.calls.Load())
			}
			retained, err := f.store.RegionalRuntimeCapacity(t.Context(), ins.AppID, f.p.ID)
			if err != nil || retained.FenceID != accepted.FenceID || *retained.StopEnvelope != *accepted.StopEnvelope || *retained.Stop != *accepted.Stop {
				t.Fatal("replay replaced original accepted proof", err)
			}
		})
	}
}
