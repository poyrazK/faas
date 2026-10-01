// adr: 386 — save a native grant before boot and publish only its matching receipt.

package sched

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

// A simulated native consumer for scheduler/storage integration only. Native
// manager/adapter and dedicated metal acceptance remain independent gates.
type standardNativeTestVMM struct {
	*fakeVMM
	identity     runtimeadmission.Identity
	revision     int64
	allowlist    []string
	ports        []int
	lastRequest  *vmmdpb.CreateAdmittedRuntimeRequest
	beforeNative func()
	omitReceipt  bool
}

func standardNativeTestNodeID(t *testing.T, s state.Store) string {
	t.Helper()
	node, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	return node.ID
}

func newStandardNativeTestVMM(t *testing.T, s state.ComputeNodeRuntimeIdentityStore, vmm *fakeVMM, nodeID string) *standardNativeTestVMM {
	t.Helper()
	identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: nodeID, Incarnation: uuid.NewString()}
	if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	return &standardNativeTestVMM{fakeVMM: vmm, identity: identity}
}
func (f *standardNativeTestVMM) RuntimeAdmissionIdentity(context.Context, string) (runtimeadmission.Identity, error) {
	return f.identity, nil
}

func (f *standardNativeTestVMM) CreatePausedFromSnapshot(ctx context.Context, nodeID, instanceID string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	return f.fakeVMM.CreateFromSnapshot(ctx, nodeID, instanceID, app, snap)
}
func (f *standardNativeTestVMM) UpdateAppEgressPolicy(_ context.Context, nodeID, appID string, revision int64, cidrs []netip.Prefix, ports []int) error {
	if nodeID != f.identity.NodeID || appID == "" || revision <= 0 {
		return runtimeadmission.ErrInvalid
	}
	f.revision = revision
	f.allowlist = nil
	for _, p := range cidrs {
		f.allowlist = append(f.allowlist, p.String())
	}
	f.ports = slices.Clone(ports)
	return nil
}
func (f *standardNativeTestVMM) CreateAdmittedRuntime(ctx context.Context, nodeID string, req *vmmdpb.CreateAdmittedRuntimeRequest) (*WakeOutcome, error) {
	binding, err := runtimeadmission.BindingFromProto(req.GetBinding())
	if err != nil || binding.Validate(time.Now()) != nil || nodeID != f.identity.NodeID || binding.Incarnation != f.identity.Incarnation || binding.EgressRevision != f.revision {
		return nil, runtimeadmission.ErrInvalid
	}
	hash, err := runtimeadmission.HashBootPayload(req)
	if err != nil || hash != binding.PayloadHash {
		return nil, runtimeadmission.ErrInvalid
	}
	f.lastRequest = req
	if f.beforeNative != nil {
		f.beforeNative()
	}
	var out *WakeOutcome
	app := AppSpec{AppID: binding.AppID, AccountID: binding.AccountID, DeploymentID: binding.DeploymentID}
	paused := false
	if restore := req.GetRestore(); restore != nil {
		if !slices.Equal(f.allowlist, restore.App.EgressAllowlist) {
			return nil, runtimeadmission.ErrStale
		}
		paused = restore.KeepPaused
		out, err = f.fakeVMM.CreateFromSnapshot(ctx, nodeID, binding.InstanceID, app, SnapshotRef{DeploymentID: binding.DeploymentID, StorageKey: restore.Snapshot.StorageKey})
	} else {
		if req.GetColdBoot() == nil || !slices.Equal(f.allowlist, req.GetColdBoot().App.EgressAllowlist) {
			return nil, runtimeadmission.ErrStale
		}
		out, err = f.fakeVMM.CreateColdBoot(ctx, nodeID, binding.InstanceID, app)
	}
	if err != nil {
		return nil, err
	}
	if !f.omitReceipt {
		r := runtimeadmission.Receipt{Binding: binding, NativeInputHash: strings.Repeat("b", 64), Netns: out.Netns, HostIP: out.HostIP, LeaseUID: out.LeaseUID, Method: out.Method, Paused: paused, CompletedAtUnixNano: time.Now().UnixNano()}
		out.RuntimeAdmissionReceipt = &r
	}
	return out, nil
}

