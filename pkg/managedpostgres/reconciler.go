package managedpostgres

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const defaultReconcileInterval = 15 * time.Second

type ReconcileOutcome string

const (
	OutcomeCompleted ReconcileOutcome = "completed"
	OutcomeDeferred  ReconcileOutcome = "deferred"
	OutcomeContended ReconcileOutcome = "contended"
	OutcomeFailed    ReconcileOutcome = "failed"
)

type ReconcileObservation struct {
	DatabaseID string
	Operation  State
	Outcome    ReconcileOutcome
}

type ReconcileSummary struct {
	Discovered int
	Completed  int
	Deferred   int
	Contended  int
	Failed     int
}

type ReconcilerOptions struct {
	Interval            time.Duration
	BatchSize           int
	Now                 func() time.Time
	IncludeProvisioning func() bool
	Observe             func(ReconcileObservation)
	Logger              *slog.Logger
}

// Reconciler discovers durable lifecycle intents and lets Service obtain the
// fenced lease before doing provider I/O. Provisioning can remain disabled
// while deletion recovery continues to prevent leaked upstream resources.
type Reconciler struct {
	service             *Service
	interval            time.Duration
	batchSize           int
	now                 func() time.Time
	includeProvisioning func() bool
	observe             func(ReconcileObservation)
	logger              *slog.Logger
}

func NewReconciler(service *Service, options ReconcilerOptions) (*Reconciler, error) {
	if service == nil || service.store == nil {
		return nil, ErrInvalid
	}
	if options.Interval == 0 {
		options.Interval = defaultReconcileInterval
	}
	if options.BatchSize == 0 {
		options.BatchSize = 20
	}
	if options.Interval < time.Second || options.BatchSize < 1 || options.BatchSize > 100 {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.IncludeProvisioning == nil {
		options.IncludeProvisioning = func() bool { return false }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Reconciler{
		service:             service,
		interval:            options.Interval,
		batchSize:           options.BatchSize,
		now:                 options.Now,
		includeProvisioning: options.IncludeProvisioning,
		observe:             options.Observe,
		logger:              options.Logger,
	}, nil
}

func (r *Reconciler) Sweep(ctx context.Context) (ReconcileSummary, error) {
	var summary ReconcileSummary
	rows, err := r.service.store.Due(ctx, r.includeProvisioning(), r.batchSize, r.now())
	if err != nil {
		return summary, err
	}
	summary.Discovered = len(rows)
	var sweepErrors []error
	for _, database := range rows {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		operation := StateProvisioning
		var result Database
		if database.State == StateDeleting {
			operation = StateDeleting
			result, err = r.service.Delete(ctx, database.AccountID, database.ID)
		} else {
			result, err = r.service.Reconcile(ctx, database.AccountID, database.ID)
		}
		outcome := classifyReconcileResult(result, err)
		switch outcome {
		case OutcomeCompleted:
			summary.Completed++
		case OutcomeDeferred:
			summary.Deferred++
		case OutcomeContended:
			summary.Contended++
		case OutcomeFailed:
			summary.Failed++
			sweepErrors = append(sweepErrors, err)
		}
		if r.observe != nil {
			r.observe(ReconcileObservation{DatabaseID: database.ID, Operation: operation, Outcome: outcome})
		}
	}
	return summary, errors.Join(sweepErrors...)
}

func (r *Reconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("managed postgres reconciliation sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func classifyReconcileResult(database Database, err error) ReconcileOutcome {
	if err == nil {
		if database.State == StateReady || database.State == StateDeleted {
			return OutcomeCompleted
		}
		return OutcomeDeferred
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		return OutcomeContended
	}
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrQuotaExceeded) {
		return OutcomeDeferred
	}
	return OutcomeFailed
}
