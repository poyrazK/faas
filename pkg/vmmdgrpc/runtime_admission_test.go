package vmmdgrpc_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type admittedNativeVMM struct {
	*fakeVMM
	identity   runtimeadmission.Identity
	requests   []fcvm.AdmittedWakeRequest
	promotions []runtimeadmission.Promotion
	destroyed  []string
	mutate     func(*fcvm.Instance, *runtimeadmission.Receipt)
}

func (v *admittedNativeVMM) RuntimeAdmissionIdentity() (runtimeadmission.Identity, error) {
	return v.identity, nil
}
func (v *admittedNativeVMM) Destroy(_ context.Context, id string) error {
	v.destroyed = append(v.destroyed, id)
	return nil
}
func (v *admittedNativeVMM) WakeAdmitted(_ context.Context, req fcvm.AdmittedWakeRequest, _ fcvm.WakeNetworkReadyHook) (*fcvm.Instance, runtimeadmission.Receipt, error) {
	v.requests = append(v.requests, req)
	lease := fcvm.Lease{Instance: req.Request.Instance, UID: 20000, HostIP: netip.MustParseAddr("10.100.0.2")}
	inst := &fcvm.Instance{Lease: lease, Net: netns.NewConfig(req.Request.Instance, "fc-runtime", "vh1", "vp1", lease.HostIP), Method: fcvm.WakeColdBoot, Paused: req.Request.KeepPaused, AppID: req.Request.AppID, DeploymentID: req.Request.DeploymentID, AccountID: req.Request.AccountID}
	method := vmmdpb.WakeMethod_WAKE_COLD_BOOT
	if req.Request.Snapshot != nil {
		inst.Method = fcvm.WakeRestore
		method = vmmdpb.WakeMethod_WAKE_RESTORE
	}
	receipt := runtimeadmission.Receipt{Binding: req.Binding, NativeInputHash: req.NativeInputHash, Netns: inst.Net.Netns, HostIP: lease.HostIP.String(), LeaseUID: int32(lease.UID), Method: method, Paused: inst.Paused, CompletedAtUnixNano: time.Now().UnixNano()}
	if v.mutate != nil {
		v.mutate(inst, &receipt)
	}
	return inst, receipt, nil
}

func admittedRPCFixture(t *testing.T) (*vmmdgrpc.Server, *admittedNativeVMM, *vmmdpb.CreateAdmittedRuntimeRequest) {
	t.Helper()
	now := time.Now()
	v := &admittedNativeVMM{fakeVMM: &fakeVMM{}, identity: runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: uuid.NewString(), Incarnation: uuid.NewString()}}
	b := runtimeadmission.Binding{ProtocolVersion: runtimeadmission.ProtocolVersion, Token: uuid.NewString(), InstanceID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), AccountID: uuid.NewString(), NodeID: v.identity.NodeID, Incarnation: v.identity.Incarnation, DesiredRevision: 9, EffectiveHash: strings.Repeat("a", 64), CapturedInputHash: strings.Repeat("b", 64), EgressRevision: 7, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	req := &vmmdpb.CreateAdmittedRuntimeRequest{Boot: &vmmdpb.CreateAdmittedRuntimeRequest_ColdBoot{ColdBoot: &vmmdpb.CreateColdBootRequest{Instance: b.InstanceID, AccountId: b.AccountID, Plan: "pro", App: &vmmdpb.AppSpec{AppId: b.AppID, BaseKey: "base", LayerKey: "layer", MemSizeMib: 128, VcpuCount: 2, EgressPorts: []uint32{5432}, EgressAllowlist: []string{"8.8.8.0/24"}, SealedEnv: []*vmmdpb.SealedSecret{{Key: "key", Ciphertext: []byte("sealed")}}}}}}
	b.PayloadHash, _ = runtimeadmission.HashBootPayload(req)
	req.Binding = b.ToProto()
	s := vmmdgrpc.New(v, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithNodeID(v.identity.NodeID)
	return s, v, req
}

