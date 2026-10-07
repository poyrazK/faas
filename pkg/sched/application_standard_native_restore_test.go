package sched

// adr: 595 Real scheduler/catalog/publication; simulated scanner/provider/VM facts.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func exerciseStandardServingRestore(t *testing.T, s composedWaveStore, variant string) {
	t.Helper()
	f := newComposedWaveFixture(t, s)
	n := &composedSnapshotNotifier{fakeNotifier: &fakeNotifier{}}
	f.engine.notif = n
	f.onboard(t)
	var id string
	for appID := range f.apps {
		id = appID
		break
	}
	parent := f.assertRuntime(t, id, 1)
	event := f.parkComposedSnapshot(t, n, parent)
	var notice struct {
		Token     string `json:"application_standard_capture_token"`
		MemoryKey string `json:"storage_key"`
	}
	if err := json.Unmarshal([]byte(event.Payload), &notice); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetApplicationStandardSnapshotCapture(t.Context(), canonicalStandardNativeUUID(f.apps[id].AccountID), id, canonicalStandardNativeUUID(f.deps[id].ID), notice.Token)
	if err != nil || r.Acknowledgment == nil {
		t.Fatal("park lost its catalog", err)
	}
	c := r.Acknowledgment.Capture
	snapshot, err := s.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: f.deps[id].ID, ApplicationStandardCaptureToken: notice.Token,
		StorageKey: notice.MemoryKey, FCVersion: r.Grant.FCVersion, MemBytes: c.Memory.Bytes, DiskBytes: c.VMState.Bytes,
		StoredBytes: 4096, Tier: state.SnapshotTierInit, BaseImageVersion: "portable-simulated"})
	if err != nil {
		t.Fatal("publish simulated cache", err)
	}
	if variant != "unsupported cold fallback" {
		f.vmm.identity.SnapshotRestoreVersion = runtimeadmission.SnapshotRestoreVersion
		if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), f.vmm.identity); err != nil {
			t.Fatal(err)
		}
	}
	if variant == "load cold fallback" {
		f.vmm.snapshotColdFallback = true
	}
	if variant == "substituted proof" {
		f.vmm.editSnapshotReceipt = func(r *runtimeadmission.Receipt) {
			r.SnapshotConsumption.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
		}
	}
	if _, err := f.engine.Wake(t.Context(), f.apps[id].ID, f.deps[id].ID, "", TriggerGateway); err != nil {
		if variant != "substituted proof" {
			t.Fatal("serving restore wake", err)
		}
		rows, listErr := s.ListInstancesForApp(t.Context(), f.apps[id].ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, row := range rows {
			if row.State == string(state.StateRunning) {
				t.Fatal("substituted snapshot proof published a runtime")
			}
		}
		return
	}
	if variant == "substituted proof" {
		t.Fatal("substituted snapshot proof accepted")
	}
	actual := f.assertRuntime(t, id, 1)
	req := f.vmm.lastRequest
	if req == nil || req.GetRestore() == nil || req.GetRestore().Snapshot.StorageKey != snapshot.StorageKey {
		t.Fatal("selected cache did not reach the ordinary wake path")
	}
	receipt, err := s.(state.InstanceApplicationStandardRuntimeReceiptStore).GetInstanceApplicationStandardRuntimeReceipt(t.Context(), actual.ID)
	if err != nil {
		t.Fatal("durable serving receipt", err)
	}
	if variant == "unsupported cold fallback" {
		if req.SnapshotRestore != nil || receipt.Binding.SnapshotCaptureToken != "" || receipt.Method != vmmdpb.WakeMethod_WAKE_COLD_BOOT || !receipt.SnapshotConsumption.IsZero() {
			t.Fatal("unsupported node received restore authority")
		}
		return
	}
	b, err := runtimeadmission.BindingFromProto(req.Binding)
	if err != nil || b.SnapshotCaptureToken != notice.Token || b.InstanceID == parent.ID || b.Token == r.Grant.Parent.Binding.Token || req.GetRestore().Snapshot.VmstateStorageKey != c.VMState.StorageKey {
		t.Fatal("fresh catalog authority or canonical state locator missing", err)
	}
	hash, err := runtimeadmission.HashBootPayload(req)
	if err != nil || hash != b.PayloadHash || runtimeadmission.CheckSnapshotRestorePayload(req, b, time.Now()) != nil {
		t.Fatal("restore envelope omitted from complete boot binding", err)
	}
	if variant == "load cold fallback" {
		if receipt.Method != vmmdpb.WakeMethod_WAKE_COLD_BOOT || !receipt.SnapshotConsumption.IsZero() {
			t.Fatal("cold fallback manufactured consumed memory")
		}
		if _, err := s.LatestSnapshot(t.Context(), snapshot.DeploymentID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("failed cache was not retired after cold publication", err)
		}
		return
	}
	evidence, err := runtimeadmission.SnapshotRestoreEvidenceFromProto(req.SnapshotRestore)
	if err != nil || receipt.Method != vmmdpb.WakeMethod_WAKE_RESTORE || receipt.SnapshotConsumption.CheckEvidence(b, receipt.ArtifactConsumption, false, evidence, time.Now()) != nil {
		t.Fatal("serving receipt lost selected immutable capture", err)
	}
	if receipt.Binding.InstanceID != actual.ID || receipt.Netns != actual.Netns || receipt.HostIP != actual.HostIP {
		t.Fatal("serving publication lost target identity")
	}
	// Follow the same ordinary scheduler into a new park. Its grant must name
	// the serving restored process, rather than the original cold ancestor.
	inputToken := notice.Token
	event = f.parkComposedSnapshot(t, n, actual)
	if err := json.Unmarshal([]byte(event.Payload), &notice); err != nil {
		t.Fatal(err)
	}
	next, err := s.GetApplicationStandardSnapshotCapture(t.Context(), b.AccountID, b.AppID, b.DeploymentID, notice.Token)
	if err != nil || next.Acknowledgment == nil || !next.Grant.Parent.Equal(receipt) || !next.Acknowledgment.Capture.Parent.Equal(receipt) || notice.Token == inputToken {
		t.Fatal("ordinary restored park lost its actual serving parent or fresh namespace", err)
	}
	// Simulate the cache owner's publication, then consume this new catalog
	// through another ordinary wake. SQL/protobuf hashes include the complete
	// restored-parent proof rather than silently returning to cold ancestry.
	if count, err := s.DeleteSnapshotsByID(t.Context(), []string{snapshot.ID}); err != nil || count != 1 {
		t.Fatal("retire previous simulated cache slot", count, err)
	}
	nextCapture := next.Acknowledgment.Capture
	if _, err := s.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: f.deps[id].ID, ApplicationStandardCaptureToken: notice.Token,
		StorageKey: notice.MemoryKey, FCVersion: next.Grant.FCVersion, MemBytes: nextCapture.Memory.Bytes, DiskBytes: nextCapture.VMState.Bytes,
		StoredBytes: 4096, Tier: state.SnapshotTierInit, BaseImageVersion: "portable-simulated"}); err != nil {
		t.Fatal("publish new simulated cache", err)
	}
	if _, err := f.engine.Wake(t.Context(), f.apps[id].ID, f.deps[id].ID, "", TriggerGateway); err != nil {
		t.Fatal("wake from serving-parent capture", err)
	}
	final := f.assertRuntime(t, id, 1)
	finalReceipt, err := s.(state.InstanceApplicationStandardRuntimeReceiptStore).GetInstanceApplicationStandardRuntimeReceipt(t.Context(), final.ID)
	if err != nil || finalReceipt.Method != vmmdpb.WakeMethod_WAKE_RESTORE || finalReceipt.Binding.SnapshotCaptureToken != notice.Token || final.ID == actual.ID {
		t.Fatal("second serving restore lost its new catalog or target identity", err)
	}
	finalEvidence, err := runtimeadmission.SnapshotRestoreEvidenceFromProto(f.vmm.lastRequest.SnapshotRestore)
	if err != nil || !finalEvidence.Capture.Parent.Equal(receipt) || finalReceipt.SnapshotConsumption.CheckEvidence(finalReceipt.Binding, finalReceipt.ArtifactConsumption, false, finalEvidence, time.Now()) != nil {
		t.Fatal("second restore omitted its actual serving-parent proof", err)
	}
}

func TestMemApplicationStandardServingRestore(t *testing.T) {
	for _, variant := range []string{"serving restore", "unsupported cold fallback", "load cold fallback", "substituted proof"} {
		t.Run(variant, func(t *testing.T) { exerciseStandardServingRestore(t, state.NewMemStore(), variant) })
	}
}
