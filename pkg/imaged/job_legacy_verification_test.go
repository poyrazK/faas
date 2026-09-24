package imaged

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type legacyArtifactDecision struct {
	found  bool
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

func (s *legacyVerificationTestStore) JobFinishLegacyArtifactVerification(_ context.Context, _, _, _ string, found bool, reason string) (state.Job, error) {
	s.finished = append(s.finished, legacyArtifactDecision{found: found, reason: reason})
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
		{name: "unsupported", wrap: func(b storage.StorageBackend) storage.StorageBackend { return legacyNoProbeBackend{b} }, wantRetry: "does not support"},
		{name: "probe error", wrap: func(b storage.StorageBackend) storage.StorageBackend { return legacyProbeErrorBackend{b} }, wantRetry: "remote storage unavailable"},
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
			if !tc.wantReady && store.finished[0].reason == "" {
				t.Fatal("missing artifact did not record a reason")
			}
		})
	}
}

// Keep the no-probe wrapper honest: it must only expose StorageBackend's
// three methods, not accidentally promote its delegate's Exists capability.
var _ storage.StorageBackend = legacyNoProbeBackend{}
