// adr: 435 — native boot clients validate backend receipts and complete payload bindings.

package sched_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/protobuf/proto"
)

type admittedWireServer struct {
	vmmdpb.UnimplementedVmmdServer
	identity  runtimeadmission.Identity
	requests  int
	destroyed []string
	mutate    func(*vmmdpb.CreateAdmittedRuntimeResponse)
}

func (s *admittedWireServer) RuntimeAdmissionIdentity(context.Context, *vmmdpb.RuntimeAdmissionIdentityRequest) (*vmmdpb.RuntimeAdmissionIdentityResponse, error) {
	return &vmmdpb.RuntimeAdmissionIdentityResponse{ProtocolVersion: s.identity.ProtocolVersion, NodeId: s.identity.NodeID, Incarnation: s.identity.Incarnation, SnapshotRestoreVersion: s.identity.SnapshotRestoreVersion}, nil
}

func TestVMMClientSnapshotRestoreIdentityPreservesCapability(t *testing.T) {
	for _, version := range []uint32{0, runtimeadmission.SnapshotRestoreVersion, runtimeadmission.SnapshotRestoreVersion + 1} {
		identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ArtifactProtocolVersion, NodeID: uuid.NewString(), Incarnation: uuid.NewString(), SnapshotRestoreVersion: version}
		server := &admittedWireServer{identity: identity}
		client := newPolicyWireClient(t, server)
		got, err := client.RuntimeAdmissionIdentity(t.Context())
		if version > runtimeadmission.SnapshotRestoreVersion {
			if err == nil {
				t.Fatal("unsupported capability accepted")
			}
			continue
		}
		if err != nil || got != identity {
			t.Fatal("snapshot capability lost on wire", err)
		}
	}
}

func TestVMMClientSnapshotRestoreUnboundEvidenceRefusedBeforeRPC(t *testing.T) {
	req, _, _ := preparedWireFixture(t, false)
	req.SnapshotRestore = &vmmdpb.RuntimeSnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: uuid.NewString()}
	req.Binding.PayloadHash, _ = runtimeadmission.HashBootPayload(req)
	server := &admittedWireServer{}
	client := newPolicyWireClient(t, server)
	if _, err := client.CreateAdmittedRuntime(t.Context(), req); err == nil || server.requests != 0 {
		t.Fatal("unbound evidence entered native RPC", err)
	}
}
func (s *admittedWireServer) Destroy(_ context.Context, req *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	s.destroyed = append(s.destroyed, req.GetInstance())
	return &vmmdpb.DestroyResponse{}, nil
}
func (s *admittedWireServer) CreateAdmittedRuntime(_ context.Context, req *vmmdpb.CreateAdmittedRuntimeRequest) (*vmmdpb.CreateAdmittedRuntimeResponse, error) {
	s.requests++
	b, _ := runtimeadmission.BindingFromProto(req.Binding)
	method := vmmdpb.WakeMethod_WAKE_COLD_BOOT
	if req.GetRestore() != nil {
		method = vmmdpb.WakeMethod_WAKE_RESTORE
	}
	receipt := runtimeadmission.Receipt{Binding: b, NativeInputHash: strings.Repeat("d", 64), Netns: "fc-runtime", HostIP: "10.100.0.2", LeaseUID: 20000, Method: method, Paused: req.GetRestore().GetKeepPaused(), CompletedAtUnixNano: time.Now().UnixNano()}
	resp := &vmmdpb.CreateAdmittedRuntimeResponse{Receipt: receipt.ToProto(), Runtime: &vmmdpb.WakeResponse{Instance: b.InstanceID, Netns: receipt.Netns, HostIp: receipt.HostIP, LeaseUid: receipt.LeaseUID, Method: method, RequestedMethod: method}}
	if s.mutate != nil {
		s.mutate(resp)
	}
	return resp, nil
}

func preparedWireFixture(t *testing.T, paused bool) (*vmmdpb.CreateAdmittedRuntimeRequest, runtimeadmission.Binding, sched.AppSpec) {
	t.Helper()
	now := time.Now()
	b := runtimeadmission.Binding{ProtocolVersion: runtimeadmission.ProtocolVersion, Token: uuid.NewString(), InstanceID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), AccountID: uuid.NewString(), NodeID: uuid.NewString(), Incarnation: uuid.NewString(), DesiredRevision: 9, EffectiveHash: strings.Repeat("a", 64), CapturedInputHash: strings.Repeat("b", 64), EgressRevision: 7, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	app := sched.AppSpec{AppID: b.AppID, DeploymentID: b.DeploymentID, AccountID: b.AccountID, Plan: api.PlanPro, BaseKey: "base", LayerKey: "layer", MemSizeMiB: 128, VCPUCount: 2, EgressAllowlist: []string{"8.8.8.0/24"}, EgressPorts: []int{5432}}
	var snap *sched.SnapshotRef
	if paused {
		snap = &sched.SnapshotRef{DeploymentID: b.DeploymentID, StorageKey: "memory", VMStateStorageKey: "state", FCVersion: "1.10.0"}
	}
	req, err := sched.PrepareAdmittedRuntime(t.Context(), b, app, snap, paused)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = runtimeadmission.BindingFromProto(req.Binding)
	return req, b, app
}

