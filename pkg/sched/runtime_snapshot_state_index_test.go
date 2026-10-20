package sched_test

// adr: 171 — durable state-backed snapshot lookup preserves resolver fallback.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func recordForState(entry sched.RuntimeSnapshot) state.RuntimeSnapshotRecord {
	key, _ := entry.Identity.Key()
	return state.RuntimeSnapshotRecord{
		Profile:             entry.Identity.Profile,
		CatalogKey:          key,
		Runtime:             entry.Identity.Runtime,
		Architecture:        entry.Identity.Architecture,
		KernelDigest:        entry.Identity.KernelDigest,
		GuestExecutorDigest: entry.Identity.GuestExecutorDigest,
		BaseImageDigest:     entry.Identity.BaseImageDigest,
		MemoryMB:            entry.Identity.MemoryMB,
		EphemeralDiskMB:     entry.Identity.EphemeralDiskMB,
		FormatVersion:       entry.Identity.FormatVersion,
		StorageKey:          entry.StorageKey,
		SnapshotDigest:      entry.SnapshotDigest,
		MemBytes:            entry.MemBytes,
		VMStateBytes:        entry.VMStateBytes,
		Sanitized:           entry.Sanitized,
		PayloadFree:         entry.PayloadFree,
		State:               string(entry.State),
		CreatedAt:           entry.CreatedAt,
	}
}

func TestStateRuntimeSnapshotIndexResolvesAndRetires(t *testing.T) {
	store := state.NewMemStore()
	entry := sched.ValidRuntimeSnapshot()
	if _, err := store.PublishRuntimeSnapshot(context.Background(), recordForState(entry)); err != nil {
		t.Fatalf("PublishRuntimeSnapshot: %v", err)
	}
	index := sched.NewStateRuntimeSnapshotIndex(store)
	key, _ := entry.Identity.Key()
	got, err := index.LookupRuntimeSnapshot(context.Background(), key)
	if err != nil {
		t.Fatalf("LookupRuntimeSnapshot: %v", err)
	}
	if got.Identity != entry.Identity || got.StorageKey != entry.StorageKey {
		t.Fatalf("lookup = %#v, want %#v", got, entry)
	}

	request := sched.ValidRuntimeSnapshotRequest()
	catalog := sched.NewRuntimeSnapshotCatalog(index, sched.RuntimeSnapshotVerifierFunc(func(context.Context, sched.RuntimeSnapshot) error { return nil }))
	plan, err := catalog.Resolve(context.Background(), request)
	if err != nil || plan.Mode != sched.RuntimeSnapshotRestore {
		t.Fatalf("Resolve = %#v, %v", plan, err)
	}
	if err := store.RetireRuntimeSnapshot(context.Background(), key, time.Unix(2, 0)); err != nil {
		t.Fatalf("RetireRuntimeSnapshot: %v", err)
	}
	plan, err = catalog.Resolve(context.Background(), request)
	if err != nil || plan.Mode != sched.RuntimeSnapshotColdBoot || plan.FallbackReason != sched.RuntimeSnapshotFallbackRetired {
		t.Fatalf("retired Resolve = %#v, %v", plan, err)
	}
}

func TestStateRuntimeSnapshotIndexMapsMissingRows(t *testing.T) {
	index := sched.NewStateRuntimeSnapshotIndex(state.NewMemStore())
	_, err := index.LookupRuntimeSnapshot(context.Background(), "missing")
	if !errors.Is(err, sched.ErrRuntimeSnapshotNotFound) {
		t.Fatalf("LookupRuntimeSnapshot = %v, want ErrRuntimeSnapshotNotFound", err)
	}
}
