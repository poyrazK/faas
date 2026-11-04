// adr: 20261022184951 — amendment 1: native runtime snapshots live in a tree only vmmd writes.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/storage"
)

// ErrNativeRuntimeSnapshotRootUnset refuses work that needs the native tree on
// a vmmd that was not given one.
var ErrNativeRuntimeSnapshotRootUnset = errors.New("native runtime capture: native_runtime_snapshot_root is not configured")

// UseNativeRuntimeSnapshotRoot opens the tree native runtime captures publish
// to. vmmd must be its only writer, so the root is refused unless it passes the
// exclusive writer's private-root check. It stays readable for restores and
// collection even after native_runtime_capture is turned off.
func (v *JailerVMM) UseNativeRuntimeSnapshotRoot(root string) error {
	if err := storage.CheckPrivateExclusiveRoot(root); err != nil {
		return fmt.Errorf("vmm: native_runtime_snapshot_root: %w", err)
	}
	backend, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		return fmt.Errorf("vmm: native_runtime_snapshot_root: %w", err)
	}
	v.nativeRuntimeSnapshots = backend
	return nil
}

// nativeRuntimeSnapshotObject reports whether key names one object of a v2
// runtime capture (memory, device state, drive or backing sidecar). It returns
// the capture's memory key and the shared captures directory above it.
func nativeRuntimeSnapshotObject(key string) (memory, captures string, ok bool) {
	slash := strings.LastIndex(key, "/")
	if slash < 0 {
		return "", "", false
	}
	switch key[slash+1:] {
	case "mem", "vmstate", "drive", "backing":
	default:
		return "", "", false
	}
	memory = key[:slash+1] + "mem"
	parts := strings.Split(memory, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	deployment, err := uuid.Parse(parts[1])
	if err != nil {
		return "", "", false
	}
	keys := nativeRuntimeCaptureKeys(memory)
	if runtimeadmission.CheckSnapshotCaptureKeys(deployment.String(), keys.Memory, keys.VMState, keys.Drive) != nil ||
		!canonicalNativeHelperID(nativeRuntimeCaptureID(memory)) ||
		key != keys.Memory && key != keys.VMState && key != keys.Drive && key != keys.Backing {
		return "", "", false
	}
	return memory, path.Dir(path.Dir(path.Dir(memory))), true
}

// nativeRuntimeSnapshotStore returns the native tree when it holds key, and
// nil otherwise. Capture keys are fresh per capture, so a key names an object
// in at most one of the native and canonical trees.
func (v *JailerVMM) nativeRuntimeSnapshotStore(key string) storage.StorageBackend {
	if v == nil || v.nativeRuntimeSnapshots == nil {
		return nil
	}
	if _, _, ok := nativeRuntimeSnapshotObject(key); !ok {
		return nil
	}
	full, _, err := v.nativeRuntimeSnapshots.LocalPath(key)
	if err != nil {
		return nil
	}
	if _, err := os.Lstat(full); err != nil {
		return nil
	}
	return v.nativeRuntimeSnapshots
}

// snapshotStore is the backend a restore reads key from: the native tree when
// it holds the object, the shared backend otherwise.
func (v *JailerVMM) snapshotStore(key string) storage.StorageBackend {
	if native := v.nativeRuntimeSnapshotStore(key); native != nil {
		return native
	}
	return v.storage
}

// nativeSnapshotPublicationBackend is where the producer publishes. On a node
// whose shared backend is local, runtime captures go only to the native tree;
// a remote backend's own conditional writes already exclude other writers, so
// runtime captures stay there. Qualification captures keep the shared backend.
func (v *JailerVMM) nativeSnapshotPublicationBackend(ctx context.Context) (storage.StorageBackend, error) {
	if permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit); ok && permit.runtime() &&
		!storage.IsRemoteBackendKind(os.Getenv("FAAS_STORAGE_BACKEND")) {
		if v.nativeRuntimeSnapshots == nil {
			return nil, ErrNativeRuntimeSnapshotRootUnset
		}
		return v.nativeRuntimeSnapshots, nil
	}
	return v.storage, nil
}

// RetireNativeRuntimeSnapshotObject removes one object of a runtime capture
// from the native tree on behalf of imaged, which owns snapshot collection but
// cannot write the tree. vmmd is the tree's only writer, so removing the key by
// name removes exactly what it published. Only v2 capture object keys are
// accepted, and a missing object is success, as for Delete.
func (v *JailerVMM) RetireNativeRuntimeSnapshotObject(ctx context.Context, key string) error {
	_, captures, ok := nativeRuntimeSnapshotObject(key)
	if !ok {
		return fmt.Errorf("vmm: retire native runtime snapshot object %q: %w", key, storage.ErrInvalidKey)
	}
	if v.nativeRuntimeSnapshots == nil {
		return ErrNativeRuntimeSnapshotRootUnset
	}
	return v.nativeRuntimeSnapshots.DeleteExclusive(ctx, key, captures)
}
