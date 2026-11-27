package fcvm

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// This wrapper deliberately has no LocalPathResolver: the ordinary snapshot
// fast path must not bypass exclusive publication or write into the shared
// group-writable snapshot tree. Only vmmd writes the native root.
type executionTemplateStorage struct {
	backend          *storage.LocalStorageBackend
	memory, captures string
}

func (v *JailerVMM) executionTemplateStorage(ctx context.Context, memory string) (storage.StorageBackend, error) {
	if storage.IsRemoteBackendKind(os.Getenv("FAAS_STORAGE_BACKEND")) {
		return nil, errors.New("fcvm: execution templates require the local snapshot backend")
	}
	if v.nativeRecovery != nil && (!v.nativeRuntimeCaptureEnabled() || v.nativeRecovery.journal == nil || !v.nativeRecovery.journal.owners) {
		return nil, errors.New("fcvm: native execution templates require runtime capture and launch owner records")
	}
	mem, captures, ok := nativeRuntimeSnapshotObject(memory)
	if !ok || mem != memory || v.nativeRuntimeSnapshots == nil {
		return nil, ErrNativeRuntimeSnapshotRootUnset
	}
	for _, key := range []string{memory, state.SnapshotVMStateKey(state.Snapshot{StorageKey: memory}), state.SnapshotDriveKey(state.Snapshot{StorageKey: memory}), state.SnapshotBackingKey(state.Snapshot{StorageKey: memory})} {
		reader, err := v.nativeRuntimeSnapshots.Get(ctx, key)
		if reader != nil {
			_ = reader.Close()
			return nil, storage.ErrArtifactExists
		}
		if !storage.IsNotFound(err) {
			return nil, err
		}
	}
	return &executionTemplateStorage{backend: v.nativeRuntimeSnapshots, memory: memory, captures: captures}, nil
}

func (b *executionTemplateStorage) owns(key string) bool {
	mem, _, ok := nativeRuntimeSnapshotObject(key)
	return ok && mem == b.memory
}

func (b *executionTemplateStorage) Put(ctx context.Context, key string, reader io.Reader) error {
	if !b.owns(key) {
		return storage.ErrInvalidKey
	}
	var size int64
	switch source := reader.(type) {
	case *os.File:
		info, err := source.Stat()
		if err != nil {
			return err
		}
		size = info.Size()
	case interface{ Len() int }:
		size = int64(source.Len())
	default:
		return errors.New("fcvm: template publication requires a bounded native artifact")
	}
	if size <= 0 {
		return errors.New("fcvm: empty template artifact")
	}
	return storage.PutExclusive(ctx, b.backend, key, reader, size)
}
func (b *executionTemplateStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if !b.owns(key) {
		return nil, storage.ErrInvalidKey
	}
	return b.backend.Get(ctx, key)
}
func (b *executionTemplateStorage) Delete(ctx context.Context, key string) error {
	if !b.owns(key) {
		return storage.ErrInvalidKey
	}
	return b.backend.DeleteExclusive(ctx, key, b.captures)
}

// RetireNativeRuntimeSnapshotObject forwards imaged's existing retirement
// decision to the VMM that owns the private tree. The server receives Manager
// through its lifecycle adapter, rather than receiving JailerVMM directly.
func (m *Manager) RetireNativeRuntimeSnapshotObject(ctx context.Context, key string) error {
	if m == nil || m.vmm == nil {
		return ErrNativeRuntimeSnapshotRootUnset
	}
	retirer, ok := m.vmm.(interface {
		RetireNativeRuntimeSnapshotObject(context.Context, string) error
	})
	if !ok {
		return ErrNativeRuntimeSnapshotRootUnset
	}
	return retirer.RetireNativeRuntimeSnapshotObject(ctx, key)
}

func (m *Manager) retireExecutionTemplate(ctx context.Context, key string) error {
	var result error
	for _, name := range []string{"mem", "vmstate", "drive", "backing"} {
		object := strings.TrimSuffix(key, "mem") + name
		result = errors.Join(result, m.RetireNativeRuntimeSnapshotObject(ctx, object))
	}
	return result
}
