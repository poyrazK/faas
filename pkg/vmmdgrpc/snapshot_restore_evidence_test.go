package vmmdgrpc_test

// Portable RPC refusal tests; the native backend is never invoked for restore.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"google.golang.org/grpc/codes"
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
