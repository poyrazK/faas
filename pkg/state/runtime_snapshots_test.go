package state

// adr: 171 — durable sanitized runtime snapshot publication and retirement.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func validRuntimeSnapshotRecord() RuntimeSnapshotRecord {
	record := RuntimeSnapshotRecord{
		Runtime:             api.ExecutionRuntimeNode22,
		Architecture:        "amd64",
		KernelDigest:        strings.Repeat("a", 64),
		GuestExecutorDigest: strings.Repeat("b", 64),
		BaseImageDigest:     strings.Repeat("c", 64),
		MemoryMB:            128,
		EphemeralDiskMB:     64,
		FormatVersion:       1,
		StorageKey:          "execution-snapshots/node22-amd64/snapshot.bin",
		SnapshotDigest:      strings.Repeat("d", 64),
		MemBytes:            128 << 20,
		VMStateBytes:        4096,
		Sanitized:           true,
		PayloadFree:         true,
		State:               RuntimeSnapshotStateReady,
		CreatedAt:           time.Unix(1, 0).UTC(),
	}
	record.CatalogKey = record.identityKey()
	return record
}

func TestMemStoreRuntimeSnapshotPublicationIsImmutable(t *testing.T) {
	store := NewMemStore()
	record := validRuntimeSnapshotRecord()
	got, err := store.PublishRuntimeSnapshot(context.Background(), record)
	if err != nil {
		t.Fatalf("PublishRuntimeSnapshot: %v", err)
	}
	if got.ID == "" || got.PublishedAt.IsZero() {
		t.Fatalf("publication metadata missing: %#v", got)
	}
	if _, err := store.PublishRuntimeSnapshot(context.Background(), record); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate publication = %v, want ErrConflict", err)
	}
	copy, err := store.LookupRuntimeSnapshot(context.Background(), record.CatalogKey)
	if err != nil {
		t.Fatalf("LookupRuntimeSnapshot: %v", err)
	}
	if copy.StorageKey != record.StorageKey || copy.State != RuntimeSnapshotStateReady {
		t.Fatalf("lookup = %#v", copy)
	}
	copy.RetiredAt = nil
	if err := store.RetireRuntimeSnapshot(context.Background(), record.CatalogKey, time.Unix(2, 0)); err != nil {
		t.Fatalf("RetireRuntimeSnapshot: %v", err)
	}
	if err := store.RetireRuntimeSnapshot(context.Background(), record.CatalogKey, time.Unix(3, 0)); err != nil {
		t.Fatalf("idempotent RetireRuntimeSnapshot: %v", err)
	}
	retired, err := store.LookupRuntimeSnapshot(context.Background(), record.CatalogKey)
	if err != nil {
		t.Fatalf("retired lookup: %v", err)
	}
	if retired.State != RuntimeSnapshotStateRetired || retired.RetiredAt == nil || !retired.RetiredAt.Equal(time.Unix(2, 0).UTC()) {
		t.Fatalf("retired row = %#v", retired)
	}
}

func TestRuntimeSnapshotRecordValidationRejectsUnsafePublication(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RuntimeSnapshotRecord)
	}{
		{"catalog key mismatch", func(record *RuntimeSnapshotRecord) { record.CatalogKey = "wrong" }},
		{"payload-bearing", func(record *RuntimeSnapshotRecord) { record.PayloadFree = false }},
		{"unsafe storage key", func(record *RuntimeSnapshotRecord) { record.StorageKey = "../tenant-source" }},
		{"uppercase digest", func(record *RuntimeSnapshotRecord) { record.SnapshotDigest = strings.Repeat("A", 64) }},
		{"retired on publication", func(record *RuntimeSnapshotRecord) { record.State = RuntimeSnapshotStateRetired }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validRuntimeSnapshotRecord()
			test.mutate(&record)
			if _, err := NewMemStore().PublishRuntimeSnapshot(context.Background(), record); !errors.Is(err, ErrRuntimeSnapshotInvalid) {
				t.Fatalf("PublishRuntimeSnapshot = %v, want ErrRuntimeSnapshotInvalid", err)
			}
		})
	}
}

func TestMemStoreRuntimeSnapshotNotFound(t *testing.T) {
	store := NewMemStore()
	if _, err := store.LookupRuntimeSnapshot(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LookupRuntimeSnapshot = %v, want ErrNotFound", err)
	}
	if err := store.RetireRuntimeSnapshot(context.Background(), "missing", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RetireRuntimeSnapshot = %v, want ErrNotFound", err)
	}
}
