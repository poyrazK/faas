// adr: 20261031193635
package imaged

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// A failed capture attempt's objects are deleted after the grace and its key
// forgotten; the published capture's objects stay while it is ready.
func TestEnvironmentBranchCaptureObjectsAreCollected(t *testing.T) {
	ctx := context.Background()
	fx := newGCFixture(t, 50)
	_, depID, _ := seedSnapshotWithApp(t, fx.store, 100, 100)
	if _, err := fx.store.RequestEnvironmentBranchCapture(ctx, depID); err != nil {
		t.Fatal(err)
	}
	attempt := func() (state.EnvironmentBranchCapture, string, []string) {
		t.Helper()
		lease := uuid.NewString()
		capture, err := fx.store.ClaimEnvironmentBranchCapture(ctx, lease, time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		key := state.SnapshotCaptureMemKey(depID, state.SnapshotTierWarm, uuid.NewString())
		if err := fx.store.RecordEnvironmentBranchCaptureObject(ctx, capture.ID, lease, key); err != nil {
			t.Fatal(err)
		}
		keys := environmentBranchCaptureKeys(state.EnvironmentBranchCaptureObject{StorageKey: key, DeploymentID: depID})
		for _, k := range keys[:3] { // memory, vmstate, drive; the backing identity is optional
			if err := fx.be.Put(ctx, k, bytes.NewReader([]byte("guest"))); err != nil {
				t.Fatal(err)
			}
		}
		return capture, lease, keys
	}
	exists := func(key string) bool {
		t.Helper()
		r, err := fx.be.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Close()
		return true
	}

	failed, lease, failedKeys := attempt()
	if err := fx.store.FailEnvironmentBranchCapture(ctx, failed.ID, lease, "snapshot failed", false); err != nil {
		t.Fatal(err)
	}
	ready, lease, readyKeys := attempt()
	if err := fx.store.PublishEnvironmentBranchCapture(ctx, ready.ID, lease, state.EnvironmentBranchCaptureArtifact{FCVersion: "1.12.0", StorageKey: readyKeys[0]}); err != nil {
		t.Fatal(err)
	}

	fx.loop.collectEnvironmentBranchCaptureObjects(ctx, fx.be, time.Now())
	if !exists(failedKeys[0]) {
		t.Fatal("a failed attempt was collected inside the grace")
	}
	fx.loop.collectEnvironmentBranchCaptureObjects(ctx, fx.be, time.Now().Add(2*environmentBranchCaptureObjectGrace))
	for _, key := range failedKeys {
		if exists(key) {
			t.Errorf("failed attempt object %s survived", key)
		}
	}
	for _, key := range readyKeys[:3] {
		if !exists(key) {
			t.Errorf("ready capture object %s was deleted", key)
		}
	}
	if garbage, err := fx.store.EnvironmentBranchCaptureGarbage(ctx, time.Now().Add(2*environmentBranchCaptureObjectGrace), 10); err != nil || len(garbage) != 0 {
		t.Fatalf("garbage after collection = %+v, %v", garbage, err)
	}
}
