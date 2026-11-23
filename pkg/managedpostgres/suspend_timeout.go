package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
)

// Scale-to-zero suspend timeout: how long a scale-to-zero database's compute
// stays up after its last activity. Every wake pays at least this much
// compute, so it is an operator setting (managed_postgres_policy
// suspend_timeout_seconds), applied live to new databases and compute policy
// changes, and converged onto existing databases by SuspendTimeoutSweeper.
const (
	DefaultSuspendTimeoutSeconds int64 = 120
	MinSuspendTimeoutSeconds     int64 = 60
	MaxSuspendTimeoutSeconds     int64 = 7 * 24 * 60 * 60
)

func validSuspendTimeout(seconds int64) bool {
	return seconds >= MinSuspendTimeoutSeconds && seconds <= MaxSuspendTimeoutSeconds
}

// SuspendTimeoutSetter is a provider whose scale-to-zero suspend timeout the
// registry sets: at startup and after every applied live policy.
type SuspendTimeoutSetter interface {
	SetSuspendTimeout(seconds int64)
}

// SuspendTimeoutResult is what one convergence step did to a database.
type SuspendTimeoutResult int

const (
	// SuspendTimeoutConverged: the database already has the timeout, or is
	// always-on and has none.
	SuspendTimeoutConverged SuspendTimeoutResult = iota
	// SuspendTimeoutChanged: the timeout was written.
	SuspendTimeoutChanged
	// SuspendTimeoutDeferred: the compute is running, held or changing; a
	// later sweep tries again. Only a suspended compute is changed, so the
	// write never touches a compute serving queries.
	SuspendTimeoutDeferred
)

// SuspendTimeoutConverger is a provider that can move an existing database's
// scale-to-zero suspend timeout to its current setting.
type SuspendTimeoutConverger interface {
	ConvergeSuspendTimeout(ctx context.Context, providerResourceID string) (SuspendTimeoutResult, error)
}

// SuspendTimeoutSeconds is the scale-to-zero suspend timeout in force.
func (r *Registry) SuspendTimeoutSeconds() int64 {
	if seconds := r.effective().suspendTimeoutSeconds; seconds != 0 {
		return seconds
	}
	return DefaultSuspendTimeoutSeconds
}

func (r *Registry) pushSuspendTimeout(seconds int64) {
	for _, backend := range r.backends {
		if setter, ok := backend.Provider.(SuspendTimeoutSetter); ok {
			setter.SetSuspendTimeout(seconds)
		}
	}
}

// SuspendTimeoutLister lists the databases the sweep considers.
type SuspendTimeoutLister interface {
	ListUsageDatabases(ctx context.Context, after UsageDatabaseCursor, limit int) ([]Database, error)
}

// SuspendTimeoutSweepSummary counts one sweep's databases.
type SuspendTimeoutSweepSummary struct {
	TargetSeconds int64
	Skipped       bool
	Converged     int
	Changed       int
	Deferred      int
	Failed        int
}

// SuspendTimeoutSweeper moves existing scale-to-zero databases onto the
// current suspend timeout. Once a sweep finds every database converged it
// stops reading providers until the setting changes or apid restarts.
type SuspendTimeoutSweeper struct {
	registry *Registry
	store    SuspendTimeoutLister
	logger   *slog.Logger
	// maxChanges bounds the writes of one sweep; the rest are deferred.
	maxChanges int
	done       atomic.Int64
}

const suspendTimeoutSweepBatch = 100

func NewSuspendTimeoutSweeper(registry *Registry, store SuspendTimeoutLister, logger *slog.Logger) (*SuspendTimeoutSweeper, error) {
	if registry == nil || store == nil {
		return nil, ErrInvalid
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SuspendTimeoutSweeper{registry: registry, store: store, logger: logger, maxChanges: 100}, nil
}

// Sweep runs one pass over ready scale-to-zero databases.
func (s *SuspendTimeoutSweeper) Sweep(ctx context.Context) (SuspendTimeoutSweepSummary, error) {
	ctx = WithBackgroundRequests(ctx)
	target := s.registry.SuspendTimeoutSeconds()
	summary := SuspendTimeoutSweepSummary{TargetSeconds: target}
	if s.done.Load() == target {
		summary.Skipped = true
		return summary, nil
	}
	var failures []error
	var after UsageDatabaseCursor
	for {
		databases, err := s.store.ListUsageDatabases(ctx, after, suspendTimeoutSweepBatch)
		if err != nil {
			return summary, err
		}
		for _, database := range databases {
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			if database.State != StateReady || database.DeletedAt != nil || database.ProviderResourceID == "" || !database.Spec.ScaleToZero {
				continue
			}
			backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
			if err != nil {
				summary.Failed++
				failures = append(failures, fmt.Errorf("database %s: %w", database.ID, err))
				continue
			}
			converger, ok := backend.Provider.(SuspendTimeoutConverger)
			if !ok {
				continue
			}
			if summary.Changed >= s.maxChanges {
				summary.Deferred++
				continue
			}
			result, err := converger.ConvergeSuspendTimeout(ctx, database.ProviderResourceID)
			switch {
			case err != nil:
				summary.Failed++
				failures = append(failures, fmt.Errorf("database %s: %w", database.ID, err))
			case result == SuspendTimeoutChanged:
				summary.Changed++
				s.logger.Info("managed postgres suspend timeout converged", "database_id", database.ID, "suspend_timeout_seconds", target)
			case result == SuspendTimeoutDeferred:
				summary.Deferred++
			default:
				summary.Converged++
			}
		}
		if len(databases) < suspendTimeoutSweepBatch {
			break
		}
		last := databases[len(databases)-1]
		after = UsageDatabaseCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	// A setting changed mid-sweep leaves done unset, so the next sweep runs.
	if summary.Changed == 0 && summary.Deferred == 0 && summary.Failed == 0 && s.registry.SuspendTimeoutSeconds() == target {
		s.done.Store(target)
	}
	return summary, errors.Join(failures...)
}
