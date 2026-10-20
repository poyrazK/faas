// adr: 171 — trusted publication writes only verified, tenant-free runtime snapshots.

package sched_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func publisherTestIdentity() sched.RuntimeSnapshotIdentity {
	return sched.RuntimeSnapshotIdentity{
		Runtime:             api.ExecutionRuntimeNode22,
		Architecture:        "amd64",
		KernelDigest:        strings.Repeat("a", 64),
		GuestExecutorDigest: strings.Repeat("b", 64),
		BaseImageDigest:     strings.Repeat("c", 64),
		MemoryMB:            128,
		EphemeralDiskMB:     64,
		FormatVersion:       sched.CurrentRuntimeSnapshotFormatVersion,
	}
}

func TestRuntimeSnapshotPublisherPublishesVerifiedPair(t *testing.T) {
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewMemStore()
	publisher := sched.NewRuntimeSnapshotPublisher(store, backend)
	memory := bytes.Repeat([]byte("m"), 128)
	vmstate := bytes.Repeat([]byte("v"), 32)
	created := time.Unix(10, 0).UTC()

	entry, err := publisher.PublishRuntimeSnapshot(context.Background(), sched.RuntimeSnapshotPublication{
		Identity: publisherTestIdentity(),
		Memory:   bytes.NewReader(memory),
		VMState:  bytes.NewReader(vmstate),
		MemBytes: int64(len(memory)), VMStateBytes: int64(len(vmstate)),
		CreatedAt: created, Sanitized: true, PayloadFree: true,
	})
	if err != nil {
		t.Fatalf("PublishRuntimeSnapshot: %v", err)
	}
	if entry.State != sched.RuntimeSnapshotReady || entry.CreatedAt != created {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.SnapshotDigest != sched.RuntimeSnapshotDigest(memory, vmstate) {
		t.Fatalf("snapshot digest = %s, want %s", entry.SnapshotDigest, sched.RuntimeSnapshotDigest(memory, vmstate))
	}
	if !strings.HasPrefix(entry.StorageKey, "snap/"+sched.RuntimeSnapshotStorageNamespace+"/captures/") || !strings.HasSuffix(entry.StorageKey, "/mem") {
		t.Fatalf("storage key = %q", entry.StorageKey)
	}

	readBack, err := backend.Get(context.Background(), entry.StorageKey)
	if err != nil {
		t.Fatalf("Get memory: %v", err)
	}
	_ = readBack.Close()
	vmstateKey := sched.RuntimeSnapshotVMStateStorageKey(entry.StorageKey)
	readBack, err = backend.Get(context.Background(), vmstateKey)
	if err != nil {
		t.Fatalf("Get vmstate: %v", err)
	}
	_ = readBack.Close()

	catalog := sched.NewRuntimeSnapshotCatalog(sched.NewStateRuntimeSnapshotIndex(store), sched.NewStorageRuntimeSnapshotVerifier(backend))
	plan, err := catalog.Resolve(context.Background(), sched.RuntimeSnapshotRequest{
		Shape:        api.ExecutionSnapshotShape{Runtime: api.ExecutionRuntimeNode22, MemoryMB: 128, EphemeralDiskMB: 64},
		Architecture: "amd64",
		KernelDigest: strings.Repeat("a", 64), GuestExecutorDigest: strings.Repeat("b", 64),
		BaseImageDigest: strings.Repeat("c", 64), FormatVersion: sched.CurrentRuntimeSnapshotFormatVersion,
	})
	if err != nil || plan.Mode != sched.RuntimeSnapshotRestore || plan.Snapshot == nil {
		t.Fatalf("catalog plan = %#v, err=%v", plan, err)
	}
}

func TestRuntimeSnapshotPublisherRejectsUnsafeOrIncompleteCapture(t *testing.T) {
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	publisher := sched.NewRuntimeSnapshotPublisher(state.NewMemStore(), backend)
	base := sched.RuntimeSnapshotPublication{
		Identity: publisherTestIdentity(), Memory: bytes.NewReader([]byte("mem")), VMState: bytes.NewReader([]byte("state")),
		MemBytes: 3, VMStateBytes: 5, Sanitized: true, PayloadFree: true,
	}
	tests := []struct {
		name   string
		mutate func(*sched.RuntimeSnapshotPublication)
	}{
		{name: "missing reader", mutate: func(p *sched.RuntimeSnapshotPublication) { p.Memory = nil }},
		{name: "payload-bearing", mutate: func(p *sched.RuntimeSnapshotPublication) { p.PayloadFree = false }},
		{name: "not sanitized", mutate: func(p *sched.RuntimeSnapshotPublication) { p.Sanitized = false }},
		{name: "invalid size", mutate: func(p *sched.RuntimeSnapshotPublication) { p.MemBytes = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publication := base
			test.mutate(&publication)
			if _, err := publisher.PublishRuntimeSnapshot(context.Background(), publication); !errors.Is(err, sched.ErrRuntimeSnapshotInvalid) {
				t.Fatalf("PublishRuntimeSnapshot = %v, want ErrRuntimeSnapshotInvalid", err)
			}
		})
	}
}

func TestRuntimeSnapshotPublisherDoesNotOverwriteCatalogOrLeavePartialObjects(t *testing.T) {
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewMemStore()
	publisher := sched.NewRuntimeSnapshotPublisher(store, backend)
	identity := publisherTestIdentity()
	publication := sched.RuntimeSnapshotPublication{
		Identity: identity, Memory: bytes.NewReader([]byte("mem")), VMState: bytes.NewReader([]byte("state")),
		MemBytes: 3, VMStateBytes: 5, Sanitized: true, PayloadFree: true,
	}
	first, err := publisher.PublishRuntimeSnapshot(context.Background(), publication)
	if err != nil {
		t.Fatalf("first publication: %v", err)
	}
	if _, err := publisher.PublishRuntimeSnapshot(context.Background(), publication); !errors.Is(err, sched.ErrRuntimeSnapshotConflict) {
		t.Fatalf("duplicate publication = %v, want ErrRuntimeSnapshotConflict", err)
	}
	stored, err := store.LookupRuntimeSnapshot(context.Background(), mustIdentityKey(identity))
	if err != nil || stored.StorageKey != first.StorageKey {
		t.Fatalf("stored row = %#v, err=%v", stored, err)
	}

	badBackend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	badPublisher := sched.NewRuntimeSnapshotPublisher(state.NewMemStore(), badBackend)
	bad := publication
	bad.Memory = bytes.NewReader([]byte("mem"))
	bad.VMState = bytes.NewReader([]byte("state"))
	bad.MemBytes = 4
	other := publisherTestIdentity()
	other.BaseImageDigest = strings.Repeat("d", 64)
	bad.Identity = other
	if _, err := badPublisher.PublishRuntimeSnapshot(context.Background(), bad); !errors.Is(err, sched.ErrRuntimeSnapshotInvalid) {
		t.Fatalf("short capture = %v, want ErrRuntimeSnapshotInvalid", err)
	}
	keys, err := badBackend.List(context.Background(), "snap/"+sched.RuntimeSnapshotStorageNamespace+"/captures")
	if err != nil {
		t.Fatalf("List failed publication objects: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("failed publication left objects: %v", keys)
	}

	oversized := publication
	oversized.Identity = publisherTestIdentity()
	oversized.Identity.BaseImageDigest = strings.Repeat("e", 64)
	oversized.Memory = bytes.NewReader([]byte("mem-extra"))
	oversized.VMState = bytes.NewReader([]byte("state"))
	if _, err := badPublisher.PublishRuntimeSnapshot(context.Background(), oversized); !errors.Is(err, sched.ErrRuntimeSnapshotInvalid) {
		t.Fatalf("oversized capture = %v, want ErrRuntimeSnapshotInvalid", err)
	}
	keys, err = badBackend.List(context.Background(), "snap/"+sched.RuntimeSnapshotStorageNamespace+"/captures")
	if err != nil {
		t.Fatalf("List oversized publication objects: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("oversized publication left objects: %v", keys)
	}
}

func TestRuntimeSnapshotPublisherPreservesCancellation(t *testing.T) {
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	publisher := sched.NewRuntimeSnapshotPublisher(state.NewMemStore(), backend)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = publisher.PublishRuntimeSnapshot(ctx, sched.RuntimeSnapshotPublication{Identity: publisherTestIdentity(), Memory: bytes.NewReader([]byte("m")), VMState: bytes.NewReader([]byte("v")), MemBytes: 1, VMStateBytes: 1, Sanitized: true, PayloadFree: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishRuntimeSnapshot = %v, want context.Canceled", err)
	}
}

func mustIdentityKey(identity sched.RuntimeSnapshotIdentity) string {
	key, err := identity.Key()
	if err != nil {
		panic(err)
	}
	return key
}
