package conformance

import (
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func testRuntimeInputReceipts(t *testing.T, fx *Fixture) {
	receipts := fx.Store.(state.RuntimeConfigReceiptStore)
	scope := fx.Deployment.Scope
	if scope == "" {
		scope = "default"
	}
	set := func(value string) {
		t.Helper()
		if err := fx.Store.UpsertAppEnvInScope(fx.Ctx, fx.Account.ID, fx.App.ID, scope, "MODE", value); err != nil {
			t.Fatal(err)
		}
	}
	set("old")
	inputs := state.RuntimeConfigInputs{Scope: scope, Boundary: time.Now().UTC(), Variables: map[string]string{"MODE": "old"}, AllSecrets: true}
	// Admission after the new value commits cannot prove which value the
	// boot read. Even a recent claimed boundary cannot replace the payload.
	set("new")
	inputs.Boundary = time.Now().UTC()
	instance, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
		string(state.StateWaking), 256, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := receipts.RecordInstanceRuntimeConfigReceipt(fx.Ctx, instance.ID, instance.WakeID, inputs); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("booting acknowledgement: %v", err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, instance.ID, string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	if err := receipts.RecordInstanceRuntimeConfigReceipt(fx.Ctx, instance.ID, uuid.NewString(), inputs); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong-wake acknowledgement: %v", err)
	}
	if err := receipts.RecordInstanceRuntimeConfigReceipt(fx.Ctx, instance.ID, instance.WakeID, inputs); err != nil {
		t.Fatal(err)
	}
	if fresh, err := receipts.RuntimeConfigInputsFresh(fx.Ctx, fx.App.ID, inputs); err != nil || fresh {
		t.Fatalf("old payload with new admission time was fresh: %v %v", fresh, err)
	}
	snap := state.Snapshot{DeploymentID: fx.Deployment.ID, FCVersion: "1.10.0", Tier: state.SnapshotTierWarm,
		StorageKey: state.SnapshotCaptureMemKey(fx.Deployment.ID, state.SnapshotTierWarm, uuid.NewString())}
	if _, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, snap, instance.ID, instance.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
		t.Fatalf("recent publication accepted old payload: %v", err)
	}
	changed := inputs
	changed.Variables = map[string]string{"MODE": "new"}
	if err := receipts.RecordInstanceRuntimeConfigReceipt(fx.Ctx, instance.ID, instance.WakeID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("same wake rewrote its boot inputs: %v", err)
	}
	if err := receipts.RecordInstanceRuntimeConfigReceipt(fx.Ctx, instance.ID, instance.WakeID, inputs); err != nil {
		t.Fatalf("same-payload replay: %v", err)
	}
	inputs.Variables["MODE"] = "mutated caller"
	stored, exists, err := receipts.InstanceRuntimeConfigReceipt(fx.Ctx, instance.ID)
	if err != nil || !exists || stored.Variables["MODE"] != "old" {
		t.Fatalf("caller mutated persisted receipt: %+v %v %v", stored, exists, err)
	}
	stored.Variables["MODE"] = "mutated getter"
	stored, _, _ = receipts.InstanceRuntimeConfigReceipt(fx.Ctx, instance.ID)
	if stored.Variables["MODE"] != "old" {
		t.Fatal("receipt getter returned shared storage")
	}

	freshInstance, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
		string(state.StateRunning), 256, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := receipts.RecordInstanceRuntimeConfigReceipt(fx.Ctx, freshInstance.ID, freshInstance.WakeID, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, snap, freshInstance.ID, freshInstance.StartedAt.Add(-time.Second)); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
		t.Fatalf("old capture frame matched a new receipt: %v", err)
	}
	snapshot, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, snap, freshInstance.ID, freshInstance.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.DeleteInstance(fx.Ctx, freshInstance.ID); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := receipts.InstanceRuntimeConfigReceipt(fx.Ctx, freshInstance.ID); err != nil || exists {
		t.Fatalf("deleted instance retained a receipt: %v %v", exists, err)
	}
	captured, exists, err := receipts.SnapshotRuntimeConfigReceipt(fx.Ctx, snapshot.ID)
	if err != nil || !exists || !maps.Equal(captured.Variables, changed.Variables) {
		t.Fatalf("snapshot lost its inputs when source was collected: %+v %v %v", captured, exists, err)
	}
	set("latest")
	if fresh, err := receipts.RuntimeConfigInputsFresh(fx.Ctx, fx.App.ID, captured); err != nil || fresh {
		t.Fatalf("snapshot receipt accepted changed inputs: %v %v", fresh, err)
	}
	if err := fx.Store.MarkSnapshotStale(fx.Ctx, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.DeleteSnapshotsByID(fx.Ctx, []string{snapshot.ID}); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := receipts.SnapshotRuntimeConfigReceipt(fx.Ctx, snapshot.ID); err != nil || exists {
		t.Fatalf("deleted snapshot retained receipt: %v %v", exists, err)
	}
}

