// Package objectgc moves shared-store object deletion to the control plane
// (ADR-20261115180000). A compute node in defer mode records the objects it
// would delete (Deferrer); the control plane's Worker re-checks each request
// against the database and deletes only objects nothing references, with
// the only store credentials that may delete.
package objectgc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Deferrer records deletes of a node's shared store (storage.DeferDeletes).
type Deferrer struct {
	Requests state.StorageObjectDeletionRequester
	// Reason names the recording daemon in each request (imaged, vmmd).
	Reason string
}

// DeferDelete implements storage.DeleteDeferrer.
func (d Deferrer) DeferDelete(ctx context.Context, key string) error {
	return d.Requests.RequestStorageObjectDeletion(ctx, key, d.Reason)
}

// DeferRetireGeneration implements storage.GenerationRetireDeferrer
// (slice 2): the control plane deletes exactly this generation.
func (d Deferrer) DeferRetireGeneration(ctx context.Context, key string, generation int64) error {
	r, ok := d.Requests.(state.StorageObjectRetirementRequester)
	if !ok {
		return errors.New("objectgc: this store cannot record generation retirements")
	}
	return r.RequestStorageObjectRetirement(ctx, key, generation, d.Reason)
}

const (
	// DefaultInterval is how often the worker looks for due requests.
	DefaultInterval = time.Minute
	// DefaultBatch bounds one tick.
	DefaultBatch = 200
	// MaxReferencedAge is how long a request for a referenced object is
	// retried before it is refused.
	MaxReferencedAge = 7 * 24 * time.Hour
	retryBase        = 10 * time.Minute
	retryCap         = 24 * time.Hour
)

// Outcomes counted by faas_object_gc_requests_total.
const (
	OutcomeDeleted = "deleted"
	OutcomeRefused = "refused"
	OutcomeRetried = "retried"
	OutcomeError   = "error"
)

// Worker drains object deletion requests on the control plane.
type Worker struct {
	store    state.StorageObjectDeletionStore
	backend  storage.StorageBackend
	log      *slog.Logger
	requests *prometheus.CounterVec
	now      func() time.Time
	Interval time.Duration
	Batch    int
	// SweepInterval paces the jobs/ and sources/ sweep (WithSweep).
	SweepInterval time.Duration
	sweep         SweepStore
}

// NewWorker returns a worker that deletes through backend, which must hold
// delete rights and must not defer (storage.DeferDeletes). reg may be nil.
func NewWorker(store state.StorageObjectDeletionStore, backend storage.StorageBackend, reg prometheus.Registerer, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	w := &Worker{store: store, backend: backend, log: log, now: time.Now, Interval: DefaultInterval, Batch: DefaultBatch, SweepInterval: DefaultSweepInterval,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "faas_object_gc_requests_total",
			Help: "Object deletion requests the control plane's object GC decided or retried, by key class and outcome (ADR-20261115180000).",
		}, []string{"class", "outcome"})}
	for _, class := range []string{state.StorageObjectClassSnapshot, state.StorageObjectClassLayer, state.StorageObjectClassSource, state.StorageObjectClassPlatform, state.StorageObjectClassUnclassified} {
		for _, outcome := range []string{OutcomeDeleted, OutcomeRefused, OutcomeRetried, OutcomeError} {
			w.requests.WithLabelValues(class, outcome)
		}
	}
	if reg != nil {
		reg.MustRegister(w.requests)
	}
	return w
}