func TestVMMClientAdmittedRuntimeWireAndNativeReceipt(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "paused"}[paused], func(t *testing.T) {
			req, b, _ := preparedWireFixture(t, paused)
			server := &admittedWireServer{identity: runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: b.NodeID, Incarnation: b.Incarnation}}
			client := newPolicyWireClient(t, server)
			identity, err := client.RuntimeAdmissionIdentity(t.Context())
			if err != nil || identity != server.identity {
				t.Fatal("identity probe lost native authority")
			}
			out, err := client.CreateAdmittedRuntime(t.Context(), req)
			if err != nil || out == nil || out.RuntimeAdmissionReceipt == nil || out.RuntimeAdmissionReceipt.Binding != b || server.requests != 1 || len(server.destroyed) != 0 {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestVMMClientAdmittedRuntimeRejectsMismatchedReceiptAndCleansUp(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*vmmdpb.CreateAdmittedRuntimeResponse)
	}{
		{"missing receipt", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Receipt = nil }},
		{"missing runtime", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Runtime = nil }},
		{"token", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Receipt.Binding.Token = uuid.NewString() }},
		{"revision", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Receipt.Binding.EgressRevision++ }},
		{"wrong node", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Receipt.Binding.NodeId = uuid.NewString() }},
		{"wrong incarnation", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Receipt.Binding.Incarnation = uuid.NewString() }},
		{"swapped runtime", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Runtime.Instance = uuid.NewString() }},
		{"runtime tuple", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Runtime.LeaseUid++ }},
		{"native hash missing", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Receipt.NativeInputHash = "" }},
		{"paused", func(r *vmmdpb.CreateAdmittedRuntimeResponse) { r.Receipt.Paused = true }},
		{"unknown receipt", func(r *vmmdpb.CreateAdmittedRuntimeResponse) {
			r.Receipt.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			req, b, _ := preparedWireFixture(t, false)
			server := &admittedWireServer{mutate: test.mutate}
			client := newPolicyWireClient(t, server)
			out, err := client.CreateAdmittedRuntime(t.Context(), req)
			if err == nil || out != nil || server.requests != 1 || len(server.destroyed) != 1 || server.destroyed[0] != b.InstanceID {
				t.Fatalf("invalid receipt accepted or leaked: out=%v err=%v destroyed=%v", out, err, server.destroyed)
			}
		})
	}
}

func TestVMMClientAdmittedRuntimeOldNodeAndChangedPreparedPayload(t *testing.T) {
	req, _, _ := preparedWireFixture(t, false)
	client := newPolicyWireClient(t, &vmmdpb.UnimplementedVmmdServer{})
	if _, err := client.RuntimeAdmissionIdentity(t.Context()); err == nil {
		t.Fatal("old node advertised capability")
	}
	if _, err := client.CreateAdmittedRuntime(t.Context(), req); err == nil {
		t.Fatal("old node accepted unbound boot")
	}
	server := &admittedWireServer{}
	client = newPolicyWireClient(t, server)
	copy := proto.Clone(req).(*vmmdpb.CreateAdmittedRuntimeRequest)
	copy.GetColdBoot().App.LayerKey = "changed"
	if _, err := client.CreateAdmittedRuntime(t.Context(), copy); err == nil || server.requests != 0 {
		t.Fatal("changed prepared payload reached the node")
	}
}

func TestPrepareAdmittedRuntimeRejectsLossyPortsAndOwnsInput(t *testing.T) {
	_, b, app := preparedWireFixture(t, false)
	for _, port := range []int{-1, 0, 25, 65536} {
		copy := app
		copy.EgressPorts = []int{port}
		if _, err := sched.PrepareAdmittedRuntime(t.Context(), b, copy, nil, false); err == nil {
			t.Fatalf("port %d silently dropped", port)
		}
	}
	req, err := sched.PrepareAdmittedRuntime(t.Context(), b, app, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	app.EgressAllowlist[0] = "9.9.9.0/24"
	if req.GetColdBoot().App.EgressAllowlist[0] != "8.8.8.0/24" {
		t.Fatal("prepared boot aliases caller-owned input")
	}
}
