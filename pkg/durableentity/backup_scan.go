// adr: 852
package durableentity

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type BackupScan struct {
	NextCursor                string
	Captured, Deleted, Failed int
}

// CheckBackups probes only a unique backup probe, never a customer entity.
func (m *Manager) CheckBackups(ctx context.Context) error {
	store, ok := m.store.(CleanupStore)
	if !ok {
		return ErrUnsupported
	}
	if _, err := m.entityPrefixes(ctx, "", api.DurableEntityBackupScanPageSize); err != nil {
		return err
	}
	prefix := "gregale/durable-entity-backups/v1/probes/" + uuid.NewString() + "/"
	key := prefix + "probe.json"
	if _, err := m.store.Put(ctx, key, []byte(`{}`), ""); err != nil {
		return writeFailure("create backup probe", err)
	}
	page, err := store.ListEntityObjects(ctx, prefix, "", api.DurableEntityBackupListPageSize)
	if err != nil {
		return err
	}
	if len(page.Keys) != 1 || page.Keys[0] != key || page.NextCursor != "" {
		return ErrUnsupported
	}
	if err := store.DeleteEntityObject(ctx, key); err != nil {
		return err
	}
	if _, _, err := m.store.Get(ctx, key, api.MaxDurableEntityBackupBytes); !errors.Is(err, ErrNotFound) {
		return errors.Join(ErrUnsupported, err)
	}
	return nil
}

// ScanBackups visits a bounded page with per-entity deadlines. It neither
// acquires execution ownership nor invokes guest code. Partial failures do not
// prevent rotation; failed entities are retried on the next rotation.
func (m *Manager) ScanBackups(ctx context.Context, cursor string, allowed func(ID) bool) (BackupScan, error) {
	if allowed == nil {
		return BackupScan{}, ErrInvalid
	}
	page, err := m.entityPrefixes(ctx, cursor, api.DurableEntityBackupScanPageSize)
	if err != nil {
		return BackupScan{}, err
	}
	out := BackupScan{NextCursor: page.NextCursor}
	for _, prefix := range page.Prefixes {
		if err := ctx.Err(); err != nil {
			return BackupScan{}, err
		}
		if err := m.backupAtPrefix(ctx, prefix, allowed, &out); err != nil {
			out.Failed++
		}
	}
	return out, nil
}

func (m *Manager) backupAtPrefix(ctx context.Context, prefix string, allowed func(ID) bool, out *BackupScan) error {
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityBackupReadTimeout)
	defer cancel()
	base, err := m.manifestAtPrefix(ctx, prefix)
	if err != nil {
		return err
	}
	if !allowed(base.ID) || base.Version == 0 {
		return nil
	}
	if _, err := m.BackupState(ctx, base.ID); err != nil {
		return err
	}
	out.Captured++
	deleted, err := m.PruneBackups(ctx, base.ID)
	out.Deleted += deleted
	return err
}
