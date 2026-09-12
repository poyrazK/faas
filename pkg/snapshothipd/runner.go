package snapshothipd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const (
	// DefaultInterval keeps queue wait below the 200 ms prepositioned-wake
	// budget. Reconciliation is event-cursor based, so this tighter cadence
	// does not rescan the complete snapshots table.
	DefaultInterval = 100 * time.Millisecond
	// DefaultMaxPerTick avoids making a registry outage or a large snapshot
	// backlog monopolise a schedd process.
	DefaultMaxPerTick = 4
	// DefaultLeaseRenewInterval is comfortably below the five-minute reclaim
	// timeout used by the durable replica queue. A long storage read therefore
	// keeps its claim while still allowing a crashed worker to be reclaimed.
	DefaultLeaseRenewInterval = time.Minute
)

// Runner reconciles the local node's snapshot cache. It is deliberately
// node-local and is hosted by vmmd: all coordination is through the database
// and shared storage, so no private IP, SSH path, or provider-specific API is
// needed.
type Runner struct {
	store              state.SnapshotReplicaStore
	backend            storage.StorageBackend
	nodeID             string
	log                *slog.Logger
	metrics            Metrics
	interval           time.Duration
	maxTick            int
	leaseRenewInterval time.Duration
}

func New(store state.SnapshotReplicaStore, backend storage.StorageBackend, nodeID string, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Runner{
		store:              store,
		backend:            backend,
		nodeID:             nodeID,
		log:                log,
		interval:           DefaultInterval,
		maxTick:            DefaultMaxPerTick,
		leaseRenewInterval: DefaultLeaseRenewInterval,
	}
}

func (r *Runner) WithMetrics(metrics Metrics) *Runner {
	r.metrics = metrics
	return r
}

func (r *Runner) WithInterval(interval time.Duration) *Runner {
	if interval > 0 {
		r.interval = interval
	}
	return r
}

// Interval returns the effective reconciliation cadence for startup logs and
// operator tests.
func (r *Runner) Interval() time.Duration {
	if r == nil || r.interval <= 0 {
		return DefaultInterval
	}
	return r.interval
}

func (r *Runner) WithMaxPerTick(max int) *Runner {
	if max > 0 {
		r.maxTick = max
	}
	return r
}

// WithLeaseRenewInterval changes the lease heartbeat cadence. It is mainly
// useful for fast-running tests; production should keep the default well
// below the queue's reclaim timeout.
func (r *Runner) WithLeaseRenewInterval(interval time.Duration) *Runner {
	if interval > 0 {
		r.leaseRenewInterval = interval
	}
	return r
}

// Run performs one reconciliation immediately, then keeps the local cache
// current until ctx is cancelled. Each reconciliation consumes the durable
// event cursor, rather than scanning the complete snapshots table. A tick
// error is logged and does not stop the daemon; the next tick retries.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	r.runTick(ctx)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			r.runTick(ctx)
		}
	}
}

func (r *Runner) validate() error {
	switch {
	case r == nil:
		return errors.New("snapshothipd: nil runner")
	case r.store == nil:
		return errors.New("snapshothipd: snapshot replica store is required")
	case r.backend == nil:
		return errors.New("snapshothipd: storage backend is required")
	case r.nodeID == "":
		return errors.New("snapshothipd: node_id is required")
	default:
		return nil
	}
}

func (r *Runner) runTick(ctx context.Context) {
	r.reconcile(ctx)
	r.runWorkTick(ctx)
}

func (r *Runner) reconcile(ctx context.Context) {
	added, err := r.store.EnqueueSnapshotReplicasForNode(ctx, r.nodeID)
	if err != nil {
		r.log.Warn("snapshothipd: reconcile failed", "node_id", r.nodeID, "err", err)
		return
	}
	if added > 0 {
		r.log.Info("snapshothipd: snapshot replica jobs enqueued", "node_id", r.nodeID, "count", added)
	}
}

