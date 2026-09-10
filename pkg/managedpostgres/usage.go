package managedpostgres

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"
)

const (
	defaultUsageCollectionInterval = 5 * time.Minute
	defaultUsageBatchSize          = 20
	secondsPerHour                 = int64(time.Hour / time.Second)
	bytesPerGiB                    = int64(1 << 30)
	byteSecondsPerGiBHour          = secondsPerHour * bytesPerGiB
)

type UsageCollectionObservation struct {
	DatabaseID string
	Outcome    string
}

type UsageCollectionSummary struct {
	Discovered  int
	Recorded    int
	Deferred    int
	Enabled     bool
	CompletedAt time.Time
}

// UsageSummary is the provider-neutral account usage read model. It contains
// only normalized meters and guardrail state; provider identifiers, rates,
// and credentials stay behind the registry boundary. The API layer can use
// this value for a customer-safe view while operators retain the raw ledger
// and policy for reconciliation.
type UsageSummary struct {
	Snapshot        UsageSnapshot
	EffectivePolicy UsagePolicy
	PolicyEnabled   bool
	Fresh           bool
	Exceeded        bool
}

type UsageCollectorOptions struct {
	Interval     time.Duration
	BatchSize    int
	Now          func() time.Time
	Logger       *slog.Logger
	Observe      func(UsageCollectionObservation)
	ObserveSweep func(UsageCollectionSummary, error)
}

// UsageCollector imports complete provider windows into the durable ledger.
// It is intentionally independent from lifecycle reconciliation: a provider
// outage can defer accounting without mutating database state, and a lifecycle
// retry cannot double-count an already-recorded window.
type UsageCollector struct {
	registry     *Registry
	store        UsageStore
	policy       UsagePolicy
	interval     time.Duration
	batchSize    int
	now          func() time.Time
	logger       *slog.Logger
	observe      func(UsageCollectionObservation)
	observeSweep func(UsageCollectionSummary, error)
}

