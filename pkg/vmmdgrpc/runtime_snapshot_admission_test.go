package vmmdgrpc_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type admittedSnapshotNativeVMM struct {
	*admittedNativeVMM
	captures int
	edit     func(*fcvm.SnapshotInfo, *runtimeadmission.SnapshotAcknowledgment)
}

func (v *admittedSnapshotNativeVMM) CaptureAdmitted(_ context.Context, g runtimeadmission.SnapshotGrant) (fcvm.SnapshotInfo, runtimeadmission.SnapshotAcknowledgment, error) {
	v.captures++
	now := time.Now().UnixNano()
	digest := "sha256:" + strings.Repeat("e", 64)
	c := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: g.Parent.Clone(), Memory: runtimeadmission.CapturedArtifact{StorageKey: g.MemoryKey, Digest: digest, Bytes: 16384}, VMState: runtimeadmission.CapturedArtifact{StorageKey: g.VMStateKey, Digest: digest, Bytes: 4096}, PrivateDrive: runtimeadmission.CapturedArtifact{StorageKey: g.PrivateDriveKey, Digest: digest, Bytes: 8192}, CapturedAtUnixNano: now}
	i := fcvm.SnapshotInfo{MemBytes: c.Memory.Bytes, VMStateBytes: c.VMState.Bytes, StoredBytes: 4096, Capture: c.Clone()}
	a := runtimeadmission.SnapshotAcknowledgment{Grant: g.Clone(), Capture: c.Clone(), CompletedAtUnixNano: now}
	if v.edit != nil {
		v.edit(&i, &a)
	}
	return i, a, nil
}

func admittedSnapshotRPCFixture(t *testing.T) (*vmmdgrpc.Server, *admittedSnapshotNativeVMM, runtimeadmission.SnapshotGrant) {
	t.Helper()
	_, original, req := admittedRPCFixture(t)
	original.identity.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	b, _ := runtimeadmission.BindingFromProto(req.Binding)
	b.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	sources := []runtimeadmission.ArtifactSource{{Kind: "base-image", StorageKey: "base", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 4096}, {Kind: "app-layer", StorageKey: "layer", Digest: "sha256:" + strings.Repeat("b", 64), Bytes: 8192}}
	b.ArtifactSourcesHash, _ = runtimeadmission.HashArtifactSources(sources)
	consumption := runtimeadmission.ArtifactConsumption{ConfigHash: strings.Repeat("d", 64), ProcessPID: 42, ProcessStart: "101"}
	for _, source := range sources {
		consumption.Drives = append(consumption.Drives, runtimeadmission.ConsumedDrive{Source: source, DriveID: source.Role(), ReadOnly: source.Role() == "base", RootDevice: source.Role() == "base", ProducerDigest: source.Digest, ProducerBytes: source.Bytes, InjectedDigest: source.Digest, InjectedBytes: source.Bytes})
	}
	now := time.Now()
	parent := runtimeadmission.Receipt{Binding: b, NativeInputHash: strings.Repeat("c", 64), Netns: "fc-snapshot", HostIP: "10.100.0.2", LeaseUID: 20000, Method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, CompletedAtUnixNano: now.UnixNano(), ArtifactConsumption: consumption}
	token := uuid.NewString()
	key := state.SnapshotCaptureMemKey(b.DeploymentID, state.SnapshotTierInit, token)
	g := runtimeadmission.SnapshotGrant{Version: runtimeadmission.SnapshotGrantVersion, Token: token, Parent: parent, MemoryKey: key, VMStateKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), PrivateDriveKey: state.SnapshotDriveKey(state.Snapshot{StorageKey: key}), FCVersion: "1.10.0", Mode: "park", SourceStartedAtUnixNano: now.UnixNano(), IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	if err := g.Validate(now); err != nil {
		t.Fatal(err)
	}
	v := &admittedSnapshotNativeVMM{admittedNativeVMM: original}
	return vmmdgrpc.New(v, wire.NewOpsMetrics("vmmd_test"), g.FCVersion, nil).WithNodeID(b.NodeID), v, g
}

