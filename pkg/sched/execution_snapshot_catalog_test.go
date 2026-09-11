package sched

// adr: 171 — sanitized runtime snapshot identity, compatibility, and fallback invariants.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type runtimeSnapshotIndexFunc func(context.Context, string) (RuntimeSnapshot, error)

func (f runtimeSnapshotIndexFunc) LookupRuntimeSnapshot(ctx context.Context, key string) (RuntimeSnapshot, error) {
	return f(ctx, key)
}

type runtimeSnapshotVerifierFunc func(context.Context, RuntimeSnapshot) error

func (f runtimeSnapshotVerifierFunc) VerifyRuntimeSnapshot(ctx context.Context, snapshot RuntimeSnapshot) error {
	return f(ctx, snapshot)
}

func validRuntimeSnapshotIdentity() RuntimeSnapshotIdentity {
	return RuntimeSnapshotIdentity{
		Runtime:             api.ExecutionRuntimeNode22,
		Architecture:        archAMD64,
		KernelDigest:        strings.Repeat("a", 64),
		GuestExecutorDigest: strings.Repeat("b", 64),
		BaseImageDigest:     strings.Repeat("c", 64),
		MemoryMB:            128,
		EphemeralDiskMB:     64,
		FormatVersion:       CurrentRuntimeSnapshotFormatVersion,
	}
}

func validRuntimeSnapshot() RuntimeSnapshot {
	return RuntimeSnapshot{
		Identity:       validRuntimeSnapshotIdentity(),
		StorageKey:     "execution-snapshots/node22-amd64/snapshot.bin",
		SnapshotDigest: strings.Repeat("d", 64),
		MemBytes:       128 << 20,
		VMStateBytes:   4096,
		Sanitized:      true,
		PayloadFree:    true,
		State:          RuntimeSnapshotReady,
		CreatedAt:      time.Unix(1, 0).UTC(),
	}
}

func validRuntimeSnapshotRequest() RuntimeSnapshotRequest {
	identity := validRuntimeSnapshotIdentity()
	return RuntimeSnapshotRequest{
		Shape:               api.ExecutionSnapshotShape{Runtime: identity.Runtime, MemoryMB: identity.MemoryMB, EphemeralDiskMB: identity.EphemeralDiskMB},
		Architecture:        identity.Architecture,
		KernelDigest:        identity.KernelDigest,
		GuestExecutorDigest: identity.GuestExecutorDigest,
		BaseImageDigest:     identity.BaseImageDigest,
		FormatVersion:       identity.FormatVersion,
	}
}

func TestRuntimeSnapshotIdentityKeyIsCanonical(t *testing.T) {
	identity := validRuntimeSnapshotIdentity()
	key, err := identity.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	want := "execution-snapshots/v1/node22/amd64/memory-128/disk-64/kernel-" + strings.Repeat("a", 64) + "/executor-" + strings.Repeat("b", 64) + "/base-" + strings.Repeat("c", 64)
	if key != want {
		t.Fatalf("Key = %q, want %q", key, want)
	}
	if strings.Contains(key, "source") || strings.Contains(key, "input") || strings.Contains(key, "payload") {
		t.Fatalf("catalog key contains caller payload terminology: %q", key)
	}
}

func TestRuntimeSnapshotIdentityValidationRejectsAmbiguousValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RuntimeSnapshotIdentity)
	}{
		{name: "runtime", mutate: func(identity *RuntimeSnapshotIdentity) { identity.Runtime = "go" }},
		{name: "architecture", mutate: func(identity *RuntimeSnapshotIdentity) { identity.Architecture = "riscv64" }},
		{name: "uppercase digest", mutate: func(identity *RuntimeSnapshotIdentity) { identity.KernelDigest = strings.Repeat("A", 64) }},
		{name: "short digest", mutate: func(identity *RuntimeSnapshotIdentity) { identity.BaseImageDigest = "abc" }},
		{name: "memory shape", mutate: func(identity *RuntimeSnapshotIdentity) { identity.MemoryMB = 192 }},
		{name: "disk shape", mutate: func(identity *RuntimeSnapshotIdentity) { identity.EphemeralDiskMB = 96 }},
		{name: "format", mutate: func(identity *RuntimeSnapshotIdentity) { identity.FormatVersion = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := validRuntimeSnapshotIdentity()
			test.mutate(&identity)
			if err := identity.Validate(); !errors.Is(err, ErrRuntimeSnapshotInvalid) {
				t.Fatalf("Validate = %v, want ErrRuntimeSnapshotInvalid", err)
			}
		})
	}
}

