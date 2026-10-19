package sched

// adr: 595 Real scheduler and durable store; native VM facts are simulated.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func (v *composedWaveNativeVMM) PromoteAdmittedRuntime(ctx context.Context, node string, req *vmmdpb.PromoteAdmittedRuntimeRequest) (runtimeadmission.Receipt, error) {
	p, err := runtimeadmission.PromotionFromProto(req)
	if err != nil {
		return runtimeadmission.Receipt{}, err
	}
	if p.Validate(time.Now()) != nil || node != v.identity.NodeID || p.Binding.Incarnation != v.identity.Incarnation || v.identity.SnapshotRestoreVersion != runtimeadmission.SnapshotRestoreVersion || !v.receipts[p.Binding.InstanceID].Equal(p.Parent) {
		return runtimeadmission.Receipt{}, runtimeadmission.ErrStale
	}
	// Verify that ordinary scheduler admission saved the exact grant first.
	saved, err := v.store.(state.InstanceApplicationStandardPromotionStore).IssueInstanceApplicationStandardPromotion(ctx, p)
	if err != nil || !saved.Equal(p) {
		return runtimeadmission.Receipt{}, runtimeadmission.ErrStale
	}
	hash, err := runtimeadmission.HashSnapshotResumeParent(p.Parent)
	if err != nil {
		return runtimeadmission.Receipt{}, err
	}
	v.measuredPromotions++
	clock := time.Now().UnixNano()
	r := p.Parent.Clone()
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, clock
	r.SnapshotResumeEvidence = runtimeadmission.SnapshotResumeEvidence{Version: runtimeadmission.SnapshotResumeEvidenceVersion, Binding: p.Binding,
		ParentBinding: p.Parent.Binding, ParentCompletedAtUnixNano: p.Parent.CompletedAtUnixNano, ParentReceiptHash: hash,
		ResumeCommandHash: runtimeadmission.SnapshotResumeCommandHash(), ResumeHookPayloadHash: strings.Repeat("a", 64),
		CommandCompletedAtUnixNano: clock, HostTimeUnixNano: clock, HookCompletedAtUnixNano: clock, CompletedAtUnixNano: clock}
	if v.editPromotionReceipt != nil {
		v.editPromotionReceipt(&r)
	}
	v.receipts[p.Binding.InstanceID] = r.Clone()
	return r, nil
}

func TestApplicationStandardOrdinaryWarmMeasuredPromotion(t *testing.T) {
	exerciseApplicationStandardOrdinaryWarmMeasuredPromotion(t, state.NewMemStore())
}

func exerciseApplicationStandardOrdinaryWarmMeasuredPromotion(t *testing.T, s composedWaveStore) {
	t.Helper()
	promotions, ok := s.(state.InstanceApplicationStandardPromotionStore)
	if !ok {
		t.Fatal("store lacks durable promotion")
	}
	f := newComposedWaveFixture(t, s)
	var reconcileLog bytes.Buffer
	f.engine.log = slog.New(slog.NewTextHandler(&reconcileLog, nil))
	n := &composedSnapshotNotifier{fakeNotifier: &fakeNotifier{}}
	f.engine.notif = n
	f.onboard(t)
	var id string
	for appID := range f.apps {
		id = appID
		break
	}
	source := f.assertRuntime(t, id, 1)
	event := f.parkComposedSnapshot(t, n, source)
	var notice struct {
		Token     string `json:"application_standard_capture_token"`
		MemoryKey string `json:"storage_key"`
	}
	if err := json.Unmarshal([]byte(event.Payload), &notice); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetApplicationStandardSnapshotCapture(t.Context(), canonicalStandardNativeUUID(f.owner.Account.ID), id, canonicalStandardNativeUUID(f.deps[id].ID), notice.Token)
	if err != nil || old.Acknowledgment == nil {
		t.Fatal("park catalog missing", err)
	}
	c := old.Acknowledgment.Capture
	if _, err := s.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{DeploymentID: f.deps[id].ID, ApplicationStandardCaptureToken: notice.Token, StorageKey: notice.MemoryKey, FCVersion: old.Grant.FCVersion, MemBytes: c.Memory.Bytes, DiskBytes: c.VMState.Bytes, StoredBytes: 4096, Tier: state.SnapshotTierInit, BaseImageVersion: "simulated"}, source.ID, source.StartedAt); err != nil {
		t.Fatal(err)
	}
	f.vmm.identity.SnapshotRestoreVersion = runtimeadmission.SnapshotRestoreVersion
	if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), f.vmm.identity); err != nil {
		t.Fatal(err)
	}
	target := 1
	if _, err := s.UpdateApp(t.Context(), f.apps[id].ID, state.UpdateAppParams{SetWarmPoolSize: true, WarmPoolSize: &target}); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.ReconcileWarmPool(t.Context(), f.apps[id].ID); err != nil {
		t.Fatal("ordinary measured warm load", err)
	}
	rows, err := s.ListInstancesForApp(t.Context(), f.apps[id].ID)
	if err != nil {
		t.Fatal(err)
	}
	var warm state.Instance
	for _, row := range rows {
		if row.State == string(state.StateWarm) {
			warm = row
		}
	}
	if warm.ID == "" {
		t.Fatal("no measured warm instance", rows, reconcileLog.String())
	}
	parent, err := promotions.GetInstanceApplicationStandardWarmParent(t.Context(), warm.ID)
	if err != nil || parent.SnapshotConsumption.IsZero() || !parent.Paused || parent.ArtifactConsumption.ConfigHash != runtimeadmission.SnapshotLoadCommandHash(true) {
		t.Fatal("warm history lost actual paused command", err)
	}
	if _, err := f.engine.Wake(t.Context(), f.apps[id].ID, f.deps[id].ID, "", TriggerGateway); err != nil {
		t.Fatal("ordinary measured warm promotion", err)
	}
	actual := f.assertRuntime(t, id, 1)
	r, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), actual.ID)
	if err != nil || actual.ID != warm.ID || f.vmm.measuredPromotions != 1 || r.SnapshotResumeEvidence.IsZero() || !r.ArtifactConsumption.Equal(parent.ArtifactConsumption) {
		t.Fatal("ordinary promotion lost same lease or measured lineage", err, "warm", warm.ID, "actual", actual.ID, "promotions", f.vmm.measuredPromotions, reconcileLog.String())
	}
	event = f.parkComposedSnapshot(t, n, actual)
	if err := json.Unmarshal([]byte(event.Payload), &notice); err != nil {
		t.Fatal(err)
	}
	next, err := s.GetApplicationStandardSnapshotCapture(t.Context(), r.Binding.AccountID, r.Binding.AppID, r.Binding.DeploymentID, notice.Token)
	if err != nil || next.Acknowledgment == nil || !next.Grant.Parent.Equal(r) || !next.Acknowledgment.Capture.Parent.Equal(r) {
		t.Fatal("ordinary park lost measured promoted parent", err)
	}
}
