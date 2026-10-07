package imaged

// ADR-054 / ADR-099: publication is fenced by the live materialization claim.
// A renewer discovering that successful publication released the claim must
// not turn that committed artifact into a failed materialization result.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicationRenewalRaceStore struct {
	*state.MemStore
	renewedAfterPublication chan struct{}
	once                    sync.Once
}

func (s *publicationRenewalRaceStore) JobRenewImageMaterializationLease(ctx context.Context, id, source, owner string, attempt int, lease time.Duration) error {
	err := s.MemStore.JobRenewImageMaterializationLease(ctx, id, source, owner, attempt, lease)
	job, readErr := s.MemStore.JobGetByID(context.WithoutCancel(ctx), id)
	if errors.Is(err, state.ErrConflict) && readErr == nil && job.ImageMaterializationStatus == "ready" && job.ImageRef == source && job.ImageMaterializationAttempts == attempt {
		s.once.Do(func() { close(s.renewedAfterPublication) })
	}
	return err
}

func (s *publicationRenewalRaceStore) JobPublishImageMaterialization(ctx context.Context, id, source, owner string, attempt int, digest, key string) (state.Job, error) {
	job, err := s.MemStore.JobPublishImageMaterialization(ctx, id, source, owner, attempt, digest, key)
	if err != nil {
		return job, err
	}
	// Force the real renewer to read the released claim before publication
	// returns to the caller, then wait for its cancellation to be recorded.
	select {
	case <-s.renewedAfterPublication:
	case <-ctx.Done():
		return state.Job{}, ctx.Err()
	}
	<-ctx.Done()
	return job, nil
}

func TestMaterializationCommittedPublicationSurvivesRenewalConflict(t *testing.T) {
	for _, mode := range []string{"single", "batch"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			store := &publicationRenewalRaceStore{MemStore: state.NewMemStore(), renewedAfterPublication: make(chan struct{})}
			acct, err := store.CreateAccount(ctx, "publication-race@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.JobCreate(ctx, acct.ID, "publication-race", "batch", "registry.example/worker:latest", []string{"/bin/worker"}, 256, 60, 2, 1, nil)
			if err != nil {
				t.Fatal(err)
			}
			puller := &jobMaterializationPuller{digest: "sha256:" + strings.Repeat("c", 64), manifest: oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:" + strings.Repeat("d", 64)}}}, blob: []byte("layer")}
			root := t.TempDir()
			backend := mustLocalStorage(t, root)
			var logs bytes.Buffer
			h := New(store, &fakeNotifier{}, puller, &fakeBuilder{bytesOut: 123}, "./guest-init", root, slog.New(slog.NewTextHandler(&logs, nil))).WithStorage(backend)
			h.jobMaterializationLeaseOverride = time.Second
			if mode == "single" {
				err = h.MaterializeJob(ctx, job.ID)
			} else {
				err = h.MaterializePendingJobs(ctx)
			}
			if err != nil {
				t.Fatalf("committed publication reported failure: %v", err)
			}
			if strings.Contains(logs.String(), "lease renewal failed") {
				t.Fatalf("committed publication logged failure: %s", logs.String())
			}
			ready, err := store.JobGetByID(ctx, job.ID)
			if err != nil || ready.ImageMaterializationStatus != "ready" || ready.ImageResolvedDigest != puller.digest || ready.ImageStorageKey == "" {
				t.Fatalf("publication lost: %+v %v", ready, err)
			}
			r, err := backend.Get(ctx, ready.ImageStorageKey)
			if err != nil {
				t.Fatalf("committed artifact removed: %v", err)
			}
			_ = r.Close()
		})
	}
}
