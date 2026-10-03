package imaged

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type legacyArtifactDecision struct {
	found  bool
	key    string
	reason string
}

type legacyVerificationTestStore struct {
	*state.MemStore
	jobs     []state.Job
	claimed  bool
	finished []legacyArtifactDecision
	retried  []string
	claimErr error
}

func (s *legacyVerificationTestStore) JobClaimLegacyArtifactVerification(context.Context, int, string, time.Duration) ([]state.Job, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if s.claimed {
		return nil, nil
	}
	s.claimed = true
	return s.jobs, nil
}

func (s *legacyVerificationTestStore) JobFinishLegacyArtifactVerification(_ context.Context, _, _, _, key string, found bool, reason string) (state.Job, error) {
	s.finished = append(s.finished, legacyArtifactDecision{found: found, key: key, reason: reason})
	return state.Job{}, nil
}

func (s *legacyVerificationTestStore) JobRetryLegacyArtifactVerification(_ context.Context, _, _, _, reason string, _ time.Time) (state.Job, error) {
	s.retried = append(s.retried, reason)
	return state.Job{}, nil
}

type legacyNoProbeBackend struct{ storage.StorageBackend }

type legacyProbeErrorBackend struct{ storage.StorageBackend }

func (legacyProbeErrorBackend) Exists(context.Context, string) (bool, error) {
	return false, errors.New("remote storage unavailable")
}

type legacyReadErrorBackend struct{ storage.StorageBackend }

func (b legacyReadErrorBackend) Exists(ctx context.Context, key string) (bool, error) {
	return b.StorageBackend.(storage.ExistenceChecker).Exists(ctx, key)
}

func (legacyReadErrorBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("legacy source read unavailable")
}

type legacyCopyErrorBackend struct{ storage.StorageBackend }

func (b legacyCopyErrorBackend) Exists(ctx context.Context, key string) (bool, error) {
	return b.StorageBackend.(storage.ExistenceChecker).Exists(ctx, key)
}

func (legacyCopyErrorBackend) Put(context.Context, string, io.Reader) error {
	return errors.New("job artifact upload unavailable")
}

type legacyDisappearingBackend struct{ storage.StorageBackend }

func (legacyDisappearingBackend) Exists(context.Context, string) (bool, error) {
	return true, nil
}

func (legacyDisappearingBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}

func TestVerifyLegacyJobArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		present   bool
		wrap      func(storage.StorageBackend) storage.StorageBackend
		wantReady bool
		wantRetry string
	}{
		{name: "present", present: true, wantReady: true},
		{name: "missing", wantReady: false},
		{name: "no existence probe", present: true, wrap: func(b storage.StorageBackend) storage.StorageBackend { return legacyNoProbeBackend{b} }, wantRetry: "does not support"},
		{name: "probe error", wrap: func(b storage.StorageBackend) storage.StorageBackend { return legacyProbeErrorBackend{b} }, wantRetry: "remote storage unavailable"},
		{name: "disappeared after probe", present: true, wrap: func(b storage.StorageBackend) storage.StorageBackend { return legacyDisappearingBackend{b} }},
		{name: "read error", present: true, wrap: func(b storage.StorageBackend) storage.StorageBackend { return legacyReadErrorBackend{b} }, wantRetry: "legacy source read unavailable"},
		{name: "copy error", present: true, wrap: func(b storage.StorageBackend) storage.StorageBackend { return legacyCopyErrorBackend{b} }, wantRetry: "job artifact upload unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			const ref = "apps/legacy-job/artifact.ext4"
			backend := mustLocalStorage(t, t.TempDir())
			if tc.present {
				if err := backend.Put(ctx, ref, strings.NewReader("ext4 contents")); err != nil {
					t.Fatal(err)
				}
			}
			var selected storage.StorageBackend = backend
			if tc.wrap != nil {
				selected = tc.wrap(backend)
			}
			store := &legacyVerificationTestStore{
				MemStore: state.NewMemStore(),
				jobs:     []state.Job{{ID: "legacy-job", ImageRef: ref, ImageStorageKey: ref, ImageMaterializationStatus: "verifying_legacy", ImageMaterializationAttempts: 1}},
			}
			h := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger()).WithStorage(selected)
			if err := h.MaterializePendingJobs(ctx); err != nil {
				t.Fatalf("MaterializePendingJobs: %v", err)
			}
			if tc.wantRetry != "" {
				if len(store.finished) != 0 || len(store.retried) != 1 || !strings.Contains(store.retried[0], tc.wantRetry) {
					t.Fatalf("finished=%+v retried=%+v, want retry containing %q", store.finished, store.retried, tc.wantRetry)
				}
				return
			}
			if len(store.retried) != 0 || len(store.finished) != 1 || store.finished[0].found != tc.wantReady {
				t.Fatalf("finished=%+v retried=%+v, want found=%v", store.finished, store.retried, tc.wantReady)
			}
			if tc.wantReady {
				const promoted = "jobs/legacy-job.ext4"
				if store.finished[0].key != promoted {
					t.Fatalf("published key = %q, want %q", store.finished[0].key, promoted)
				}
				copy, err := backend.Get(ctx, promoted)
				if err != nil {
					t.Fatalf("read promoted artifact: %v", err)
				}
				buf, err := io.ReadAll(copy)
				_ = copy.Close()
				if err != nil || string(buf) != "ext4 contents" {
					t.Fatalf("promoted artifact = %q, %v", buf, err)
				}
			}
			if !tc.wantReady && store.finished[0].reason == "" {
				t.Fatal("missing artifact did not record a reason")
			}
			if !tc.wantReady && store.finished[0].key != "" {
				t.Fatalf("missing artifact published key %q", store.finished[0].key)
			}
		})
	}
}

// Keep the no-probe wrapper honest: it must only expose StorageBackend's
// three methods, not accidentally promote its delegate's Exists capability.
var _ storage.StorageBackend = legacyNoProbeBackend{}