// Run ticks until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	var lastSweep time.Time
	for {
		if w.sweep != nil && w.now().Sub(lastSweep) >= w.SweepInterval {
			lastSweep = w.now()
			w.sweepOnce(ctx)
		}
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("objectgc: tick", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick decides every due request once.
func (w *Worker) Tick(ctx context.Context) error {
	now := w.now()
	due, err := w.store.DueStorageObjectDeletions(ctx, now, w.Batch)
	if err != nil {
		return fmt.Errorf("objectgc: due requests: %w", err)
	}
	var errs []error
	for _, d := range due {
		if err := w.decide(ctx, d, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *Worker) decide(ctx context.Context, d state.StorageObjectDeletion, now time.Time) error {
	referenced, class, err := w.store.StorageObjectReferenced(ctx, d.StorageKey)
	switch {
	case err != nil:
		return w.retry(ctx, d, class, now, fmt.Sprintf("reference check: %v", err))
	case class == state.StorageObjectClassPlatform || class == state.StorageObjectClassUnclassified:
		w.log.Warn("objectgc: refused a request for an object it never deletes", "key", d.StorageKey, "class", class, "requested_by", d.RequestedBy)
		return w.outcome(ctx, d, class, OutcomeRefused, "objects of class "+class+" are not deleted by request")
	case referenced && now.Sub(d.RequestedAt) >= MaxReferencedAge:
		w.log.Warn("objectgc: refused a request for a referenced object", "key", d.StorageKey, "class", class, "requested_by", d.RequestedBy)
		return w.outcome(ctx, d, class, OutcomeRefused, "still referenced after "+MaxReferencedAge.String())
	case referenced:
		return w.retry(ctx, d, class, now, "referenced")
	}
	if d.Generation > 0 {
		// An exclusive artifact's retirement: only the acknowledged
		// generation goes; a replacement under the key is left alone.
		err := storage.DeleteGeneration(ctx, w.backend, d.StorageKey, d.Generation)
		switch {
		case errors.Is(err, storage.ErrArtifactReceiptMismatch):
			w.log.Warn("objectgc: refused a retirement whose key holds another generation", "key", d.StorageKey, "generation", d.Generation, "requested_by", d.RequestedBy)
			return w.outcome(ctx, d, class, OutcomeRefused, "the key holds another generation now")
		case err != nil:
			return w.retry(ctx, d, class, now, fmt.Sprintf("delete generation: %v", err))
		}
		return w.outcome(ctx, d, class, OutcomeDeleted, "")
	}
	if err := w.backend.Delete(ctx, d.StorageKey); err != nil && !storage.IsNotFound(err) {
		return w.retry(ctx, d, class, now, fmt.Sprintf("delete: %v", err))
	}
	return w.outcome(ctx, d, class, OutcomeDeleted, "")
}

func (w *Worker) outcome(ctx context.Context, d state.StorageObjectDeletion, class, outcome, detail string) error {
	stateOutcome := state.StorageObjectDeleted
	if outcome == OutcomeRefused {
		stateOutcome = state.StorageObjectRefused
	}
	if err := w.store.DecideStorageObjectDeletion(ctx, d, stateOutcome, detail); err != nil {
		w.requests.WithLabelValues(class, OutcomeError).Inc()
		return fmt.Errorf("objectgc: record %s for %q: %w", outcome, d.StorageKey, err)
	}
	w.requests.WithLabelValues(class, outcome).Inc()
	return nil
}

func (w *Worker) retry(ctx context.Context, d state.StorageObjectDeletion, class string, now time.Time, detail string) error {
	if class == "" {
		class = state.StorageObjectClassUnclassified
	}
	if err := w.store.RetryStorageObjectDeletion(ctx, d, now.Add(retryDelay(d.Attempts)), detail); err != nil {
		w.requests.WithLabelValues(class, OutcomeError).Inc()
		return fmt.Errorf("objectgc: retry %q: %w", d.StorageKey, err)
	}
	w.requests.WithLabelValues(class, OutcomeRetried).Inc()
	return nil
}

// DeleteModeEnv selects how a compute daemon deletes shared-store objects:
// "direct" (default) deletes them, "defer" records requests the control
// plane decides (ADR-20261115180000).
const DeleteModeEnv = "FAAS_STORAGE_DELETE_MODE"

// DeferFromEnv applies DeleteModeEnv to backend. It reports whether deletes
// are now deferred. Defer mode refuses a backend without a shared store and
// a daemon without a database, so it never silently keeps deleting.
func DeferFromEnv(getenv func(string) string, backend storage.StorageBackend, requests state.StorageObjectDeletionRequester, reason string) (bool, error) {
	switch mode := getenv(DeleteModeEnv); mode {
	case "", "direct":
		return false, nil
	case "defer":
		if requests == nil {
			return false, fmt.Errorf("objectgc: %s=defer needs a database", DeleteModeEnv)
		}
		n, err := storage.DeferDeletes(backend, Deferrer{Requests: requests, Reason: reason})
		if err != nil {
			return false, fmt.Errorf("objectgc: %s=defer: %w", DeleteModeEnv, err)
		}
		if n == 0 {
			return false, fmt.Errorf("objectgc: %s=defer, but the storage backend has no shared store", DeleteModeEnv)
		}
		return true, nil
	default:
		return false, fmt.Errorf("objectgc: %s=%q: want direct or defer", DeleteModeEnv, mode)
	}
}

// retryDelay doubles from 10 minutes up to a day.
func retryDelay(attempts int) time.Duration {
	d := retryBase
	for i := 0; i < attempts && d < retryCap; i++ {
		d *= 2
	}
	return min(d, retryCap)
}
