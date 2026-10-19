// adr: 171 — restore only accepts a verified memory/vmstate snapshot pair.

package sched_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestStorageRuntimeSnapshotVerifierAuthenticatesPair(t *testing.T) {
	memory := bytes.Repeat([]byte("m"), 128)
	vmstate := bytes.Repeat([]byte("v"), 32)
	backend := &snapshotVerifyStorage{objects: map[string][]byte{
		"execution-snapshots/node22/mem":     memory,
		"execution-snapshots/node22/vmstate": vmstate,
	}}
	snapshot := validStorageRuntimeSnapshot(memory, vmstate)
	verifier := sched.NewStorageRuntimeSnapshotVerifier(backend)
	if err := verifier.VerifyRuntimeSnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("VerifyRuntimeSnapshot: %v", err)
	}
	if backend.gets != 2 {
		t.Fatalf("storage gets = %d, want 2", backend.gets)
	}

	backend.objects["execution-snapshots/node22/vmstate"][0] ^= 1
	if err := verifier.VerifyRuntimeSnapshot(context.Background(), snapshot); !errors.Is(err, sched.ErrRuntimeSnapshotCorrupt) {
		t.Fatalf("tampered vmstate error = %v, want ErrRuntimeSnapshotCorrupt", err)
	}
}

func TestStorageRuntimeSnapshotVerifierRejectsMissingAndWrongSize(t *testing.T) {
	memory := []byte("mem")
	vmstate := []byte("state")
	backend := &snapshotVerifyStorage{objects: map[string][]byte{
		"snap/runtime/mem":     memory,
		"snap/runtime/vmstate": vmstate,
	}}
	snapshot := validStorageRuntimeSnapshot(memory, vmstate)
	snapshot.StorageKey = "snap/runtime/mem"
	snapshot.MemBytes = int64(len(memory))
	snapshot.VMStateBytes = int64(len(vmstate))
	snapshot.SnapshotDigest = sched.RuntimeSnapshotDigest(memory, vmstate)
	verifier := sched.NewStorageRuntimeSnapshotVerifier(backend)

	delete(backend.objects, "snap/runtime/vmstate")
	if err := verifier.VerifyRuntimeSnapshot(context.Background(), snapshot); !errors.Is(err, sched.ErrRuntimeSnapshotCorrupt) {
		t.Fatalf("missing object error = %v, want ErrRuntimeSnapshotCorrupt", err)
	}
	backend.objects["snap/runtime/vmstate"] = append([]byte(nil), vmstate...)
	snapshot.VMStateBytes++
	if err := verifier.VerifyRuntimeSnapshot(context.Background(), snapshot); !errors.Is(err, sched.ErrRuntimeSnapshotCorrupt) {
		t.Fatalf("wrong size metadata error = %v, want ErrRuntimeSnapshotCorrupt", err)
	}
}

