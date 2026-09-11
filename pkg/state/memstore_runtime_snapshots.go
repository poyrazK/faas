package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

var _ RuntimeSnapshotStore = (*MemStore)(nil)

func (m *MemStore) PublishRuntimeSnapshot(ctx context.Context, record RuntimeSnapshotRecord) (RuntimeSnapshotRecord, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshotRecord{}, err
	}
	if record.State != RuntimeSnapshotStateReady {
		return RuntimeSnapshotRecord{}, ErrRuntimeSnapshotInvalid
	}
	if record.PublishedAt.IsZero() {
		record.PublishedAt = time.Now().UTC()
	} else {
		record.PublishedAt = record.PublishedAt.UTC()
	}
	record.CreatedAt = record.CreatedAt.UTC()
	if err := record.Validate(); err != nil {
		return RuntimeSnapshotRecord{}, err
	}
	if record.ID == "" {
		record.ID = uuid.NewString()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeSnapshots == nil {
		m.runtimeSnapshots = make(map[string]RuntimeSnapshotRecord)
	}
	if _, exists := m.runtimeSnapshots[record.CatalogKey]; exists {
		return RuntimeSnapshotRecord{}, ErrConflict
	}
	m.runtimeSnapshots[record.CatalogKey] = cloneRuntimeSnapshotRecord(record)
	return cloneRuntimeSnapshotRecord(record), nil
}

func (m *MemStore) LookupRuntimeSnapshot(ctx context.Context, catalogKey string) (RuntimeSnapshotRecord, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshotRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.runtimeSnapshots[catalogKey]
	if !ok {
		return RuntimeSnapshotRecord{}, ErrNotFound
	}
	if err := record.Validate(); err != nil {
		return RuntimeSnapshotRecord{}, err
	}
	return cloneRuntimeSnapshotRecord(record), nil
}

func (m *MemStore) RetireRuntimeSnapshot(ctx context.Context, catalogKey string, retiredAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if retiredAt.IsZero() {
		return ErrRuntimeSnapshotInvalid
	}
	retiredAt = retiredAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.runtimeSnapshots[catalogKey]
	if !ok {
		return ErrNotFound
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if record.State == RuntimeSnapshotStateRetired {
		return nil
	}
	if retiredAt.Before(record.CreatedAt) {
		return ErrRuntimeSnapshotInvalid
	}
	record.State = RuntimeSnapshotStateRetired
	record.RetiredAt = &retiredAt
	m.runtimeSnapshots[catalogKey] = record
	return nil
}
