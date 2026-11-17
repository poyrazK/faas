package objectgc_test

// adr: 20261115180000

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectgc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type fakeStore struct {
	due        []state.StorageObjectDeletion
	referenced map[string]bool
	refErr     error
	decided    map[string]string
	retried    map[string]time.Time
}

func (f *fakeStore) DueStorageObjectDeletions(context.Context, time.Time, int) ([]state.StorageObjectDeletion, error) {
	return f.due, nil
}

func (f *fakeStore) DecideStorageObjectDeletion(_ context.Context, d state.StorageObjectDeletion, outcome, _ string) error {
	f.decided[d.StorageKey] = outcome
	return nil
}

func (f *fakeStore) RetryStorageObjectDeletion(_ context.Context, d state.StorageObjectDeletion, next time.Time, _ string) error {
	f.retried[d.StorageKey] = next
	return nil
}

func (f *fakeStore) StorageObjectReferenced(_ context.Context, key string) (bool, string, error) {
	class, _, _ := state.ClassifyStorageObject(key)
	if f.refErr != nil {
		return true, class, f.refErr
	}
	return f.referenced[key] || class == state.StorageObjectClassPlatform || class == state.StorageObjectClassUnclassified, class, nil
}

type fakeBackend struct {
	deleted []string
	err     error
}

func (b *fakeBackend) Put(context.Context, string, io.Reader) error { return nil }
func (b *fakeBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}
func (b *fakeBackend) Delete(_ context.Context, key string) error {
	if b.err != nil {
		return b.err
	}
	b.deleted = append(b.deleted, key)
	return nil
}

func TestWorker_DecidesEachRequest(t *testing.T) {
	now := time.Now()
	const (
		free     = "snap/d/captures/c1/v2/mem.age"
		live     = "snap/d/captures/c2/v2/mem"
		stale    = "apps/x/d.ext4"
		platform = "base/base-amd64.ext4"
		other    = "sbom/d.json"
	)
	store := &fakeStore{
		due: []state.StorageObjectDeletion{
			{StorageKey: free, RequestedAt: now.Add(-time.Hour)},
			{StorageKey: live, RequestedAt: now.Add(-time.Hour), Attempts: 2},
			{StorageKey: stale, RequestedAt: now.Add(-objectgc.MaxReferencedAge - time.Minute)},
			{StorageKey: platform, RequestedAt: now},
			{StorageKey: other, RequestedAt: now},
		},
		referenced: map[string]bool{live: true, stale: true},
		decided:    map[string]string{}, retried: map[string]time.Time{},
	}
	be := &fakeBackend{}
	if err := objectgc.NewWorker(store, be, nil, nil).Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(be.deleted) != 1 || be.deleted[0] != free {
		t.Fatalf("deleted %v, want only the unreferenced object", be.deleted)
	}
	want := map[string]string{free: state.StorageObjectDeleted, stale: state.StorageObjectRefused,
		platform: state.StorageObjectRefused, other: state.StorageObjectRefused}
	for key, outcome := range want {
		if store.decided[key] != outcome {
			t.Errorf("%s decided %q, want %q", key, store.decided[key], outcome)
		}
	}
	if next, ok := store.retried[live]; !ok || next.Sub(now) < 39*time.Minute {
		t.Fatalf("referenced object retried at %v, want backoff of 40 min after 2 attempts", next.Sub(now))
	}
	if _, ok := store.decided[live]; ok {
		t.Fatal("a referenced object was decided before it aged out")
	}
}

func TestWorker_RetriesFailures(t *testing.T) {
	key := "layers/d.ext4"
	for name, tc := range map[string]struct {
		refErr, delErr error
	}{
		"reference check fails": {refErr: errors.New("db down")},
		"delete fails":          {delErr: errors.New("403")},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{due: []state.StorageObjectDeletion{{StorageKey: key, RequestedAt: time.Now()}},
				refErr: tc.refErr, decided: map[string]string{}, retried: map[string]time.Time{}}
			be := &fakeBackend{err: tc.delErr}
			if err := objectgc.NewWorker(store, be, nil, nil).Tick(context.Background()); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if _, ok := store.retried[key]; !ok || len(store.decided) != 0 {
				t.Fatalf("retried=%v decided=%v, want a retry and no decision", store.retried, store.decided)
			}
		})
	}
}

// generationBackend deletes one generation: a key holding another
// generation refuses, a missing key is done.
type generationBackend struct {
	fakeBackend
	generations map[string]int64
	retired     []string
}

func (b *generationBackend) DeleteGeneration(_ context.Context, key string, generation int64) error {
	g, ok := b.generations[key]
	switch {
	case !ok:
		return nil
	case g != generation:
		return storage.ErrArtifactReceiptMismatch
	}
	delete(b.generations, key)
	b.retired = append(b.retired, key)
	return nil
}

