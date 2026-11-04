// adr: 733
package imaged

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type recordingDeleter struct {
	deleted []string
	fail    map[string]error
}

func (d *recordingDeleter) Delete(_ context.Context, key string) error {
	d.deleted = append(d.deleted, key)
	return d.fail[key]
}

func TestDeleteCrashCaptureFilesRemovesAllFourObjects(t *testing.T) {
	mem := state.SnapshotCaptureMemKey("dep-1", state.SnapshotTierWarm, "cap-1")
	vmstate := state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem})
	capture := state.CrashCapture{ID: "cap-1", DeploymentID: "dep-1", StorageKey: &mem, VMStateStorageKey: &vmstate}
	snap := state.Snapshot{DeploymentID: "dep-1", StorageKey: mem, Tier: state.SnapshotTierWarm}
	want := []string{mem, vmstate, state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)}

	d := &recordingDeleter{fail: map[string]error{vmstate: storage.ErrNotFound}}
	if err := deleteCrashCaptureFiles(context.Background(), d, capture); err != nil {
		t.Fatalf("delete with a missing object: %v (missing must be success)", err)
	}
	for _, key := range want {
		if key != "" && !slices.Contains(d.deleted, key) {
			t.Errorf("did not delete %q (deleted %v)", key, d.deleted)
		}
	}

	failing := &recordingDeleter{fail: map[string]error{mem: context.DeadlineExceeded}}
	if err := deleteCrashCaptureFiles(context.Background(), failing, capture); err == nil {
		t.Fatal("a real delete failure was swallowed; the capture would be marked expired with files left behind")
	}
}
