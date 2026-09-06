package managedpostgres

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type BindingReconcileObservation struct {
	BindingID string
	Operation BindingState
	Outcome   ReconcileOutcome
}

type BindingReconcileSummary struct {
	Discovered int
	Completed  int
	Deferred   int
	Contended  int
	Failed     int
}

type BindingReconcilerOptions struct {
	Interval            time.Duration
	BatchSize           int
	Now                 func() time.Time
	IncludeProvisioning func() bool
	Observe             func(BindingReconcileObservation)
	Logger              *slog.Logger
}

// BindingReconciler discovers durable credential intents. Provisioning obeys
// the dark-launch gate, while deletion always runs so disabling rollout can
// never strand an upstream role or an owned app secret.
type BindingReconciler struct {
	service             *BindingService
	interval            time.Duration
	batchSize           int
	now                 func() time.Time
	includeProvisioning func() bool
	observe             func(BindingReconcileObservation)
	logger              *slog.Logger
}

func NewBindingReconciler(service *BindingService, options BindingReconcilerOptions) (*BindingReconciler, error) {
	if service == nil || service.bindings == nil {
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
	return &BindingReconciler{
		service:             service,
		interval:            options.Interval,
		batchSize:           options.BatchSize,
		now:                 options.Now,
		includeProvisioning: options.IncludeProvisioning,
		observe:             options.Observe,
		logger:              options.Logger,
	}, nil
}

func (r *BindingReconciler) Sweep(ctx context.Context) (BindingReconcileSummary, error) {
	var summary BindingReconcileSummary
	rows, err := r.service.bindings.DueBindings(ctx, r.includeProvisioning(), r.batchSize, r.now())
	if err != nil {
		return summary, err
	}
	summary.Discovered = len(rows)
	var sweepErrors []error
	for _, binding := range rows {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		operation := BindingStateProvisioning
		var result Binding
		if binding.State == BindingStateDeleting {
			operation = BindingStateDeleting
			result, err = r.service.Delete(ctx, binding.AccountID, binding.ID)
		} else {
			result, err = r.service.Reconcile(ctx, binding.AccountID, binding.ID)
		}
		outcome := classifyBindingReconcileResult(result, err)
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
			r.observe(BindingReconcileObservation{BindingID: binding.ID, Operation: operation, Outcome: outcome})
		}
	}
	return summary, errors.Join(sweepErrors...)
}

func (r *BindingReconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("managed postgres binding reconciliation sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func classifyBindingReconcileResult(binding Binding, err error) ReconcileOutcome {
	if err == nil {
		if binding.State == BindingStateReady || binding.State == BindingStateDeleted {
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