// Slice 2: a retirement deletes only the generation it names, never the key.
func TestWorker_RetiresOnlyTheRecordedGeneration(t *testing.T) {
	now := time.Now()
	const (
		retire   = "snap/d/captures/c1/v2/mem"
		replaced = "snap/d/captures/c2/v2/mem"
		gone     = "snap/d/captures/c3/v2/mem"
		inUse    = "snap/d/captures/c4/v2/mem"
	)
	store := &fakeStore{
		due: []state.StorageObjectDeletion{
			{StorageKey: retire, RequestedAt: now, Generation: 7},
			{StorageKey: replaced, RequestedAt: now, Generation: 7},
			{StorageKey: gone, RequestedAt: now, Generation: 7},
			{StorageKey: inUse, RequestedAt: now, Generation: 7},
		},
		referenced: map[string]bool{inUse: true},
		decided:    map[string]string{}, retried: map[string]time.Time{},
	}
	be := &generationBackend{generations: map[string]int64{retire: 7, replaced: 8, inUse: 7}}
	if err := objectgc.NewWorker(store, be, nil, nil).Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(be.deleted) != 0 {
		t.Fatalf("unconditional deletes %v, want none for generation retirements", be.deleted)
	}
	if len(be.retired) != 1 || be.retired[0] != retire || be.generations[replaced] != 8 || be.generations[inUse] != 7 {
		t.Fatalf("retired %v, left %v; want only the matching unreferenced generation gone", be.retired, be.generations)
	}
	want := map[string]string{retire: state.StorageObjectDeleted, replaced: state.StorageObjectRefused, gone: state.StorageObjectDeleted}
	for key, outcome := range want {
		if store.decided[key] != outcome {
			t.Errorf("%s decided %q, want %q", key, store.decided[key], outcome)
		}
	}
	if _, ok := store.retried[inUse]; !ok {
		t.Fatal("a referenced retirement was not retried")
	}
}

// A backend that cannot delete one generation retries; it never falls back
// to deleting the whole key.
func TestWorker_GenerationRetirementNeverDeletesTheKey(t *testing.T) {
	key := "snap/d/captures/c1/v2/mem"
	store := &fakeStore{due: []state.StorageObjectDeletion{{StorageKey: key, RequestedAt: time.Now(), Generation: 3}},
		decided: map[string]string{}, retried: map[string]time.Time{}}
	be := &fakeBackend{}
	if err := objectgc.NewWorker(store, be, nil, nil).Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(be.deleted) != 0 || len(store.decided) != 0 {
		t.Fatalf("deleted %v decided %v, want neither", be.deleted, store.decided)
	}
	if _, ok := store.retried[key]; !ok {
		t.Fatal("want a retry")
	}
}

type recordingRetirer struct {
	recordingRequester
	generations []int64
}

func (r *recordingRetirer) RequestStorageObjectRetirement(_ context.Context, key string, generation int64, reason string) error {
	r.keys, r.reasons, r.generations = append(r.keys, key), append(r.reasons, reason), append(r.generations, generation)
	return nil
}

func TestDeferrer_DeferRetireGeneration(t *testing.T) {
	r := &recordingRetirer{}
	if err := (objectgc.Deferrer{Requests: r, Reason: "vmmd"}).DeferRetireGeneration(context.Background(), "k", 9); err != nil {
		t.Fatal(err)
	}
	if len(r.keys) != 1 || r.keys[0] != "k" || r.generations[0] != 9 || r.reasons[0] != "vmmd" {
		t.Fatalf("recorded %v %v %v", r.keys, r.generations, r.reasons)
	}
	if err := (objectgc.Deferrer{Requests: &recordingRequester{}}).DeferRetireGeneration(context.Background(), "k", 9); err == nil {
		t.Fatal("a store without generation retirements accepted one")
	}
}

type recordingRequester struct{ keys, reasons []string }

func (r *recordingRequester) RequestStorageObjectDeletion(_ context.Context, key, reason string) error {
	r.keys, r.reasons = append(r.keys, key), append(r.reasons, reason)
	return nil
}

func TestDeferFromEnv(t *testing.T) {
	remote, err := storage.NewOCIRegistryStorageBackend(storage.WithRegistry("https://registry.invalid/acme"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := func(v string) func(string) string { return func(string) string { return v } }
	req := &recordingRequester{}
	for name, tc := range map[string]struct {
		mode    string
		backend storage.StorageBackend
		req     state.StorageObjectDeletionRequester
		want    bool
		wantErr bool
	}{
		"unset":        {mode: "", backend: remote, req: req},
		"direct":       {mode: "direct", backend: remote, req: req},
		"defer":        {mode: "defer", backend: remote, req: req, want: true},
		"no store":     {mode: "defer", backend: local, req: req, wantErr: true},
		"no database":  {mode: "defer", backend: remote, wantErr: true},
		"unknown mode": {mode: "maybe", backend: remote, req: req, wantErr: true},
	} {
		got, err := objectgc.DeferFromEnv(env(tc.mode), tc.backend, tc.req, "imaged")
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("%s = %v, %v; want %v (error %v)", name, got, err, tc.want, tc.wantErr)
		}
	}
	key := "snap/0b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e6f/mem"
	if err := remote.Delete(context.Background(), key); err != nil || len(req.keys) != 1 || req.keys[0] != key || req.reasons[0] != "imaged" {
		t.Fatalf("deferred delete = %v, recorded %v %v", err, req.keys, req.reasons)
	}
}