func NewUsageCollector(registry *Registry, store UsageStore, options UsageCollectorOptions) (*UsageCollector, error) {
	if registry == nil || store == nil {
		return nil, ErrInvalid
	}
	policy := registry.UsagePolicy()
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if options.Interval == 0 {
		options.Interval = policy.CollectionInterval
		if options.Interval == 0 {
			options.Interval = defaultUsageCollectionInterval
		}
	}
	if options.BatchSize == 0 {
		options.BatchSize = defaultUsageBatchSize
	}
	if options.Interval < time.Minute || options.BatchSize < 1 || options.BatchSize > 100 {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &UsageCollector{
		registry:     registry,
		store:        store,
		policy:       policy,
		interval:     options.Interval,
		batchSize:    options.BatchSize,
		now:          options.Now,
		logger:       options.Logger,
		observe:      options.Observe,
		observeSweep: options.ObserveSweep,
	}, nil
}

func (c *UsageCollector) Collect(ctx context.Context) (summary UsageCollectionSummary, sweepErr error) {
	summary.Enabled = c.policy.Enabled
	defer func() {
		summary.CompletedAt = c.now().UTC()
		if c.observeSweep != nil {
			c.observeSweep(summary, sweepErr)
		}
	}()
	if !c.policy.Enabled {
		return summary, nil
	}
	now := c.now().UTC()
	to := now.Truncate(c.policy.Window)
	if to.IsZero() {
		return summary, ErrInvalid
	}
	from := to.Add(-c.policy.Window)
	databases, err := c.store.ListUsageDatabases(ctx, c.batchSize)
	if err != nil {
		return summary, err
	}
	summary.Discovered = len(databases)
	for _, database := range databases {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		outcome := "recorded"
		if err := c.collectDatabase(ctx, database, from, to, now); err != nil {
			outcome = "deferred"
			summary.Deferred++
			sweepErr = errors.Join(sweepErr, err)
		} else {
			summary.Recorded++
		}
		if c.observe != nil {
			c.observe(UsageCollectionObservation{DatabaseID: database.ID, Outcome: outcome})
		}
	}
	return summary, sweepErr
}

func (c *UsageCollector) collectDatabase(ctx context.Context, database Database, from, to, observedAt time.Time) error {
	if database.State != StateReady || database.ProviderResourceID == "" {
		return ErrConflict
	}
	backend, err := c.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return ErrUnavailable
	}
	usage, err := backend.Provider.Usage(ctx, database.ProviderResourceID, UsageWindow{From: from, To: to})
	if err != nil {
		return normalizeProviderError(err)
	}
	if err := usage.Validate(); err != nil {
		return ErrUnavailable
	}
	if usage.Window.From.UTC() != from || usage.Window.To.UTC() != to {
		return ErrUnavailable
	}
	records := make([]UsageRecord, 0, len(usage.Readings))
	for _, reading := range usage.Readings {
		cost, err := c.policy.Cost(reading)
		if err != nil {
			return err
		}
		record := UsageRecord{
			AccountID: database.AccountID, DatabaseID: database.ID,
			BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
			WindowFrom: from, WindowTo: to, ObservedAt: observedAt,
			Meter: reading.Meter, Quantity: reading.Quantity, CostMillicents: cost,
		}
		if err := record.Validate(); err != nil {
			return err
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return ErrUnavailable
	}
	return c.store.RecordUsage(ctx, records)
}

func (c *UsageCollector) Run(ctx context.Context) error {
	if !c.policy.Enabled {
		return nil
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		if _, err := c.Collect(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("managed postgres usage collection sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Admit rejects new database reservations when the account's observed usage
// is stale or has crossed any configured monthly safety ceiling. It is a
// fail-closed control-plane guard, not a customer invoice calculation.
func (p UsagePolicy) Admit(ctx context.Context, store UsageStore, accountID string, now time.Time) error {
	return p.AdmitWithCeilings(ctx, store, accountID, now, UsageCeilings{})
}

// AdmitWithCeilings applies both the operator COGS policy and the account's
// plan entitlement before a new reservation is persisted. Existing named
// resources remain idempotent in Service.Create; only new reservations are
// blocked when observations are stale or a ceiling has been reached.
func (p UsagePolicy) AdmitWithCeilings(ctx context.Context, store UsageStore, accountID string, now time.Time, ceilings UsageCeilings) error {
	if !p.Enabled {
		return nil
	}
	if store == nil || accountID == "" || now.IsZero() {
		return ErrInvalid
	}
	p = p.WithCeilings(ceilings)
	snapshot, err := store.UsageSnapshot(ctx, accountID, monthStart(now))
	if err != nil {
		return ErrUnavailable
	}
	if snapshot.Stale(p, now.UTC()) {
		return ErrUsageStale
	}
	if snapshot.Exceeds(p) {
		return ErrQuotaExceeded
	}
	return nil
}

// UsageSummary returns the current UTC-month snapshot for one account. Plan
// ceilings are supplied by the caller and intersected with the operator
// policy before freshness and budget state are evaluated, keeping this seam
// independent of plan names and provider billing models.
func (s *Service) UsageSummary(ctx context.Context, accountID string, now time.Time, ceilings UsageCeilings) (UsageSummary, error) {
	if s == nil || s.registry == nil || s.store == nil {
		return UsageSummary{}, ErrUnavailable
	}
	if accountID == "" || now.IsZero() {
		return UsageSummary{}, ErrInvalid
	}
	store, ok := s.store.(UsageStore)
	if !ok {
		return UsageSummary{}, ErrUnavailable
	}
	now = now.UTC()
	snapshot, err := store.UsageSnapshot(ctx, accountID, monthStart(now))
	if err != nil {
		return UsageSummary{}, err
	}
	policy := s.registry.UsagePolicy().WithCeilings(ceilings)
	return UsageSummary{
		Snapshot:        snapshot,
		EffectivePolicy: policy,
		PolicyEnabled:   policy.Enabled,
		Fresh:           policy.Enabled && !snapshot.Stale(policy, now),
		Exceeded:        snapshot.Exceeds(policy),
	}, nil
}

func (p UsagePolicy) Cost(reading MeterReading) (int64, error) {
	if reading.Quantity < 0 {
		return 0, ErrInvalid
	}
	var rate, denominator int64
	switch reading.Meter {
	case MeterComputeUnitSeconds:
		rate, denominator = p.ComputeUnitHourMillicents, secondsPerHour
	case MeterStorageByteSeconds:
		rate, denominator = p.StorageGiBHourMillicents, byteSecondsPerGiBHour
	case MeterHistoryByteSeconds:
		rate, denominator = p.HistoryGiBHourMillicents, byteSecondsPerGiBHour
	case MeterEgressBytes:
		rate, denominator = p.EgressGiBMillicents, bytesPerGiB
	default:
		return 0, nil
	}
	if rate == 0 || reading.Quantity == 0 {
		return 0, nil
	}
	if reading.Quantity > math.MaxInt64/rate {
		return 0, ErrUnavailable
	}
	total := reading.Quantity * rate
	quotient := total / denominator
	if total%denominator != 0 {
		quotient++
	}
	return quotient, nil
}

func addUsage(current, delta int64) (int64, error) {
	if delta < 0 || current > math.MaxInt64-delta {
		return 0, ErrUnavailable
	}
	return current + delta, nil
}

func monthStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}
