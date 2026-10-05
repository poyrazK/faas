package managedpostgres

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const (
	healthSweepInterval   = 15 * time.Second
	healthProviderTimeout = 10 * time.Second
	healthLeaseDuration   = 30 * time.Second
	healthMaxBackoff      = 15 * time.Minute
)

func (r *Registry) HealthPolicy() HealthPolicy { return r.health }

type HealthCollectionObservation struct {
	Outcome  string
	Duration time.Duration
}
type HealthCollectionSummary struct {
	Enabled     bool
	Checked     int
	Counts      HealthCounts
	CompletedAt time.Time
	StaleAfter  time.Duration
}
type HealthCollectorOptions struct {
	BatchSize      int
	MinimumSpacing time.Duration
	Now            func() time.Time
	NewLeaseToken  func() string
	Observe        func(HealthCollectionObservation)
	ObserveSweep   func(HealthCollectionSummary, error)
	Logger         *slog.Logger
}
type HealthCollector struct {
	registry *Registry
	store    HealthStore
	policy   HealthPolicy
	options  HealthCollectorOptions
}

func NewHealthCollector(registry *Registry, store HealthStore, options HealthCollectorOptions) (*HealthCollector, error) {
	if registry == nil || store == nil {
		return nil, ErrInvalid
	}
	if options.BatchSize == 0 {
		options.BatchSize = 20
	}
	if options.MinimumSpacing == 0 {
		options.MinimumSpacing = time.Second
	}
	if options.BatchSize < 1 || options.BatchSize > 100 || options.MinimumSpacing < time.Millisecond {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewLeaseToken == nil {
		options.NewLeaseToken = func() string { return uuid.NewString() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &HealthCollector{registry: registry, store: store, policy: registry.HealthPolicy(), options: options}, nil
}

func (c *HealthCollector) Sweep(ctx context.Context) (summary HealthCollectionSummary, sweepErr error) {
	summary.Enabled, summary.StaleAfter = c.policy.Enabled, c.policy.StaleAfter
	defer func() {
		if c.options.ObserveSweep != nil {
			c.options.ObserveSweep(summary, sweepErr)
		}
	}()
	if !c.policy.Enabled {
		return summary, nil
	}
	for i := 0; i < c.options.BatchSize; i++ {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		now := c.options.Now()
		claim, err := c.store.ClaimHealthCheck(ctx, c.options.NewLeaseToken(), now, now.Add(healthLeaseDuration))
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			return summary, err
		}
		if err := c.check(ctx, claim); err != nil {
			return summary, err
		}
		summary.Checked++
		// Bound provider request starts per process. Durable per-database
		// leases prevent replicas from duplicating checks; failures back off.
		timer := time.NewTimer(c.options.MinimumSpacing)
		select {
		case <-ctx.Done():
			timer.Stop()
			return summary, ctx.Err()
		case <-timer.C:
		}
	}
	var err error
	now := c.options.Now()
	summary.Counts, err = c.store.CountDatabaseHealth(ctx, now.Add(-c.policy.StaleAfter), now)
	if err != nil {
		return summary, err
	}
	summary.CompletedAt = c.options.Now()
	return summary, nil
}

func (c *HealthCollector) check(ctx context.Context, claim HealthClaim) error {
	started := c.options.Now()
	result := c.observe(ctx, claim.Database)
	if ctx.Err() != nil {
		return ctx.Err()
	} // Let the lease expire on shutdown.
	result.CheckedAt = c.options.Now()
	delay := c.policy.Interval
	if !result.Succeeded {
		for i := int32(0); i < claim.AttemptCount && delay < healthMaxBackoff; i++ {
			delay *= 2
		}
		ceiling := max(healthMaxBackoff, c.policy.Interval)
		if delay > ceiling {
			delay = ceiling
		}
	}
	result.NextCheckAt = result.CheckedAt.Add(delay)
	err := c.store.FinishHealthCheck(ctx, claim, result)
	if errors.Is(err, ErrConflict) {
		return nil
	} // Deletion/replacement or expired lease won.
	if err != nil {
		return err
	}
	if c.options.Observe != nil {
		outcome := "degraded"
		if result.LastErrorCode == "" && result.ProviderStatus == "ready" {
			outcome = "healthy"
		}
		c.options.Observe(HealthCollectionObservation{Outcome: outcome, Duration: c.options.Now().Sub(started)})
	}
	return nil
}

func (c *HealthCollector) observe(ctx context.Context, database Database) HealthResult {
	result := HealthResult{HealthSnapshot: HealthSnapshot{ProviderStatus: "unknown", ComputeState: ComputeStateUnknown}}
	backend, err := c.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		result.LastErrorCode = "backend_unavailable"
		return result
	}
	observer, ok := backend.Provider.(DatabaseObserver)
	if !ok {
		result.LastErrorCode = "observer_unsupported"
		return result
	}
	providerCtx, cancel := context.WithTimeout(ctx, healthProviderTimeout)
	defer cancel()
	observed, err := observer.Observe(providerCtx, database.ProviderResourceID)
	if err != nil {
		result.LastErrorCode = healthErrorCode(err)
		if errors.Is(err, ErrNotFound) {
			result.ProviderStatus = "missing"
		}
		return result
	}
	if observed.ProviderResourceID != database.ProviderResourceID {
		result.LastErrorCode = "observation_invalid"
		return result
	}
	switch observed.Status {
	case ProviderStatusPending, ProviderStatusReady, ProviderStatusDeleting, ProviderStatusFailed:
	default:
		result.LastErrorCode = "observation_invalid"
		return result
	}
	switch observed.ComputeState {
	case ComputeStateUnknown, ComputeStateActive, ComputeStateSuspended, ComputeStateWaking:
	default:
		result.LastErrorCode = "observation_invalid"
		return result
	}
	result.ProviderStatus, result.ComputeState = string(observed.Status), observed.ComputeState
	result.Succeeded = true
	if observed.Status == ProviderStatusFailed {
		result.LastErrorCode = "provider_failed"
	}
	if observed.Status == ProviderStatusReady && observed.Spec != database.Spec {
		result.LastErrorCode = "spec_mismatch"
	}
	if observed.Status == ProviderStatusReady && observed.ComputeState == ComputeStateUnknown {
		result.LastErrorCode = "observation_invalid"
	}
	return result
}

func (c *HealthCollector) Run(ctx context.Context) error {
	ticker := time.NewTicker(healthSweepInterval)
	defer ticker.Stop()
	for {
		if _, err := c.Sweep(ctx); err != nil && ctx.Err() == nil {
			c.options.Logger.Warn("managed postgres health collection failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