func TestMemoryRuntimeSnapshotIndexPublishesImmutably(t *testing.T) {
	index := NewMemoryRuntimeSnapshotIndex()
	entry := validRuntimeSnapshot()
	if err := index.Publish(entry); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	key, _ := entry.Identity.Key()
	got, err := index.LookupRuntimeSnapshot(context.Background(), key)
	if err != nil {
		t.Fatalf("LookupRuntimeSnapshot: %v", err)
	}
	if got != entry {
		t.Fatalf("lookup = %#v, want %#v", got, entry)
	}
	if err := index.Publish(entry); !errors.Is(err, ErrRuntimeSnapshotConflict) {
		t.Fatalf("duplicate Publish = %v, want ErrRuntimeSnapshotConflict", err)
	}
	entry.StorageKey = "execution-snapshots/relocated.bin"
	if err := index.Publish(entry); !errors.Is(err, ErrRuntimeSnapshotConflict) {
		t.Fatalf("relocation Publish = %v, want ErrRuntimeSnapshotConflict", err)
	}
}

func TestMemoryRuntimeSnapshotIndexRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RuntimeSnapshot)
	}{
		{name: "payload-bearing", mutate: func(entry *RuntimeSnapshot) { entry.PayloadFree = false }},
		{name: "not sanitized", mutate: func(entry *RuntimeSnapshot) { entry.Sanitized = false }},
		{name: "unsafe storage path", mutate: func(entry *RuntimeSnapshot) { entry.StorageKey = "../tenant-source" }},
		{name: "bad artifact digest", mutate: func(entry *RuntimeSnapshot) { entry.SnapshotDigest = strings.Repeat("z", 64) }},
		{name: "zero memory bytes", mutate: func(entry *RuntimeSnapshot) { entry.MemBytes = 0 }},
		{name: "unknown state", mutate: func(entry *RuntimeSnapshot) { entry.State = "ready-ish" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := validRuntimeSnapshot()
			test.mutate(&entry)
			if err := NewMemoryRuntimeSnapshotIndex().Publish(entry); !errors.Is(err, ErrRuntimeSnapshotInvalid) {
				t.Fatalf("Publish = %v, want ErrRuntimeSnapshotInvalid", err)
			}
		})
	}
}

func TestRuntimeSnapshotCatalogResolvesCompatibleSnapshot(t *testing.T) {
	entry := validRuntimeSnapshot()
	request := validRuntimeSnapshotRequest()
	var lookedUpKey string
	var verified bool
	catalog := NewRuntimeSnapshotCatalog(
		runtimeSnapshotIndexFunc(func(_ context.Context, key string) (RuntimeSnapshot, error) {
			lookedUpKey = key
			return entry, nil
		}),
		runtimeSnapshotVerifierFunc(func(_ context.Context, snapshot RuntimeSnapshot) error {
			verified = snapshot.SnapshotDigest == entry.SnapshotDigest
			return nil
		}),
	)

	plan, err := catalog.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	key, _ := entry.Identity.Key()
	if plan.Mode != RuntimeSnapshotRestore || plan.FallbackReason != "" || plan.Snapshot == nil || lookedUpKey != key || !verified {
		t.Fatalf("plan = %#v, lookup=%q verified=%v", plan, lookedUpKey, verified)
	}
	if plan.Identity != request.Identity() {
		t.Fatalf("plan identity = %#v, want %#v", plan.Identity, request.Identity())
	}
	plan.Snapshot.StorageKey = "caller-mutated-copy"
	stored, _ := catalog.index.LookupRuntimeSnapshot(context.Background(), key)
	if stored.StorageKey == "caller-mutated-copy" {
		t.Fatal("resolver exposed mutable catalog entry")
	}
}

