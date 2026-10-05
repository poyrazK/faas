package vmmdgrpc_test

// Portable RPC tests use explicit simulations of native process/load facts.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func rpcSnapshotRestoreEnvelope(t *testing.T, req *vmmdpb.CreateAdmittedRuntimeRequest) {
	t.Helper()
	b, _ := runtimeadmission.BindingFromProto(req.Binding)
	b.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	sources := []runtimeadmission.ArtifactSource{{Kind: "base-image", StorageKey: "base", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 4096}, {Kind: "app-layer", StorageKey: "layer", Digest: "sha256:" + strings.Repeat("b", 64), Bytes: 8192}}
	b.ArtifactSourcesHash, _ = runtimeadmission.HashArtifactSources(sources)
	parent := b
	parent.Token, parent.InstanceID = uuid.NewString(), uuid.NewString()
	r := runtimeadmission.Receipt{Binding: parent, NativeInputHash: strings.Repeat("c", 64), Netns: "simulated-parent", HostIP: "10.100.0.3", LeaseUID: 20001, CompletedAtUnixNano: time.Now().UnixNano(), ArtifactConsumption: runtimeadmission.ArtifactConsumption{ConfigHash: strings.Repeat("d", 64), ProcessPID: 42, ProcessStart: "101"}}
	for _, s := range sources {
		r.ArtifactConsumption.Drives = append(r.ArtifactConsumption.Drives, runtimeadmission.ConsumedDrive{Source: s, DriveID: s.Role(), ReadOnly: s.Role() != "main", RootDevice: s.Role() == "base", ProducerDigest: s.Digest, ProducerBytes: s.Bytes, InjectedDigest: s.Digest, InjectedBytes: s.Bytes})
		req.ArtifactSources = append(req.ArtifactSources, s.ToProto())
	}
	token := uuid.NewString()
	prefix := "snap/" + b.DeploymentID + "/captures/" + token + "/v2/"
	artifact := func(key string, n int64) runtimeadmission.CapturedArtifact {
		return runtimeadmission.CapturedArtifact{StorageKey: key, Digest: "sha256:" + strings.Repeat("e", 64), Bytes: n}
	}
	e := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: token, FCVersion: "1.12.1", Capture: runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: r, Memory: artifact(prefix+"mem", 128<<20), VMState: artifact(prefix+"vmstate", 4096), PrivateDrive: artifact(prefix+"drive", 8192), CapturedAtUnixNano: time.Now().UnixNano()}}
	b.SnapshotCaptureToken = token
	var err error
	b.SnapshotEvidenceHash, err = e.Hash()
	if err != nil {
		t.Fatal(err)
	}
	cold := req.GetColdBoot()
	req.Boot = &vmmdpb.CreateAdmittedRuntimeRequest_Restore{Restore: &vmmdpb.CreateFromSnapshotRequest{Instance: b.InstanceID, AccountId: b.AccountID, Plan: cold.Plan, App: cold.App, Snapshot: &vmmdpb.SnapshotRef{DeploymentId: b.DeploymentID, FcVersion: e.FCVersion, StorageKey: e.Capture.Memory.StorageKey, VmstateStorageKey: e.Capture.VMState.StorageKey}}}
	req.SnapshotRestore = e.ToProto()
	req.Binding = b.ToProto()
	rehashAdmittedRequest(t, req)
	b, _ = runtimeadmission.BindingFromProto(req.Binding)
	if err := runtimeadmission.CheckSnapshotRestorePayload(req, b, time.Now()); err != nil {
		t.Fatal("invalid refusal fixture", err)
	}
}

func simulatedRPCSnapshotConsumption(v *admittedNativeVMM, inst *fcvm.Instance, r *runtimeadmission.Receipt) {
	e := v.requests[len(v.requests)-1].Request.SnapshotRestore
	r.ArtifactConsumption = e.Capture.Parent.ArtifactConsumption.Clone()
	r.ArtifactConsumption.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(false)
	r.ArtifactConsumption.ProcessPID, r.ArtifactConsumption.ProcessStart = 4242, "202"
	r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion,
		CaptureToken: e.CaptureToken, EvidenceHash: r.Binding.SnapshotEvidenceHash, Memory: e.Capture.Memory, VMState: e.Capture.VMState,
		PrivateDrive: e.Capture.PrivateDrive, MappedMemoryBytes: e.Capture.Memory.Bytes}
}