func TestApplicationStandardNativeWakeRequiresDurableGrant(t *testing.T) {
	s, app, dep := newStandardCaptureService(t)
	vmm := newStandardNativeTestVMM(t, s, &fakeVMM{}, standardNativeTestNodeID(t, s))
	vmm.beforeNative = func() {
		b, _ := runtimeadmission.BindingFromProto(vmm.lastRequest.Binding)
		saved, err := s.IssueInstanceApplicationStandardBoot(t.Context(), string(state.StateColdBooting), b)
		if err != nil || saved != b {
			t.Fatalf("native RPC preceded durable grant: %+v %v", saved, err)
		}
	}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(rows) != 1 || rows[0].State != string(state.StateRunning) || rows[0].HostIP == "" {
		t.Fatalf("native wake was not published: %+v %v", rows, err)
	}
	enrollment, _ := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if enrollment.ObservedRevision != 0 {
		t.Fatal("boot receipt fabricated full policy observation")
	}
}

func TestApplicationStandardNativeWakeRefusesMissingCapabilityAndReceipt(t *testing.T) {
	for _, missingReceipt := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-node", true: "missing-receipt"}[missingReceipt], func(t *testing.T) {
			s, app, dep := newStandardCaptureService(t)
			legacy := &fakeVMM{}
			var vmm RoutedVMM = legacy
			if missingReceipt {
				native := newStandardNativeTestVMM(t, s, legacy, standardNativeTestNodeID(t, s))
				native.omitReceipt = true
				vmm = native
			}
			e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
			_, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway)
			if err == nil || !missingReceipt && !errors.Is(err, runtimeadmission.ErrUnavailable) {
				t.Fatalf("managed node manufactured capability: %v", err)
			}
			wantBoot, wantDestroy := 0, 0
			if missingReceipt {
				wantBoot, wantDestroy = 1, 1
			}
			if legacy.coldBoots != wantBoot || legacy.destroys != wantDestroy || e.Ledger().ResidentRAM() != 0 {
				t.Fatalf("invalid native path leaked: boots=%d destroys=%d RAM=%d", legacy.coldBoots, legacy.destroys, e.Ledger().ResidentRAM())
			}
			rows, _ := s.ListInstancesForApp(t.Context(), app.ID)
			if len(rows) != 1 || rows[0].State != string(state.StateFailed) || rows[0].HostIP != "" {
				t.Fatalf("invalid native path published: %+v", rows)
			}
		})
	}
}

func TestApplicationStandardNativeSnapshotAndWarmRestore(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(map[bool]string{false: "snapshot-wake", true: "paused-warm-restore"}[warm], func(t *testing.T) {
			s, app, dep := newStandardCaptureService(t)
			if _, err := s.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, Tier: state.SnapshotTierInit, FCVersion: "1.10.0", MemBytes: int64(app.RAMMB) << 20, StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "native-grant")}); err != nil {
				t.Fatal(err)
			}
			vmm := newStandardNativeTestVMM(t, s, &fakeVMM{}, standardNativeTestNodeID(t, s))
			e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
			want := state.StateRunning
			if warm {
				target := 1
				if _, err := s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWarmPoolSize: true, WarmPoolSize: &target}); err != nil {
					t.Fatal(err)
				}
				if err := e.ReconcileWarmPool(t.Context(), app.ID); err != nil {
					t.Fatal(err)
				}
				want = state.StateWarm
			} else if _, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway); err != nil {
				t.Fatal(err)
			}
			rows, err := s.ListInstancesForApp(t.Context(), app.ID)
			if err != nil || len(rows) != 1 || rows[0].State != string(want) || rows[0].HostIP == "" || vmm.restores != 1 || vmm.coldBoots != 0 || vmm.lastRequest.GetRestore() == nil || vmm.lastRequest.GetRestore().KeepPaused != warm {
				t.Fatalf("native restore did not publish its actual paused intent: rows=%+v restore=%d cold=%d err=%v", rows, vmm.restores, vmm.coldBoots, err)
			}
		})
	}
}
