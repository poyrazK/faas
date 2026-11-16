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