func TestCaptureAdmittedRuntimeServerPreservesExactNativeAcknowledgment(t *testing.T) {
	s, v, g := admittedSnapshotRPCFixture(t)
	p, err := s.CaptureAdmittedRuntime(admittedRPCContext(t), &vmmdpb.CaptureAdmittedRuntimeRequest{Grant: g.ToProto()})
	if err != nil || v.captures != 1 {
		t.Fatal("capture did not reach admitted native backend", err)
	}
	a, err := runtimeadmission.CheckAdmittedSnapshotResponse(p, g)
	if err != nil || a.Check(g, time.Now()) != nil {
		t.Fatal("RPC altered native acknowledgment", err)
	}
	if len(v.destroyed) != 0 {
		t.Fatal("valid capture triggered cleanup")
	}
}

func TestCaptureAdmittedRuntimeServerRejectsPeerAndStaleGrantBeforeBackend(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*vmmdpb.CaptureAdmittedRuntimeRequest)
		ctx  func(*testing.T) context.Context
	}{
		{name: "unknown control", edit: func(p *vmmdpb.CaptureAdmittedRuntimeRequest) { p.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) }},
		{name: "unknown parent drive", edit: func(p *vmmdpb.CaptureAdmittedRuntimeRequest) {
			p.Grant.Parent.ArtifactConsumption.Drives[0].Source.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{name: "node", edit: func(p *vmmdpb.CaptureAdmittedRuntimeRequest) { p.Grant.Parent.Binding.NodeId = uuid.NewString() }},
		{name: "incarnation", edit: func(p *vmmdpb.CaptureAdmittedRuntimeRequest) { p.Grant.Parent.Binding.Incarnation = uuid.NewString() }},
		{name: "Firecracker version", edit: func(p *vmmdpb.CaptureAdmittedRuntimeRequest) { p.Grant.FcVersion += "-different" }},
		{name: "missing", edit: func(p *vmmdpb.CaptureAdmittedRuntimeRequest) { p.Grant = nil }},
		{name: "missing peer", ctx: func(t *testing.T) context.Context { return t.Context() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, v, g := admittedSnapshotRPCFixture(t)
			p := &vmmdpb.CaptureAdmittedRuntimeRequest{Grant: g.ToProto()}
			if tc.edit != nil {
				tc.edit(p)
			}
			ctx := admittedRPCContext(t)
			if tc.ctx != nil {
				ctx = tc.ctx(t)
			}
			got, err := s.CaptureAdmittedRuntime(ctx, p)
			if err == nil || got != nil || v.captures != 0 || len(v.destroyed) != 0 {
				t.Fatal("rejected grant reached native operation", err)
			}
		})
	}
}

func TestCaptureAdmittedRuntimeServerRejectsBackendProofAndCleansUp(t *testing.T) {
	for _, edit := range []func(*fcvm.SnapshotInfo, *runtimeadmission.SnapshotAcknowledgment){
		func(_ *fcvm.SnapshotInfo, a *runtimeadmission.SnapshotAcknowledgment) {
			a.Grant.Token = uuid.NewString()
		},
		func(i *fcvm.SnapshotInfo, _ *runtimeadmission.SnapshotAcknowledgment) { i.MemBytes++ },
		func(i *fcvm.SnapshotInfo, _ *runtimeadmission.SnapshotAcknowledgment) {
			i.Capture = runtimeadmission.SnapshotCapture{}
		},
	} {
		s, v, g := admittedSnapshotRPCFixture(t)
		v.edit = edit
		p, err := s.CaptureAdmittedRuntime(admittedRPCContext(t), &vmmdpb.CaptureAdmittedRuntimeRequest{Grant: g.ToProto()})
		if err == nil || p != nil || v.captures != 1 || len(v.destroyed) != 1 || v.destroyed[0] != g.Parent.Binding.InstanceID {
			t.Fatal("invalid backend proof escaped or retained native resources", err)
		}
	}
}

func TestCaptureAdmittedRuntimeServerUnavailableWithoutNativeCapability(t *testing.T) {
	_, v, g := admittedSnapshotRPCFixture(t)
	s := vmmdgrpc.New(v.admittedNativeVMM, wire.NewOpsMetrics("vmmd_test"), g.FCVersion, nil).WithNodeID(g.Parent.Binding.NodeID)
	p, err := s.CaptureAdmittedRuntime(admittedRPCContext(t), &vmmdpb.CaptureAdmittedRuntimeRequest{Grant: g.ToProto()})
	if status.Code(err) != codes.Unimplemented || p != nil {
		t.Fatal("old backend accepted admitted capture", err)
	}
}
