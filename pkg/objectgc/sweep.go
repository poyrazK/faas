package objectgc

// adr: 20261117190000 — the listing sweeps compute nodes lose with grants.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// DefaultSweepInterval is how often the control plane lists the swept
// prefixes.
const DefaultSweepInterval = 6 * time.Hour

// SweepStore is what the sweep reads, and the outbox it writes to.
type SweepStore interface {
	JobGetByID(ctx context.Context, id string) (state.Job, error)
	BuildByID(ctx context.Context, id string) (state.Build, error)
	RequestStorageObjectDeletion(ctx context.Context, key, reason string) error
}

// Sweep lists jobs/ and sources/ with the control plane's credentials and
// turns garbage into deletion requests, so the worker re-checks each one
// after the grace period. A node reaching the store through grants cannot
// list it, so imaged's job-artifact reconcile and builderd's source GC no
// longer run there. The rules are theirs:
//   - jobs/: an attempt of a deleted job, of a job whose materialization
//     failed, or a superseded attempt of a ready job.
//   - sources/: an archive whose UUIDv7 name is older than
//     state.SourceOrphanGrace and whose build is gone or finished.
//
// It returns how many requests it made.
func Sweep(ctx context.Context, lister storage.LocalArtifactLister, st SweepStore, now time.Time) (int, error) {
	var errs []error
	requested := 0
	request := func(key, why string) {
		if err := st.RequestStorageObjectDeletion(ctx, key, "objectgc sweep: "+why); err != nil {
			errs = append(errs, fmt.Errorf("request %s: %w", key, err))
			return
		}
		requested++
	}
	jobs, err := lister.List(ctx, "jobs/")
	if err != nil {
		return 0, fmt.Errorf("objectgc: sweep jobs/: %w", err)
	}
	for _, key := range jobs {
		jobID, ok := storagekey.JobLayerJobID(key)
		if !ok {
			continue
		}
		job, err := st.JobGetByID(ctx, jobID)
		switch {
		case errors.Is(err, state.ErrNotFound):
			request(key, "job gone")
		case err != nil:
			errs = append(errs, fmt.Errorf("job %s: %w", jobID, err))
		case job.ImageMaterializationStatus == "failed":
			request(key, "materialization failed")
		case job.ImageMaterializationStatus == "ready" && job.ImageStorageKey != "" && job.ImageStorageKey != key:
			request(key, "superseded attempt")
		}
	}
	sources, err := lister.List(ctx, "sources/")
	if err != nil {
		return requested, errors.Join(append(errs, fmt.Errorf("objectgc: sweep sources/: %w", err))...)
	}
	for _, key := range sources {
		name, ok := strings.CutSuffix(strings.TrimPrefix(key, "sources/"), ".tar.gz")
		id, perr := uuid.Parse(name)
		if !ok || perr != nil || id.Version() != 7 || id.String() != name {
			continue
		}
		sec, nsec := id.Time().UnixTime()
		if now.Sub(time.Unix(sec, nsec)) < state.SourceOrphanGrace {
			continue
		}
		build, err := st.BuildByID(ctx, name)
		switch {
		case errors.Is(err, state.ErrNotFound):
			request(key, "build gone")
		case err != nil:
			errs = append(errs, fmt.Errorf("build %s: %w", name, err))
		case build.Status != state.BuildQueued && build.Status != state.BuildRunning:
			request(key, "build finished")
		}
	}
	return requested, errors.Join(errs...)
}

// WithSweep makes Run also sweep jobs/ and sources/ every SweepInterval
// through the worker's backend, which must list.
func (w *Worker) WithSweep(st SweepStore) *Worker {
	w.sweep = st
	return w
}

func (w *Worker) sweepOnce(ctx context.Context) {
	lister, ok := w.backend.(storage.LocalArtifactLister)
	if !ok || w.sweep == nil {
		return
	}
	n, err := Sweep(ctx, lister, w.sweep, w.now())
	if err != nil && ctx.Err() == nil {
		w.log.Warn("objectgc: sweep", "err", err, "requested", n)
		return
	}
	w.log.Info("objectgc: sweep", "requested", n)
}
