package storage

// adr: 20261115180000 — slice 2: exclusive retirement through the object GC.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type generationDeferrer struct {
	recordingDeferrer
	keys        []string
	generations []int64
}

func (g *generationDeferrer) DeferRetireGeneration(_ context.Context, key string, generation int64) error {
	g.keys, g.generations = append(g.keys, key), append(g.generations, generation)
	return nil
}

// A deferring node records the acknowledged generation and deletes nothing.
func TestRetireExclusiveArtifact_DeferredRecordsGeneration(t *testing.T) {
	b, s := receiptGCSFixture(t, snapshotCompressionNone)
	key := exclusiveCapturePrefix + "mem"
	r, err := PutExclusiveArtifact(t.Context(), b, key, strings.NewReader("memory"), 6)
	if err != nil {
		t.Fatal(err)
	}
	d := &generationDeferrer{}
	if n, err := DeferDeletes(b, d); err != nil || n != 1 {
		t.Fatalf("DeferDeletes = %d, %v", n, err)
	}
	if err := RetireExclusiveArtifact(t.Context(), b, r); err != nil {
		t.Fatalf("deferred retirement: %v", err)
	}
	if s.retirements != 0 || s.ordinaryDeletes != 0 {
		t.Fatalf("a deferring node deleted: retirements=%d deletes=%d", s.retirements, s.ordinaryDeletes)
	}
	if len(d.keys) != 1 || d.keys[0] != key || d.generations[0] != r.Generation || len(d.recordingDeferrer.keys) != 0 {
		t.Fatalf("recorded %v %v (plain %v), want %s at generation %d", d.keys, d.generations, d.recordingDeferrer.keys, key, r.Generation)
	}
	if _, ok := s.objects[b.bucket+"/"+key]; !ok {
		t.Fatal("object gone after a deferred retirement")
	}
}

// A deferrer that cannot record a generation refuses: it never falls back
// to an unconditional deletion request.
func TestRetireExclusiveArtifact_DeferredWithoutGenerationRefuses(t *testing.T) {
	b, s := receiptGCSFixture(t, snapshotCompressionNone)
	r, err := PutExclusiveArtifact(t.Context(), b, exclusiveCapturePrefix+"mem", strings.NewReader("memory"), 6)
	if err != nil {
		t.Fatal(err)
	}
	d := &recordingDeferrer{}
	if _, err := DeferDeletes(b, d); err != nil {
		t.Fatal(err)
	}
	if err := RetireExclusiveArtifact(t.Context(), b, r); !errors.Is(err, ErrExclusiveRetireUnsupported) {
		t.Fatalf("err = %v, want ErrExclusiveRetireUnsupported", err)
	}
	if len(d.keys) != 0 || s.retirements != 0 {
		t.Fatalf("recorded %v, retirements %d; want nothing", d.keys, s.retirements)
	}
}

// The control plane deletes exactly the recorded generation.
func TestDeleteGeneration(t *testing.T) {
	b, s := receiptGCSFixture(t, snapshotCompressionNone)
	key := exclusiveCapturePrefix + "vmstate"
	r, err := PutExclusiveArtifact(t.Context(), b, key, strings.NewReader("state"), 5)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := NewFallbackStorageBackend(b, &errorStorageBackend{})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewPrefixRouter(map[string]StorageBackend{"kernel/": &errorStorageBackend{}}, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteGeneration(t.Context(), router, key, r.Generation+1); !errors.Is(err, ErrArtifactReceiptMismatch) {
		t.Fatalf("other generation: err = %v, want ErrArtifactReceiptMismatch", err)
	}
	if _, ok := s.objects[b.bucket+"/"+key]; !ok {
		t.Fatal("a mismatched generation deleted the object")
	}
	if err := DeleteGeneration(t.Context(), router, key, r.Generation); err != nil {
		t.Fatalf("matching generation: %v", err)
	}
	if _, ok := s.objects[b.bucket+"/"+key]; ok {
		t.Fatal("the matching generation is still there")
	}
	if err := DeleteGeneration(t.Context(), router, key, r.Generation); err != nil {
		t.Fatalf("already gone: %v", err)
	}
	if s.ordinaryDeletes != 0 {
		t.Fatalf("ordinary deletes %d, want 0", s.ordinaryDeletes)
	}
	if err := DeleteGeneration(t.Context(), router, key, 0); err == nil {
		t.Fatal("generation 0 accepted")
	}
	d := &generationDeferrer{}
	if _, err := DeferDeletes(b, d); err != nil {
		t.Fatal(err)
	}
	if err := DeleteGeneration(t.Context(), b, key, r.Generation); err == nil {
		t.Fatal("a deferring backend deleted a generation")
	}
}