func (r *Runner) runWorkTick(ctx context.Context) {
	for i := 0; i < r.maxTick; i++ {
		if ctx.Err() != nil {
			return
		}
		job, err := r.store.ClaimSnapshotReplica(ctx, r.nodeID)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			r.log.Warn("snapshothipd: claim failed", "node_id", r.nodeID, "err", err)
			return
		}
		if err := r.syncJob(ctx, job); err != nil {
			if storage.IsNotFound(err) {
				err = state.PermanentSnapshotReplicaError(err)
			}
			r.metricsObserve("failed", job.Region)
			if markErr := r.markReplicaFailed(ctx, job, err); markErr != nil {
				r.log.Warn("snapshothipd: mark failed", "snapshot_id", job.SnapshotID, "node_id", job.NodeID, "err", markErr)
			}
			r.log.Warn("snapshothipd: snapshot preposition failed", "snapshot_id", job.SnapshotID, "deployment_id", job.DeploymentID, "node_id", job.NodeID, "attempt", job.Attempts, "err", err)
			continue
		}
		if err := r.markReplicaReady(ctx, job); err != nil {
			r.metricsObserve("failed", job.Region)
			r.log.Warn("snapshothipd: mark ready failed", "snapshot_id", job.SnapshotID, "node_id", job.NodeID, "err", err)
			continue
		}
		r.metricsObserve("ready", job.Region)
		if !job.QueuedAt.IsZero() {
			if latency := time.Since(job.QueuedAt); latency >= 0 {
				r.metricsObserveLatency(job.Region, latency)
			}
		}
		r.log.Debug("snapshothipd: snapshot prepositioned", "snapshot_id", job.SnapshotID, "deployment_id", job.DeploymentID, "node_id", job.NodeID, "attempt", job.Attempts)
	}
}

func (r *Runner) syncJob(ctx context.Context, job state.SnapshotReplicaJob) error {
	leased, ok := r.store.(state.SnapshotReplicaLeaseStore)
	if !ok || job.LeaseToken == "" {
		return syncJob(ctx, r.backend, job)
	}
	return syncJobWithLease(ctx, leased, r.backend, job, r.leaseRenewInterval)
}

func syncJobWithLease(ctx context.Context, store state.SnapshotReplicaLeaseStore, backend storage.StorageBackend, job state.SnapshotReplicaJob, renewInterval time.Duration) error {
	if renewInterval <= 0 {
		return syncJob(ctx, backend, job)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	renewErr := make(chan error, 1)
	stop := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(renewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-workCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(workCtx, renewInterval)
				err := store.RenewSnapshotReplicaLease(renewCtx, job.SnapshotID, job.NodeID, job.LeaseToken)
				renewCancel()
				if err == nil {
					continue
				}
				select {
				case renewErr <- err:
				default:
				}
				cancel()
				return
			}
		}
	}()

	syncErr := syncJob(workCtx, backend, job)
	close(stop)
	<-heartbeatDone
	select {
	case err := <-renewErr:
		return fmt.Errorf("snapshothipd: snapshot replica lease renewal failed: %w", err)
	default:
		return syncErr
	}
}

func (r *Runner) markReplicaReady(ctx context.Context, job state.SnapshotReplicaJob) error {
	if leased, ok := r.store.(state.SnapshotReplicaLeaseStore); ok {
		return leased.MarkSnapshotReplicaReadyWithLease(ctx, job.SnapshotID, job.NodeID, job.LeaseToken)
	}
	return r.store.MarkSnapshotReplicaReady(ctx, job.SnapshotID, job.NodeID)
}

func (r *Runner) markReplicaFailed(ctx context.Context, job state.SnapshotReplicaJob, cause error) error {
	if leased, ok := r.store.(state.SnapshotReplicaLeaseStore); ok {
		return leased.MarkSnapshotReplicaFailedWithLease(ctx, job.SnapshotID, job.NodeID, job.LeaseToken, cause)
	}
	return r.store.MarkSnapshotReplicaFailed(ctx, job.SnapshotID, job.NodeID, cause)
}

func (r *Runner) metricsObserve(outcome, region string) {
	if r.metrics != nil {
		r.metrics.ObserveFanout(outcome, region)
	}
}

func (r *Runner) metricsObserveLatency(region string, latency time.Duration) {
	if metrics, ok := r.metrics.(interface {
		ObserveFanoutLatency(region string, latency time.Duration)
	}); ok {
		metrics.ObserveFanoutLatency(region, latency)
	}
}

func syncJob(ctx context.Context, backend storage.StorageBackend, job state.SnapshotReplicaJob) error {
	if job.StorageKey == "" || job.VMStateStorageKey == "" {
		return errors.New("snapshothipd: snapshot replica has incomplete storage keys")
	}
	keys := make([]string, 0, 2+len(job.LayerStorageKeys))
	keys = append(keys, job.StorageKey, job.VMStateStorageKey)
	keys = append(keys, job.LayerStorageKeys...)
	for _, key := range keys {
		if key == "" {
			return errors.New("snapshothipd: snapshot replica has an empty dependency key")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if resolver, ok := backend.(storage.LocalPathResolver); ok {
			if _, local, err := resolver.LocalPath(key); err != nil {
				return fmt.Errorf("resolve %q: %w", key, err)
			} else if local {
				continue
			}
		}
		rc, err := backend.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("get %q: %w", key, err)
		}
		_, copyErr := io.Copy(io.Discard, rc)
		closeErr := rc.Close()
		if copyErr != nil {
			return fmt.Errorf("read %q: %w", key, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %q: %w", key, closeErr)
		}
	}
	return nil
}
