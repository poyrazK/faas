package managedpostgres

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sort"
	"time"
)

const (
	defaultUsageCollectionInterval = 5 * time.Minute
	defaultUsageBatchSize          = 20
	secondsPerHour                 = int64(time.Hour / time.Second)
	bytesPerGiB                    = int64(1 << 30)
	byteSecondsPerGiBHour          = secondsPerHour * bytesPerGiB
	// Recovery and correction replay share this per-database request budget.
	// Committed coverage resumes remaining recovery windows on the next run.
	maximumUsageWindowsPerSweep  = 24
	recentUsageCorrectionWindows = 3
)

type UsageCollectionObservation struct {
	DatabaseID string
	Outcome    string
}

type UsageCollectionSummary struct {
	Discovered            int
	Recorded              int
	IncludedInSourceUsage int
	Deferred              int
	Enabled               bool
	CompletedAt           time.Time
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
	// Build the sweep from paginated catalog rows and durable coverage. Retain
	// one work item per database so no page can replay corrections before a
	// later page has had its recovery turns.
	var work []*usageCollectionWork
	defer func() {
		sweepErr = errors.Join(sweepErr, c.finishUsageCollection(ctx, work, to, sweepErr, &summary))
	}()
	var after UsageDatabaseCursor
	for {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		databases, err := c.store.ListUsageDatabases(ctx, after, c.batchSize)
		if err != nil {
			return summary, err
		}
		summary.Discovered += len(databases)
		for _, database := range databases {
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			work = append(work, c.prepareUsageCollection(ctx, database, to))
		}
		if len(databases) < c.batchSize {
			break
		}
		last := databases[len(databases)-1]
		after = UsageDatabaseCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	// Oldest successful observations go first, with the catalog order breaking
	// ties. Unmetered databases precede metered ones; persisted observations
	// move recently successful work behind older observations after a restart.
	sort.SliceStable(work, func(i, j int) bool {
		return work[i].observedAt.Before(work[j].observedAt)
	})
	if err := c.collectUsageRounds(ctx, work, to, now, false); err != nil {
		return summary, err
	}
	// Exhaust every eligible recovery turn before any correction request.
	// Databases with unfinished recovery cannot replay older windows.
	return summary, c.collectUsageRounds(ctx, work, to, now, true)
}

type usageCollectionWork struct {
	database    Database
	backend     Backend
	from        time.Time
	replayFrom  time.Time
	replayUntil time.Time
	observedAt  time.Time
	requests    int
	included    bool
	err         error
}

func (c *UsageCollector) prepareUsageCollection(ctx context.Context, database Database, to time.Time) *usageCollectionWork {
	work := &usageCollectionWork{database: database}
	if database.State != StateReady || database.ProviderResourceID == "" {
		work.err = ErrConflict
		return work
	}
	backend, err := c.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		work.err = ErrUnavailable
		return work
	}
	work.backend = backend
	if database.RestoreSourceDatabaseID != "" && backend.Capabilities.RestoreUsageIncludedInSource {
		// A restore descendant shares its root's aggregate, never an independent
		// provider request or ledger quantity.
		work.included = true
		work.err = c.recordSharedUsage(ctx, database)
		return work
	}
	progress, err := c.store.UsageProgress(ctx, database.AccountID, database.ID, c.policy.Window)
	if err != nil {
		work.err = err
		return work
	}
	work.observedAt = progress.ObservedAt
	work.from = to.Add(-c.policy.Window)
	if !progress.CollectedUntil.IsZero() {
		work.from = progress.CollectedUntil
	} else if !database.CreatedAt.IsZero() {
		work.from = database.CreatedAt.UTC().Truncate(c.policy.Window)
	}
	if !work.from.Before(to) && progress.CollectedUntil.IsZero() {
		work.err = ErrUsageStale
		return work
	}
	collectedFrom := progress.CollectedFrom
	if collectedFrom.IsZero() {
		collectedFrom = work.from
	}
	// Replay only windows established before this sweep. Recovery must not
	// fetch a new window and immediately request it again as a correction.
	work.replayUntil = work.from
	if work.replayUntil.After(to) {
		work.replayUntil = to
	}
	work.replayFrom = to.Add(-recentUsageCorrectionWindows * c.policy.Window)
	if work.replayFrom.Before(collectedFrom) {
		work.replayFrom = collectedFrom
	}
	return work
}

