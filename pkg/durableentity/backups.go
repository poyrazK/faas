// adr: 942
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const backupTimeFormat = "20060102T150405Z"

type Backup struct {
	CapturedAt time.Time   `json:"captured_at"`
	Export     StateExport `json:"export"`
}

type BackupInfo struct {
	ID         string    `json:"id"`
	CapturedAt time.Time `json:"captured_at"`
	Version    uint64    `json:"version"`
}

type BackupPage struct {
	Items      []BackupInfo `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

func backupPrefix(id ID) string {
	return "gregale/durable-entity-backups/v1/" + strings.TrimSuffix(strings.TrimPrefix(id.prefix(), "gregale/durable-entities/v1/entities/"), "/") + "/"
}

func backupTime(value string) (time.Time, bool) {
	at, err := time.Parse(backupTimeFormat, value)
	return at, err == nil && at.Format(backupTimeFormat) == value && at.Equal(at.Truncate(api.DurableEntityBackupInterval))
}

// ReadBackup checks scope, checksum and the slot key before exposing any data.
func (m *Manager) ReadBackup(ctx context.Context, id ID, backupID string) (Backup, error) {
	at, valid := backupTime(backupID)
	if !id.valid() || !valid {
		return Backup{}, ErrInvalid
	}
	body, _, err := m.store.Get(ctx, backupPrefix(id)+backupID+".json", api.MaxDurableEntityBackupBytes)
	if err != nil {
		return Backup{}, err
	}
	var out Backup
	if len(body) > api.MaxDurableEntityBackupBytes || json.Unmarshal(body, &out) != nil || !out.CapturedAt.Equal(at) || !validStateExport(id, out.Export) {
		return Backup{}, ErrCorrupt
	}
	return out, nil
}

// ListBackups returns bounded metadata only. A deletion race is retryable.
func (m *Manager) ListBackups(ctx context.Context, id ID, cursor string) (BackupPage, error) {
	store, ok := m.store.(EntityObjectLister)
	if !ok {
		return BackupPage{}, ErrUnsupported
	}
	if !id.valid() || len(cursor) > api.MaxObjectS3ListCursorBytes {
		return BackupPage{}, ErrInvalid
	}
	prefix := backupPrefix(id)
	page, err := store.ListEntityObjects(ctx, prefix, cursor, api.DurableEntityBackupListPageSize)
	if err != nil {
		return BackupPage{}, err
	}
	if len(page.Keys) > api.DurableEntityBackupListPageSize || len(page.NextCursor) > api.MaxObjectS3ListCursorBytes || page.NextCursor != "" && (page.NextCursor == cursor || len(page.Keys) == 0) {
		return BackupPage{}, ErrCorrupt
	}
	out := BackupPage{Items: []BackupInfo{}, NextCursor: page.NextCursor}
	for _, key := range page.Keys {
		name := strings.TrimSuffix(strings.TrimPrefix(key, prefix), ".json")
		if key != prefix+name+".json" {
			return BackupPage{}, ErrCorrupt
		}
		value, err := m.ReadBackup(ctx, id, name)
		if errors.Is(err, ErrNotFound) {
			return BackupPage{}, ErrConflict
		}
		if err != nil {
			return BackupPage{}, err
		}
		out.Items = append(out.Items, BackupInfo{ID: name, CapturedAt: value.CapturedAt, Version: value.Export.Version})
	}
	return out, nil
}

func validStateExport(id ID, value StateExport) bool {
	return value.Format == 1 && value.Entity == id && value.Version > 0 && json.Valid(value.Data) && value.Checksum == exportChecksum(value)
}

// BackupState conditionally creates one immutable hourly slot. The first
// successful writer wins; uncertain publication is confirmed by a later read.
// A slot never replaces an earlier successful capture in the same hour.
func (m *Manager) BackupState(ctx context.Context, id ID) (BackupInfo, error) {
	at := m.now().UTC().Truncate(api.DurableEntityBackupInterval)
	name := at.Format(backupTimeFormat)
	if existing, err := m.ReadBackup(ctx, id, name); err == nil {
		return BackupInfo{ID: name, CapturedAt: at, Version: existing.Export.Version}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return BackupInfo{}, err
	}
	exported, err := m.ExportState(ctx, id)
	if err != nil {
		return BackupInfo{}, err
	}
	body, err := json.Marshal(Backup{CapturedAt: at, Export: exported})
	if err != nil {
		return BackupInfo{}, ErrInvalid
	}
	if len(body) > api.MaxDurableEntityBackupBytes {
		return BackupInfo{}, ErrLimit
	}
	if _, err := m.store.Put(ctx, backupPrefix(id)+name+".json", body, ""); err != nil {
		if errors.Is(err, ErrConflict) {
			value, err := m.ReadBackup(ctx, id, name)
			return BackupInfo{ID: name, CapturedAt: at, Version: value.Export.Version}, err
		}
		return BackupInfo{}, writeFailure("publish entity backup", err)
	}
	return BackupInfo{ID: name, CapturedAt: at, Version: exported.Version}, nil
}

// PruneBackups deletes only strictly older-than-retention slot keys, and only
// after confirming a current-hour backup. It never touches live entity objects.
// Page limits bound work; restart from the first page safely retries deletion.
func (m *Manager) PruneBackups(ctx context.Context, id ID) (int, error) {
	store, ok := m.store.(CleanupStore)
	if !ok {
		return 0, ErrUnsupported
	}
	if _, err := m.ReadBackup(ctx, id, m.now().UTC().Truncate(api.DurableEntityBackupInterval).Format(backupTimeFormat)); err != nil {
		return 0, err
	}
	prefix := backupPrefix(id)
	page, err := store.ListEntityObjects(ctx, prefix, "", api.DurableEntityBackupListPageSize)
	if err != nil {
		return 0, err
	}
	if len(page.Keys) > api.DurableEntityBackupListPageSize {
		return 0, ErrCorrupt
	}
	cutoff := m.now().UTC().Truncate(api.DurableEntityBackupInterval).Add(-api.DurableEntityBackupRetention)
	deleted := 0
	for _, key := range page.Keys {
		name := strings.TrimSuffix(strings.TrimPrefix(key, prefix), ".json")
		at, valid := backupTime(name)
		if !valid || key != prefix+name+".json" {
			return deleted, ErrCorrupt
		}
		if !at.Before(cutoff) {
			continue
		}
		if err := store.DeleteEntityObject(ctx, key); err != nil && !errors.Is(err, ErrNotFound) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
