package sched_test

// adr: 595 An RPC producer's structurally valid proof must match the selected catalog.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func snapshotConsumedWireFixture(t *testing.T) (*vmmdpb.CreateAdmittedRuntimeRequest, runtimeadmission.SnapshotRestoreEvidence) {
	t.Helper()
	req, b, _ := preparedWireFixture(t, false)
	b.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	sources := []runtimeadmission.ArtifactSource{{Kind: "base-image", StorageKey: "base", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 4096}, {Kind: "app-layer", StorageKey: "layer", Digest: "sha256:" + strings.Repeat("b", 64), Bytes: 8192}}
	b.ArtifactSourcesHash, _ = runtimeadmission.HashArtifactSources(sources)
	parentBinding := b
	parentBinding.InstanceID, parentBinding.Token = uuid.NewString(), uuid.NewString()
	parent := runtimeadmission.Receipt{Binding: parentBinding, NativeInputHash: strings.Repeat("c", 64), Netns: "simulated-source", HostIP: "10.100.0.3", LeaseUID: 20001, CompletedAtUnixNano: time.Now().UnixNano(), ArtifactConsumption: runtimeadmission.ArtifactConsumption{ConfigHash: strings.Repeat("d", 64), ProcessPID: 42, ProcessStart: "101"}}
	for _, s := range sources {
		parent.ArtifactConsumption.Drives = append(parent.ArtifactConsumption.Drives, runtimeadmission.ConsumedDrive{Source: s, DriveID: s.Role(), ReadOnly: s.Role() != "main", RootDevice: s.Role() == "base", ProducerDigest: s.Digest, ProducerBytes: s.Bytes, InjectedDigest: s.Digest, InjectedBytes: s.Bytes})
		req.ArtifactSources = append(req.ArtifactSources, s.ToProto())
	}
	token := uuid.NewString()
	prefix := "snap/" + b.DeploymentID + "/captures/" + token + "/v2/"
	artifact := func(key string, n int64) runtimeadmission.CapturedArtifact {
		return runtimeadmission.CapturedArtifact{StorageKey: key, Digest: "sha256:" + strings.Repeat("e", 64), Bytes: n}
	}
	e := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: token, FCVersion: "1.12.1", Capture: runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: parent, Memory: artifact(prefix+"mem", 128<<20), VMState: artifact(prefix+"vmstate", 4096), PrivateDrive: artifact(prefix+"drive", 8192), CapturedAtUnixNano: time.Now().UnixNano()}}
	b.SnapshotCaptureToken = token
	var err error
	b.SnapshotEvidenceHash, err = e.Hash()
	if err != nil {
		t.Fatal(err)
	}
	cold := req.GetColdBoot()
	req.Boot = &vmmdpb.CreateAdmittedRuntimeRequest_Restore{Restore: &vmmdpb.CreateFromSnapshotRequest{Instance: b.InstanceID, AccountId: b.AccountID, Plan: cold.Plan, App: cold.App, Snapshot: &vmmdpb.SnapshotRef{DeploymentId: b.DeploymentID, StorageKey: e.Capture.Memory.StorageKey, VmstateStorageKey: e.Capture.VMState.StorageKey, FcVersion: e.FCVersion}}}
	req.SnapshotRestore = e.ToProto()
	b.PayloadHash, err = runtimeadmission.HashBootPayload(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Binding = b.ToProto()
	return req, e
}

func TestVMMClientSnapshotConsumptionComparesCatalogAndCleansUp(t *testing.T) {
	for _, variant := range []string{"valid", "substituted memory", "substituted state", "substituted private drive", "missing proof", "cold fallback"} {
		t.Run(variant, func(t *testing.T) {
			req, e := snapshotConsumedWireFixture(t)
			server := &admittedWireServer{}
			server.mutate = func(resp *vmmdpb.CreateAdmittedRuntimeResponse) {
				r, _ := runtimeadmission.ReceiptFromProto(resp.Receipt)
				r.ArtifactConsumption = e.Capture.Parent.ArtifactConsumption.Clone()
				r.ArtifactConsumption.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(false)
				r.ArtifactConsumption.ProcessPID, r.ArtifactConsumption.ProcessStart = 4242, "202"
				r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: e.CaptureToken, EvidenceHash: r.Binding.SnapshotEvidenceHash, Memory: e.Capture.Memory, VMState: e.Capture.VMState, PrivateDrive: e.Capture.PrivateDrive, MappedMemoryBytes: e.Capture.Memory.Bytes}
				digest := "sha256:" + strings.Repeat("0", 64)
				switch variant {
				case "substituted memory":
					r.SnapshotConsumption.Memory.Digest = digest
				case "substituted state":
					r.SnapshotConsumption.VMState.Digest = digest
				case "substituted private drive":
					r.SnapshotConsumption.PrivateDrive.Digest = digest
				case "missing proof":
					r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{}
				case "cold fallback":
					r.Method, resp.Runtime.Method = vmmdpb.WakeMethod_WAKE_COLD_BOOT, vmmdpb.WakeMethod_WAKE_COLD_BOOT
					r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{}
				}
				resp.Receipt = r.ToProto()
			}
			client := newPolicyWireClient(t, server)
			out, err := client.CreateAdmittedRuntime(t.Context(), req)
			valid := variant == "valid" || variant == "cold fallback"
			if valid {
				if err != nil || out == nil || out.RuntimeAdmissionReceipt == nil || len(server.destroyed) != 0 {
					t.Fatal("valid snapshot outcome refused", err)
				}
				return
			}
			if err == nil || out != nil || len(server.destroyed) != 1 || server.destroyed[0] != req.Binding.InstanceId {
				t.Fatal("forged catalog consumption accepted or leaked", err)
			}
		})
	}
}
