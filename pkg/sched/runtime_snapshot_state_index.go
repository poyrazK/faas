package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// StateRuntimeSnapshotIndex adapts the durable state catalog to the scheduler
// resolver. The adapter performs a second validation after the database read so
// a malformed row becomes a cache miss rather than a restore attempt.
type StateRuntimeSnapshotIndex struct {
	store state.RuntimeSnapshotStore
}

func NewStateRuntimeSnapshotIndex(store state.RuntimeSnapshotStore) *StateRuntimeSnapshotIndex {
	return &StateRuntimeSnapshotIndex{store: store}
}

var _ RuntimeSnapshotIndex = (*StateRuntimeSnapshotIndex)(nil)

func (i *StateRuntimeSnapshotIndex) LookupRuntimeSnapshot(ctx context.Context, catalogKey string) (RuntimeSnapshot, error) {
	if i == nil || i.store == nil {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotUnwired
	}
	record, err := i.store.LookupRuntimeSnapshot(ctx, catalogKey)
	if errors.Is(err, state.ErrNotFound) {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotNotFound
	}
	if err != nil {
		if errors.Is(err, state.ErrRuntimeSnapshotInvalid) {
			return RuntimeSnapshot{}, ErrRuntimeSnapshotCorrupt
		}
		return RuntimeSnapshot{}, err
	}
	identity := RuntimeSnapshotIdentity{
		Runtime:             record.Runtime,
		Architecture:        record.Architecture,
		KernelDigest:        record.KernelDigest,
		GuestExecutorDigest: record.GuestExecutorDigest,
		BaseImageDigest:     record.BaseImageDigest,
		MemoryMB:            record.MemoryMB,
		EphemeralDiskMB:     record.EphemeralDiskMB,
		FormatVersion:       record.FormatVersion,
	}
	entry := RuntimeSnapshot{
		Identity:       identity,
		StorageKey:     record.StorageKey,
		SnapshotDigest: record.SnapshotDigest,
		MemBytes:       record.MemBytes,
		VMStateBytes:   record.VMStateBytes,
		Sanitized:      record.Sanitized,
		PayloadFree:    record.PayloadFree,
		State:          RuntimeSnapshotState(record.State),
		CreatedAt:      record.CreatedAt,
	}
	if err := entry.Validate(); err != nil {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotCorrupt
	}
	key, err := identity.Key()
	if err != nil || key != catalogKey {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotCorrupt
	}
	return entry, nil
}