func testRuntimeInputReceiptSecretVersions(t *testing.T, fx *Fixture) {
	receipts := fx.Store.(state.RuntimeConfigReceiptStore)
	scope := fx.Deployment.Scope
	if scope == "" {
		scope = "default"
	}
	set := func(key, ciphertext string) {
		t.Helper()
		if err := fx.Store.UpsertAppSecretInScope(fx.Ctx, fx.Account.ID, fx.App.ID, scope, key, []byte(ciphertext)); err != nil {
			t.Fatal(err)
		}
	}
	set("TOKEN", "cipher-v1")
	inputs := state.RuntimeConfigInputs{Scope: scope, Boundary: time.Now().UTC(), Variables: map[string]string{},
		SecretVersions: map[string]int64{scope + "/TOKEN": 1}}
	check := func(want bool) {
		t.Helper()
		if got, err := receipts.RuntimeConfigInputsFresh(fx.Ctx, fx.App.ID, inputs); err != nil || got != want {
			t.Fatalf("secret input freshness: %v %v, want %v", got, err, want)
		}
	}
	check(true)
	if err := fx.Store.ResealAppSecretWithKidAndValueHashInScope(fx.Ctx, fx.Account.ID, fx.App.ID, scope,
		"TOKEN", "rekeyed", "1111111111111111", []byte("resealed-cipher")); err != nil {
		t.Fatal(err)
	}
	check(true) // host-key resealing preserves the delivered version
	set("UNUSED", "other-cipher")
	check(true) // an explicit allowlist did not stage the new secret
	inputs.AllSecrets = true
	check(false) // the legacy stage-all path must include every secret
	inputs.AllSecrets = false
	set("TOKEN", "cipher-v2")
	check(false)
}

func testRuntimeInputReceiptPublication(t *testing.T, fx *Fixture) {
	publisher := fx.Store.(state.RuntimeConfigReceiptPublisher)
	receipts := fx.Store.(state.RuntimeConfigReceiptStore)
	scope := fx.Deployment.Scope
	if scope == "" {
		scope = "default"
	}
	instance, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
		string(state.StateColdBooting), 256, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	inputs := state.RuntimeConfigInputs{Scope: scope, Boundary: time.Unix(0, 0).UTC(), Variables: map[string]string{}, AllSecrets: true}
	for _, attempt := range []struct{ wake, scope string }{{uuid.NewString(), scope}, {instance.WakeID, "wrong-scope"}} {
		invalid := inputs
		invalid.Scope = attempt.scope
		if _, err := publisher.PublishInstanceRuntimeWithConfig(fx.Ctx, instance.ID, string(state.StateColdBooting),
			"fc-invalid", "10.99.0.9", 20009, attempt.wake, invalid); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unfenced publication: %v", err)
		}
		current, err := fx.Store.InstanceByID(fx.Ctx, instance.ID)
		if err != nil || current.State != string(state.StateColdBooting) || current.Netns != "" {
			t.Fatalf("failed receipt publication changed readiness: %+v %v", current, err)
		}
		if _, exists, err := receipts.InstanceRuntimeConfigReceipt(fx.Ctx, instance.ID); err != nil || exists {
			t.Fatalf("failed publication left evidence: %v %v", exists, err)
		}
	}
	ready, err := publisher.PublishInstanceRuntimeWithConfig(fx.Ctx, instance.ID, string(state.StateColdBooting),
		"fc-receipt", "10.99.0.8", 20008, instance.WakeID, inputs)
	if err != nil || ready.State != string(state.StateRunning) || ready.HostIP != "10.99.0.8" || ready.WakeID != instance.WakeID {
		t.Fatalf("acknowledged publication: %+v %v", ready, err)
	}
	acknowledged, exists, err := receipts.InstanceRuntimeConfigReceipt(fx.Ctx, instance.ID)
	if err != nil || !exists || !acknowledged.Boundary.Equal(inputs.Boundary) || acknowledged.Scope != scope {
		t.Fatalf("readiness committed without its receipt: %+v %v %v", acknowledged, exists, err)
	}
	if _, err := publisher.PublishInstanceRuntimeWithConfig(fx.Ctx, instance.ID, string(state.StateColdBooting),
		"stale", "10.99.0.9", 20009, instance.WakeID, inputs); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("late publication overwrote readiness: %v", err)
	}
}