func (c *UsageCollector) collectUsageRounds(ctx context.Context, work []*usageCollectionWork, to, observedAt time.Time, corrections bool) error {
	rounds := maximumUsageWindowsPerSweep
	if corrections {
		rounds = recentUsageCorrectionWindows
	}
	for round := 0; round < rounds; round++ {
		attempted := false
		for _, item := range work {
			if item.err != nil || item.included || item.requests >= maximumUsageWindowsPerSweep {
				continue
			}
			from := item.from
			if corrections {
				if from.Before(to) || !item.replayUntil.After(item.replayFrom) {
					continue
				}
				from = item.replayUntil.Add(-c.policy.Window)
			} else if !from.Before(to) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			attempted = true
			item.requests++
			item.err = c.collectWindow(ctx, item.database, item.backend, from, from.Add(c.policy.Window), observedAt)
			if item.err == nil {
				if corrections {
					item.replayUntil = from
				} else {
					item.from = from.Add(c.policy.Window)
				}
			}
		}
		if !attempted {
			break
		}
	}
	return ctx.Err()
}

func (c *UsageCollector) finishUsageCollection(ctx context.Context, work []*usageCollectionWork, to time.Time, interrupted error, summary *UsageCollectionSummary) error {
	var collectionErr error
	for _, item := range work {
		if item.err == nil && !item.included {
			pending := item.requests < maximumUsageWindowsPerSweep && item.replayUntil.After(item.replayFrom)
			if item.from.Before(to) || (interrupted != nil && pending) {
				item.err = ErrUsageStale
				if err := ctx.Err(); err != nil {
					item.err = err
				}
			}
		}
		outcome := "recorded"
		switch {
		case item.err != nil:
			outcome = "deferred"
			summary.Deferred++
			collectionErr = errors.Join(collectionErr, item.err)
		case item.included:
			outcome = "included_in_source"
			summary.IncludedInSourceUsage++
		default:
			summary.Recorded++
		}
		if c.observe != nil {
			c.observe(UsageCollectionObservation{DatabaseID: item.database.ID, Outcome: outcome})
		}
	}
	return collectionErr
}

func (c *UsageCollector) recordSharedUsage(ctx context.Context, database Database) error {
	source := database
	seen := map[string]bool{database.ID: true}
	for source.RestoreSourceDatabaseID != "" {
		if seen[source.RestoreSourceDatabaseID] {
			return ErrConflict
		}
		seen[source.RestoreSourceDatabaseID] = true
		var err error
		source, err = c.store.Get(ctx, database.AccountID, source.RestoreSourceDatabaseID)
		if err != nil {
			return err
		}
		if source.State != StateReady || source.BackendID != database.BackendID || source.BackendFingerprint != database.BackendFingerprint {
			return ErrConflict
		}
	}
	return c.store.RecordSharedUsage(ctx, database.AccountID, database.ID, source.ID, c.policy.Window)
}

func (c *UsageCollector) collectWindow(ctx context.Context, database Database, backend Backend, from, to, observedAt time.Time) error {
	usage, err := backend.Provider.Usage(ctx, database.ProviderResourceID, UsageWindow{From: from, To: to})
	if err != nil {
		return normalizeProviderError(err)
	}
	if err := usage.Validate(); err != nil {
		return ErrUnavailable
	}
	// PostgreSQL checkpoints can carry time.Local; compare instants rather
	// than time.Time's location pointer or monotonic clock representation.
	if !usage.Window.From.Equal(from) || !usage.Window.To.Equal(to) {
		return ErrUnavailable
	}
	for _, meter := range backend.Capabilities.UsageMeters {
		found := false
		for _, reading := range usage.Readings {
			if reading.Meter == meter {
				found = true
				break
			}
		}
		if !found {
			return ErrUnavailable
		}
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