func rehashAdmittedRequest(t *testing.T, req *vmmdpb.CreateAdmittedRuntimeRequest) {
	t.Helper()
	hash, err := runtimeadmission.HashBootPayload(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Binding.PayloadHash = hash
}

func TestCreateAdmittedRuntimeUsesNativeReceipt(t *testing.T) {
	for _, variant := range []string{"cold", "restore", "paused"} {
		t.Run(variant, func(t *testing.T) {
			s, v, req := admittedRPCFixture(t)
			if variant != "cold" {
				cold := req.GetColdBoot()
				req.Boot = &vmmdpb.CreateAdmittedRuntimeRequest_Restore{Restore: &vmmdpb.CreateFromSnapshotRequest{Instance: cold.Instance, App: cold.App, AccountId: cold.AccountId, Plan: cold.Plan, KeepPaused: variant == "paused", Snapshot: &vmmdpb.SnapshotRef{DeploymentId: req.Binding.DeploymentId, StorageKey: "memory", VmstateStorageKey: "state", FcVersion: "1.10.0"}}}
				rehashAdmittedRequest(t, req)
			}
			identity, err := s.RuntimeAdmissionIdentity(admittedRPCContext(t), &vmmdpb.RuntimeAdmissionIdentityRequest{})
			if err != nil || identity.NodeId != v.identity.NodeID || identity.Incarnation != v.identity.Incarnation {
				t.Fatal("probe did not return the actual native identity")
			}
			resp, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req)
			if err != nil || resp.Receipt == nil || len(v.requests) != 1 {
				t.Fatalf("resp=%v err=%v", resp, err)
			}
			receipt, err := runtimeadmission.ReceiptFromProto(resp.Receipt)
			binding, _ := runtimeadmission.BindingFromProto(req.Binding)
			if err != nil || receipt.Check(binding, time.Now()) != nil || receipt.NativeInputHash != v.requests[0].NativeInputHash || v.requests[0].Request.DeploymentID != binding.DeploymentID || v.requests[0].Request.EgressPorts[0] != 5432 || receipt.Paused != (variant == "paused") {
				t.Fatal("adapter changed the admitted request or receipt")
			}
		})
	}
}

func TestCreateAdmittedRuntimeMalformedNeverReachesBackend(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*vmmdpb.CreateAdmittedRuntimeRequest)
		rehash bool
		code   codes.Code
	}{
		{"wrong hash", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetColdBoot().App.LayerKey = "changed" }, false, codes.InvalidArgument},
		{"node", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.Binding.NodeId = uuid.NewString() }, false, codes.FailedPrecondition},
		{"incarnation", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.Binding.Incarnation = uuid.NewString() }, false, codes.FailedPrecondition},
		{"missing binding", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.Binding = nil }, false, codes.InvalidArgument},
		{"tenant", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetColdBoot().AccountId = uuid.NewString() }, true, codes.InvalidArgument},
		{"app", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetColdBoot().App.AppId = uuid.NewString() }, true, codes.InvalidArgument},
		{"expired", func(r *vmmdpb.CreateAdmittedRuntimeRequest) {
			r.Binding.IssuedAtUnixNano = time.Now().Add(-time.Minute).UnixNano()
			r.Binding.ExpiresAtUnixNano = time.Now().Add(-time.Second).UnixNano()
		}, false, codes.FailedPrecondition},
		{"port overflow", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetColdBoot().App.EgressPorts = []uint32{70000} }, true, codes.InvalidArgument},
		{"forbidden port", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetColdBoot().App.EgressPorts = []uint32{25} }, true, codes.InvalidArgument},
		{"builder", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetColdBoot().Build = &vmmdpb.BuildSpec{} }, true, codes.InvalidArgument},
		{"incomplete source set", func(r *vmmdpb.CreateAdmittedRuntimeRequest) {
			r.ArtifactSources = []*vmmdpb.RuntimeArtifactSource{{Kind: "app-layer", StorageKey: "layer", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 10}}
		}, true, codes.InvalidArgument},
		{"nil source", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.ArtifactSources = []*vmmdpb.RuntimeArtifactSource{nil} }, false, codes.InvalidArgument},
		{"unknown nested control", func(r *vmmdpb.CreateAdmittedRuntimeRequest) {
			r.GetColdBoot().App.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}, false, codes.InvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, v, req := admittedRPCFixture(t)
			test.mutate(req)
			if test.rehash {
				rehashAdmittedRequest(t, req)
			}
			resp, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req)
			if status.Code(err) != test.code || resp != nil || len(v.requests) != 0 {
				t.Fatalf("resp=%v err=%v nativeCalls=%d", resp, err, len(v.requests))
			}
		})
	}
}