func TestSnapshotRestoreRPCForwardsOwnedEnvelopeAndValidatesConsumption(t *testing.T) {
	for _, variant := range []string{"restore", "cold fallback", "substituted blob", "missing proof", "partial map", "wrong command"} {
		t.Run(variant, func(t *testing.T) {
			s, v, req := admittedRPCFixture(t)
			rpcSnapshotRestoreEnvelope(t, req)
			v.identity.ProtocolVersion, v.identity.SnapshotRestoreVersion = runtimeadmission.ArtifactProtocolVersion, runtimeadmission.SnapshotRestoreVersion
			v.mutate = func(inst *fcvm.Instance, r *runtimeadmission.Receipt) {
				simulatedRPCSnapshotConsumption(v, inst, r)
				switch variant {
				case "cold fallback":
					inst.Method, r.Method = fcvm.WakeColdBoot, vmmdpb.WakeMethod_WAKE_COLD_BOOT
					r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{}
				case "substituted blob":
					r.SnapshotConsumption.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
				case "missing proof":
					r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{}
				case "partial map":
					r.SnapshotConsumption.MappedMemoryBytes--
				case "wrong command":
					r.ArtifactConsumption.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(true)
				}
			}
			resp, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req)
			valid := variant == "restore" || variant == "cold fallback"
			if !valid {
				if err == nil || resp != nil || len(v.destroyed) != 1 {
					t.Fatal("invalid consumed snapshot survived RPC validation", err)
				}
				return
			}
			if err != nil || len(v.requests) != 1 || len(v.destroyed) != 0 {
				t.Fatal("valid snapshot outcome refused", err)
			}
			r, err := runtimeadmission.ReceiptFromProto(resp.Receipt)
			if err != nil || r.SnapshotConsumption.IsZero() != (variant == "cold fallback") {
				t.Fatal("RPC lost consumption", err)
			}
			native := v.requests[0]
			hash, err := fcvm.NativeWakeInputHash(native.Request)
			if err != nil || hash != native.NativeInputHash || native.Request.Snapshot.MemBytes != 128<<20 {
				t.Fatal("native payload omitted catalog facts", err)
			}
			req.SnapshotRestore.Capture.Memory.Digest = "caller-change"
			if native.Request.SnapshotRestore.Capture.Memory.Digest == "caller-change" {
				t.Fatal("RPC aliased the caller's catalog")
			}
		})
	}
}

func TestSnapshotRestoreRPCGeneratedClientRetainsServingProof(t *testing.T) {
	s, v, req := admittedRPCFixture(t)
	rpcSnapshotRestoreEnvelope(t, req)
	v.identity.ProtocolVersion, v.identity.SnapshotRestoreVersion = runtimeadmission.ArtifactProtocolVersion, runtimeadmission.SnapshotRestoreVersion
	v.mutate = func(inst *fcvm.Instance, r *runtimeadmission.Receipt) { simulatedRPCSnapshotConsumption(v, inst, r) }
	root, err := os.MkdirTemp("", "grg-restore-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "rpc.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, s)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(func() { server.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := sched.NewVMMClient(conn)
	identity, err := client.RuntimeAdmissionIdentity(t.Context())
	if err != nil || identity.SnapshotRestoreVersion != runtimeadmission.SnapshotRestoreVersion {
		t.Fatal("generated client lost capability", err)
	}
	out, err := client.CreateAdmittedRuntime(t.Context(), req)
	if err != nil || out == nil || out.RuntimeAdmissionReceipt == nil || out.RuntimeAdmissionReceipt.SnapshotConsumption.IsZero() || len(v.requests) != 1 {
		t.Fatal("generated client lost restore proof", err)
	}
	if out.RuntimeAdmissionReceipt.SnapshotConsumption.Memory.StorageKey != req.GetRestore().Snapshot.StorageKey {
		t.Fatal("wrong captured memory crossed RPC")
	}
}

func TestSnapshotRestoreRPCRemainsUnavailableBeforeNativeLoading(t *testing.T) {
	s, v, req := admittedRPCFixture(t)
	rpcSnapshotRestoreEnvelope(t, req)
	v.identity.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	i, err := s.RuntimeAdmissionIdentity(admittedRPCContext(t), &vmmdpb.RuntimeAdmissionIdentityRequest{})
	if err != nil || i.SnapshotRestoreVersion != 0 {
		t.Fatal("preparation advertised measured restore", err)
	}
	if _, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req); status.Code(err) != codes.Unimplemented {
		t.Fatal("restore entered unverified backend", err)
	}
	if len(v.requests) != 0 || len(v.destroyed) != 0 {
		t.Fatal("refusal allocated or cleaned up native state")
	}
	req.SnapshotRestore.Capture.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
	rehashAdmittedRequest(t, req)
	if _, err := s.CreateAdmittedRuntime(admittedRPCContext(t), req); status.Code(err) != codes.InvalidArgument {
		t.Fatal("changed evidence bypassed binding", err)
	}
	if len(v.requests) != 0 {
		t.Fatal("changed evidence invoked native backend")
	}
}
