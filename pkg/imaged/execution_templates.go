package imaged

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// CollectExecutionTemplateOrphans removes only unreferenced, old captures in
// the platform namespace. Ready and retired catalog rows are both retained.
// The ordinary deployment sweep must never classify that namespace as an app.
func (l *Loop) CollectExecutionTemplateOrphans(ctx context.Context, now time.Time) error {
	reader, ok := l.store.(state.RuntimeSnapshotRetentionReader)
	if !ok {
		return errors.New("imaged: runtime template retention reader unavailable")
	}
	rows, err := reader.RuntimeSnapshotRecords(ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, row := range rows {
		known[row.StorageKey] = true
	}
	if l.storageRoot == "" || l.handler == nil {
		return nil
	}
	backend, err := storage.NewLocalStorageBackend(l.storageRoot)
	if err != nil {
		return err
	}
	roots := []string{l.storageRoot}
	if l.handler.nativeSnapshotRoot != "" && l.handler.nativeSnapshotRoot != l.storageRoot {
		roots = append(roots, l.handler.nativeSnapshotRoot)
	}
	for _, root := range roots {
		if err := l.collectExecutionTemplateRoot(ctx, now, root, backend, known); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) collectExecutionTemplateRoot(ctx context.Context, now time.Time, root string, backend storage.StorageBackend, known map[string]bool) error {
	top := filepath.Join(root, "snap", uuid.Nil.String(), "captures")
	for _, dir := range []string{filepath.Dir(filepath.Dir(top)), filepath.Dir(top), top} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("imaged: template namespace is not an owned directory")
		}
	}
	entries, err := os.ReadDir(top)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, err := uuid.Parse(entry.Name())
		if err != nil || id == uuid.Nil || id.String() != entry.Name() || !entry.IsDir() {
			continue
		}
		key := "snap/" + uuid.Nil.String() + "/captures/" + entry.Name() + "/v2/mem"
		if known[key] {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(now.Add(-localSnapshotOrphanGrace)) {
			continue
		}
		v2, err := os.Lstat(filepath.Join(top, entry.Name(), "v2"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !v2.IsDir() || v2.Mode()&os.ModeSymlink != 0 {
			continue
		}
		snap := state.Snapshot{StorageKey: key}
		for _, key := range []string{key, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
			if err := l.handler.deleteSnapshotObject(ctx, backend, key); err != nil && !storage.IsNotFound(err) {
				return err
			}
			if root != l.handler.nativeSnapshotRoot {
				removeEmptySnapshotParents(root, key)
			}
		}
	}
	return nil
}