func TestStorageRuntimeSnapshotVerifierPreservesTransientAndCancellation(t *testing.T) {
	memory := []byte("mem")
	vmstate := []byte("state")
	snapshot := validStorageRuntimeSnapshot(memory, vmstate)
	transient := errors.New("object store unavailable")
	backend := &snapshotVerifyStorage{objects: map[string][]byte{
		snapshot.StorageKey: memory,
		sched.RuntimeSnapshotVMStateStorageKey(snapshot.StorageKey): vmstate,
	}, err: transient}
	verifier := sched.NewStorageRuntimeSnapshotVerifier(backend)
	if err := verifier.VerifyRuntimeSnapshot(context.Background(), snapshot); !errors.Is(err, transient) {
		t.Fatalf("transient error = %v, want wrapped storage error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend.err = nil
	if err := verifier.VerifyRuntimeSnapshot(ctx, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v, want context.Canceled", err)
	}
}

func TestRuntimeSnapshotDigestIsLengthDelimited(t *testing.T) {
	if sched.RuntimeSnapshotDigest([]byte("ab"), []byte("c")) == sched.RuntimeSnapshotDigest([]byte("a"), []byte("bc")) {
		t.Fatal("digest does not bind object boundaries")
	}
}

func TestRuntimeSnapshotCatalogRestoresOnlyVerifiedStoragePair(t *testing.T) {
	memory := []byte("mem")
	vmstate := []byte("state")
	entry := validStorageRuntimeSnapshot(memory, vmstate)
	backend := &snapshotVerifyStorage{objects: map[string][]byte{
		entry.StorageKey: memory,
		sched.RuntimeSnapshotVMStateStorageKey(entry.StorageKey): vmstate,
	}}
	index := sched.NewMemoryRuntimeSnapshotIndex()
	if err := index.Publish(entry); err != nil {
		t.Fatal(err)
	}
	catalog := sched.NewRuntimeSnapshotCatalog(index, sched.NewStorageRuntimeSnapshotVerifier(backend))
	plan, err := catalog.Resolve(context.Background(), sched.RuntimeSnapshotRequest{
		Shape:        api.ExecutionSnapshotShape{Runtime: api.ExecutionRuntimeNode22, MemoryMB: 128, EphemeralDiskMB: 64},
		Architecture: sched.ArchAMD64, KernelDigest: strings.Repeat("a", 64),
		GuestExecutorDigest: strings.Repeat("b", 64), BaseImageDigest: strings.Repeat("c", 64),
		FormatVersion: sched.CurrentRuntimeSnapshotFormatVersion,
	})
	if err != nil || plan.Mode != sched.RuntimeSnapshotRestore || plan.Snapshot == nil {
		t.Fatalf("catalog plan = %#v, err=%v", plan, err)
	}
	backend.objects[entry.StorageKey][0] ^= 1
	plan, err = catalog.Resolve(context.Background(), sched.RuntimeSnapshotRequest{
		Shape:        api.ExecutionSnapshotShape{Runtime: api.ExecutionRuntimeNode22, MemoryMB: 128, EphemeralDiskMB: 64},
		Architecture: sched.ArchAMD64, KernelDigest: strings.Repeat("a", 64),
		GuestExecutorDigest: strings.Repeat("b", 64), BaseImageDigest: strings.Repeat("c", 64),
		FormatVersion: sched.CurrentRuntimeSnapshotFormatVersion,
	})
	if err != nil || plan.Mode != sched.RuntimeSnapshotColdBoot || plan.FallbackReason != sched.RuntimeSnapshotFallbackCorrupt {
		t.Fatalf("tampered catalog plan = %#v, err=%v", plan, err)
	}
}

func validStorageRuntimeSnapshot(memory, vmstate []byte) sched.RuntimeSnapshot {
	identity := sched.RuntimeSnapshotIdentity{
		Runtime: api.ExecutionRuntimeNode22, Architecture: sched.ArchAMD64,
		KernelDigest: strings.Repeat("a", 64), GuestExecutorDigest: strings.Repeat("b", 64),
		BaseImageDigest: strings.Repeat("c", 64), MemoryMB: 128, EphemeralDiskMB: 64,
		FormatVersion: sched.CurrentRuntimeSnapshotFormatVersion,
	}
	return sched.RuntimeSnapshot{
		Identity: identity, StorageKey: "execution-snapshots/node22/mem",
		SnapshotDigest: sched.RuntimeSnapshotDigest(memory, vmstate), MemBytes: int64(len(memory)), VMStateBytes: int64(len(vmstate)),
		Sanitized: true, PayloadFree: true, State: sched.RuntimeSnapshotReady, CreatedAt: time.Unix(1, 0).UTC(),
	}
}

type snapshotVerifyStorage struct {
	objects map[string][]byte
	err     error
	gets    int
}

func (s *snapshotVerifyStorage) Put(context.Context, string, io.Reader) error { return nil }

func (s *snapshotVerifyStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.gets++
	if s.err != nil {
		return nil, s.err
	}
	value, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, key)
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}

func (s *snapshotVerifyStorage) Delete(context.Context, string) error { return nil }