func TestRuntimeSnapshotCatalogFallsBackSafely(t *testing.T) {
	request := validRuntimeSnapshotRequest()
	tests := []struct {
		name       string
		index      RuntimeSnapshotIndex
		verifier   RuntimeSnapshotVerifier
		wantReason RuntimeSnapshotFallbackReason
		wantErr    error
	}{
		{name: "catalog unwired", wantReason: RuntimeSnapshotFallbackUnwired},
		{name: "missing", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) {
			return RuntimeSnapshot{}, ErrRuntimeSnapshotNotFound
		}), wantReason: RuntimeSnapshotFallbackMissing},
		{name: "index marks corrupt", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) {
			return RuntimeSnapshot{}, ErrRuntimeSnapshotCorrupt
		}), wantReason: RuntimeSnapshotFallbackCorrupt},
		{name: "metadata corrupt", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) {
			entry := validRuntimeSnapshot()
			entry.PayloadFree = false
			return entry, nil
		}), wantReason: RuntimeSnapshotFallbackCorrupt},
		{name: "incompatible", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) {
			entry := validRuntimeSnapshot()
			entry.Identity.BaseImageDigest = strings.Repeat("e", 64)
			return entry, nil
		}), wantReason: RuntimeSnapshotFallbackIncompatible},
		{name: "retired", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) {
			entry := validRuntimeSnapshot()
			entry.State = RuntimeSnapshotRetired
			return entry, nil
		}), wantReason: RuntimeSnapshotFallbackRetired},
		{name: "artifact corrupt", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) { return validRuntimeSnapshot(), nil }), verifier: runtimeSnapshotVerifierFunc(func(context.Context, RuntimeSnapshot) error { return ErrRuntimeSnapshotCorrupt }), wantReason: RuntimeSnapshotFallbackCorrupt},
		{name: "verifier unwired", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) { return validRuntimeSnapshot(), nil }), wantReason: RuntimeSnapshotFallbackUnwired},
		{name: "index unavailable", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) {
			return RuntimeSnapshot{}, errors.New("object store unavailable")
		}), wantErr: errors.New("object store unavailable")},
		{name: "verifier unavailable", index: runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) { return validRuntimeSnapshot(), nil }), verifier: runtimeSnapshotVerifierFunc(func(context.Context, RuntimeSnapshot) error { return errors.New("digest service unavailable") }), wantErr: errors.New("digest service unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := NewRuntimeSnapshotCatalog(test.index, test.verifier)
			plan, err := catalog.Resolve(context.Background(), request)
			if test.wantErr != nil {
				if err == nil || !strings.Contains(err.Error(), test.wantErr.Error()) {
					t.Fatalf("Resolve error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || plan.Mode != RuntimeSnapshotColdBoot || plan.FallbackReason != test.wantReason || plan.Snapshot != nil {
				t.Fatalf("plan = %#v, err=%v", plan, err)
			}
		})
	}
}

func TestRuntimeSnapshotCatalogRejectsUnsupportedRequestFormat(t *testing.T) {
	request := validRuntimeSnapshotRequest()
	request.FormatVersion = CurrentRuntimeSnapshotFormatVersion + 1
	catalog := NewRuntimeSnapshotCatalog(NewMemoryRuntimeSnapshotIndex(), nil)
	if _, err := catalog.Resolve(context.Background(), request); !errors.Is(err, ErrRuntimeSnapshotUnsupported) {
		t.Fatalf("Resolve = %v, want ErrRuntimeSnapshotUnsupported", err)
	}
}

func TestMemoryRuntimeSnapshotIndexConcurrentPublication(t *testing.T) {
	index := NewMemoryRuntimeSnapshotIndex()
	entry := validRuntimeSnapshot()
	const publishers = 16
	var wg sync.WaitGroup
	results := make(chan error, publishers)
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- index.Publish(entry)
		}()
	}
	wg.Wait()
	close(results)
	won, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrRuntimeSnapshotConflict):
			conflicts++
		default:
			t.Fatalf("concurrent Publish = %v", err)
		}
	}
	if won != 1 || conflicts != publishers-1 {
		t.Fatalf("publishers won=%d conflicts=%d, want 1/%d", won, conflicts, publishers-1)
	}
}