func TestCreateAdmittedRuntimeDeliversExactOwnedSourceInputs(t *testing.T) {
	s, v, req := admittedRPCFixture(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	req.ArtifactSources = []*vmmdpb.RuntimeArtifactSource{{Kind: "base-image", StorageKey: "base", Digest: digest, Bytes: 10}, {Kind: "app-layer", StorageKey: "layer", Digest: digest, Bytes: 20}}
	rehashAdmittedRequest(t, req)
	resp, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req)
	if err != nil || resp == nil || len(v.requests) != 1 {
		t.Fatalf("native transport err=%v", err)
	}
	native := v.requests[0]
	if len(native.Request.ArtifactSources) != 2 || native.Request.ArtifactSources[1].Bytes != 20 {
		t.Fatal("native projection lost approved bytes")
	}
	hash, err := fcvm.NativeWakeInputHash(native.Request)
	if err != nil || hash != native.NativeInputHash {
		t.Fatal("native digest omitted source inputs")
	}
	req.ArtifactSources[1].Bytes++
	if native.Request.ArtifactSources[1].Bytes != 20 {
		t.Fatal("native source set aliases the wire request")
	}
	if _, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req); status.Code(err) != codes.InvalidArgument || len(v.requests) != 1 {
		t.Fatal("changed source retained its boot grant")
	}
}

func TestCreateAdmittedRuntimeInvalidNativeReceiptDestroysInstance(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fcvm.Instance, *runtimeadmission.Receipt)
	}{
		{"absent", func(_ *fcvm.Instance, r *runtimeadmission.Receipt) { *r = runtimeadmission.Receipt{} }},
		{"grant", func(_ *fcvm.Instance, r *runtimeadmission.Receipt) { r.Binding.Token = uuid.NewString() }},
		{"native digest", func(_ *fcvm.Instance, r *runtimeadmission.Receipt) { r.NativeInputHash = strings.Repeat("f", 64) }},
		{"runtime tuple", func(_ *fcvm.Instance, r *runtimeadmission.Receipt) { r.LeaseUID++ }},
		{"swapped instance", func(i *fcvm.Instance, _ *runtimeadmission.Receipt) { i.Lease.Instance = uuid.NewString() }},
		{"paused", func(_ *fcvm.Instance, r *runtimeadmission.Receipt) { r.Paused = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, v, req := admittedRPCFixture(t)
			v.mutate = test.mutate
			resp, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req)
			if err == nil || resp != nil || len(v.destroyed) != 1 || v.destroyed[0] != req.Binding.InstanceId {
				t.Fatalf("invalid receipt escaped or leaked: err=%v destroy=%v", err, v.destroyed)
			}
		})
	}
}

func TestCreateAdmittedRuntimeOldBackendNeverCallsLegacyWake(t *testing.T) {
	legacyCalls := 0
	v := &fakeVMM{wakeFn: func(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) { legacyCalls++; return nil, nil }}
	s := vmmdgrpc.New(v, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	_, _, req := admittedRPCFixture(t)
	resp, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req)
	if status.Code(err) != codes.Unimplemented || resp != nil || legacyCalls != 0 {
		t.Fatal("old backend fell back to an unbound wake")
	}
	if _, err := s.RuntimeAdmissionIdentity(admittedRPCContext(t), &vmmdpb.RuntimeAdmissionIdentityRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatal("old backend advertised authority")
	}
}

func admittedRPCContext(t *testing.T) context.Context {
	return peer.NewContext(t.Context(), &peer.Peer{Addr: &net.UnixAddr{Net: "unix", Name: "vmmd.sock"}})
}

func TestCreateAdmittedRuntimeRequiresSchedulerPeer(t *testing.T) {
	for _, test := range []struct {
		name string
		peer *peer.Peer
		code codes.Code
	}{
		{"missing", nil, codes.Unauthenticated},
		{"insecure remote", &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}}, codes.Unauthenticated},
		{"unverified TLS", &peer.Peer{AuthInfo: credentials.TLSInfo{}}, codes.Unauthenticated},
		{"other daemon", &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{Subject: pkix.Name{CommonName: "imaged.faas"}}}}}}}, codes.PermissionDenied},
		{"scheduler", &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{Subject: pkix.Name{CommonName: "schedd.faas"}}}}}}}, codes.OK},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, v, req := admittedRPCFixture(t)
			ctx := t.Context()
			if test.peer != nil {
				ctx = peer.NewContext(ctx, test.peer)
			}
			resp, err := s.CreateAdmittedRuntime(ctx, req)
			if status.Code(err) != test.code {
				t.Fatalf("peer code=%v err=%v", test.code, err)
			}
			if test.code != codes.OK && (resp != nil || len(v.requests) != 0) {
				t.Fatal("unauthorized peer reached native boot")
			}
			if _, err := s.RuntimeAdmissionIdentity(ctx, &vmmdpb.RuntimeAdmissionIdentityRequest{}); status.Code(err) != test.code {
				t.Fatal("probe did not apply scheduler identity boundary")
			}
		})
	}
}