func TestRuntimeSnapshotCatalogPreservesContextOnLookup(t *testing.T) {
	request := validRuntimeSnapshotRequest()
	cancelled := errors.New("context marker")
	catalog := NewRuntimeSnapshotCatalog(runtimeSnapshotIndexFunc(func(ctx context.Context, _ string) (RuntimeSnapshot, error) {
		if ctx.Err() != nil {
			return RuntimeSnapshot{}, ctx.Err()
		}
		return RuntimeSnapshot{}, cancelled
	}), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := catalog.Resolve(ctx, request)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve error = %v, want context.Canceled", err)
	}
}

func TestRuntimeSnapshotPlanColdBootKeepsIdentity(t *testing.T) {
	request := validRuntimeSnapshotRequest()
	catalog := NewRuntimeSnapshotCatalog(nil, nil)
	plan, err := catalog.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Mode != RuntimeSnapshotColdBoot || plan.Identity != request.Identity() {
		t.Fatalf("cold-boot plan = %#v", plan)
	}
}

func TestRuntimeSnapshotEntryValidateRequiresCanonicalStorageKey(t *testing.T) {
	for _, key := range []string{" /leading-space", "/absolute", "a//b", "a/../b", "a\\b", "a\x00b"} {
		entry := validRuntimeSnapshot()
		entry.StorageKey = key
		if err := entry.Validate(); !errors.Is(err, ErrRuntimeSnapshotInvalid) {
			t.Errorf("StorageKey %q: Validate = %v, want ErrRuntimeSnapshotInvalid", key, err)
		}
	}
}

func TestRuntimeSnapshotVerifierGetsValidatedEntryOnly(t *testing.T) {
	entry := validRuntimeSnapshot()
	request := validRuntimeSnapshotRequest()
	var got RuntimeSnapshot
	catalog := NewRuntimeSnapshotCatalog(runtimeSnapshotIndexFunc(func(context.Context, string) (RuntimeSnapshot, error) {
		return entry, nil
	}), runtimeSnapshotVerifierFunc(func(_ context.Context, snapshot RuntimeSnapshot) error {
		got = snapshot
		return nil
	}))
	if _, err := catalog.Resolve(context.Background(), request); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != entry || !got.Sanitized || !got.PayloadFree || got.State != RuntimeSnapshotReady {
		t.Fatalf("verifier entry = %#v", got)
	}
}

func TestRuntimeSnapshotCatalogNilReceiverFallsBackAfterIdentityValidation(t *testing.T) {
	request := validRuntimeSnapshotRequest()
	plan, err := (*RuntimeSnapshotCatalog)(nil).Resolve(context.Background(), request)
	if err != nil || plan.Mode != RuntimeSnapshotColdBoot || plan.FallbackReason != RuntimeSnapshotFallbackUnwired {
		t.Fatalf("nil receiver Resolve = %#v, err=%v", plan, err)
	}
}

func TestRuntimeSnapshotCatalogDoesNotFallbackOnMalformedRequest(t *testing.T) {
	request := validRuntimeSnapshotRequest()
	request.KernelDigest = "not-a-digest"
	catalog := NewRuntimeSnapshotCatalog(nil, nil)
	if _, err := catalog.Resolve(context.Background(), request); !errors.Is(err, ErrRuntimeSnapshotInvalid) {
		t.Fatalf("Resolve = %v, want ErrRuntimeSnapshotInvalid", err)
	}
}

func TestRuntimeSnapshotErrorsRemainSentinels(t *testing.T) {
	if !errors.Is(fmt.Errorf("lookup: %w", ErrRuntimeSnapshotNotFound), ErrRuntimeSnapshotNotFound) {
		t.Fatal("ErrRuntimeSnapshotNotFound lost errors.Is identity")
	}
}
