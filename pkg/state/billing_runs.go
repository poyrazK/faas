package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Billing run vocabulary (ADR-1503).
const (
	BillingRunSourceAppConsumers    = "app_consumers"
	BillingRunSourcePlatformTenants = "platform_tenants"

	BillingRunExportNone   = "none"
	BillingRunExportStripe = "stripe"
	BillingRunExportPaddle = "paddle"
	BillingRunExportLago   = "lago"

	BillingRunTriggerAPI      = "api"
	BillingRunTriggerSchedule = "schedule"

	BillingRunQueued    = "queued"
	BillingRunRunning   = "running"
	BillingRunCompleted = "completed"

	BillingRunItemPending   = "pending"
	BillingRunItemDrafted   = "drafted"
	BillingRunItemFinalized = "finalized"
	BillingRunItemExported  = "exported"
	BillingRunItemUpToDate  = "up_to_date"
	BillingRunItemHeld      = "held"
	BillingRunItemSkipped   = "skipped"
	BillingRunItemFailed    = "failed"
)

// BillingRunItemOutcomes lists every item outcome in display order.
var BillingRunItemOutcomes = []string{BillingRunItemPending, BillingRunItemDrafted, BillingRunItemFinalized,
	BillingRunItemExported, BillingRunItemUpToDate, BillingRunItemHeld, BillingRunItemSkipped, BillingRunItemFailed}

// ErrBillingRunLeaseLost reports that another worker now owns the run.
var ErrBillingRunLeaseLost = errors.New("billing run lease lost")

// BillingRunOptions are what a run, or each run a schedule starts, does for
// every customer.
type BillingRunOptions struct {
	Sources    []string
	Finalize   bool
	Export     string
	HoldOnGaps bool
}

// Includes reports whether the run bills source.
func (o BillingRunOptions) Includes(source string) bool { return slices.Contains(o.Sources, source) }

// BillingRun bills one ended UTC month for every customer with billable
// usage. Counts holds items per outcome and is filled by reads.
type BillingRun struct {
	ID          string
	AccountID   string
	PeriodStart time.Time
	PeriodEnd   time.Time
	BillingRunOptions
	Trigger      string
	Status       string
	DiscoveredAt *time.Time
	LeaseOwner   string
	LeaseUntil   *time.Time
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	// NotifiedAt is when the account was told about the completed run
	// (ADR-1504).
	NotifiedAt *time.Time
	Counts     map[string]int
}

// BillingRunItem is one customer of a run. SourceID is the consumer or
// platform tenant ID; AppID is set for app consumers only.
type BillingRunItem struct {
	RunID            string
	SourceKind       string
	SourceID         string
	AppID            string
	ExternalRef      string
	Outcome          string
	StatementID      string
	Revision         int
	Currency         string
	AmountMillicents int64
	Completeness     string
	InvoiceID        string
	ExportedCount    int
	Reason           string
	Attempts         int
	NextAttemptAt    time.Time
	UpdatedAt        time.Time
	// PaymentStatus is read from the statement's invoice export (ADR-1504).
	PaymentStatus string
}

// Key orders items within a run and is the item list cursor.
func (i BillingRunItem) Key() string { return i.SourceKind + "/" + i.SourceID }

// BillingRunSchedule starts a run for NextPeriodStart's month at NextRunAt.
type BillingRunSchedule struct {
	AccountID string
	BillingRunOptions
	DelayHours      int
	NextPeriodStart time.Time
	NextRunAt       time.Time
	LastPeriodStart *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// BillingRunStore persists billing runs, their items and schedules.
type BillingRunStore interface {
	// CreateBillingRun returns the account's active run with ErrConflict
	// when one is queued or running.
	CreateBillingRun(ctx context.Context, run BillingRun) (BillingRun, error)
	GetBillingRun(ctx context.Context, accountID, runID string) (BillingRun, error)
	ListBillingRuns(ctx context.Context, accountID string, limit int) ([]BillingRun, error)
	// ListBillingRunItems pages a run's items in Key order after the given
	// key; outcome filters when set.
	ListBillingRunItems(ctx context.Context, accountID, runID, outcome, after string, limit int) ([]BillingRunItem, error)

	// ClaimBillingRun leases the oldest queued or running run whose lease is
	// free or expired; ErrNotFound when there is none.
	ClaimBillingRun(ctx context.Context, owner string, now time.Time, ttl time.Duration) (BillingRun, error)
	// DiscoverBillingRunItems adds one pending item per customer with
	// billable usage in the run's month. With platform tenants included, an
	// app consumer with tenant-attributed usage is added as skipped with
	// tenantReason.
	DiscoverBillingRunItems(ctx context.Context, runID, owner, tenantReason string, now time.Time) error
	ListDueBillingRunItems(ctx context.Context, runID, owner string, now time.Time, limit int) ([]BillingRunItem, error)
	UpdateBillingRunItem(ctx context.Context, owner string, item BillingRunItem) error
	// ReleaseBillingRun completes the run when no item is pending, and
	// otherwise frees the lease for the next tick.
	ReleaseBillingRun(ctx context.Context, runID, owner string, now time.Time) (BillingRun, error)

	PutBillingRunSchedule(ctx context.Context, schedule BillingRunSchedule) (BillingRunSchedule, error)
	GetBillingRunSchedule(ctx context.Context, accountID string) (BillingRunSchedule, error)
	DeleteBillingRunSchedule(ctx context.Context, accountID string) error
	ListDueBillingRunSchedules(ctx context.Context, now time.Time, limit int) ([]BillingRunSchedule, error)
	// StartScheduledBillingRun creates the schedule's run for its next month
	// (or finds the one already started for it) and advances the schedule
	// to next. It returns ErrConflict when the account already has another
	// active run or the schedule moved on.
	StartScheduledBillingRun(ctx context.Context, schedule BillingRunSchedule, run BillingRun, next BillingRunSchedule) (BillingRun, error)
}

// ValidateBillingRunOptions checks a run's or schedule's options.
func ValidateBillingRunOptions(o BillingRunOptions) error {
	if len(o.Sources) == 0 || len(o.Sources) > 2 {
		return fmt.Errorf("%w: sources must list app_consumers, platform_tenants or both", ErrInvalidArgument)
	}
	for i, source := range o.Sources {
		if (source != BillingRunSourceAppConsumers && source != BillingRunSourcePlatformTenants) || slices.Contains(o.Sources[:i], source) {
			return fmt.Errorf("%w: unknown or repeated source %q", ErrInvalidArgument, source)
		}
	}
	if o.Export != BillingRunExportNone && !ValidBillingProvider(o.Export) {
		return fmt.Errorf("%w: export must be none, stripe, paddle or lago", ErrInvalidArgument)
	}
	if o.Export != BillingRunExportNone && !o.Finalize {
		return fmt.Errorf("%w: exporting requires finalize", ErrInvalidArgument)
	}
	return nil
}

func validateBillingRun(run BillingRun) error {
	if run.AccountID == "" || !run.PeriodStart.Equal(monthStartUTC(run.PeriodStart)) || !run.PeriodEnd.Equal(run.PeriodStart.AddDate(0, 1, 0)) {
		return fmt.Errorf("%w: a billing run covers one UTC calendar month", ErrInvalidArgument)
	}
	if run.Trigger != BillingRunTriggerAPI && run.Trigger != BillingRunTriggerSchedule {
		return fmt.Errorf("%w: unknown billing run trigger", ErrInvalidArgument)
	}
	return ValidateBillingRunOptions(run.BillingRunOptions)
}

func validateBillingRunSchedule(s BillingRunSchedule) error {
	if s.AccountID == "" || s.DelayHours < 1 || s.DelayHours > 240 ||
		!s.NextPeriodStart.Equal(monthStartUTC(s.NextPeriodStart)) || !s.NextRunAt.After(s.NextPeriodStart) {
		return fmt.Errorf("%w: invalid billing run schedule", ErrInvalidArgument)
	}
	return ValidateBillingRunOptions(s.BillingRunOptions)
}

func monthStartUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func validBillingRunItemOutcome(outcome string) bool {
	return slices.Contains(BillingRunItemOutcomes, outcome)
}

// --- MemStore ---

func (m *MemStore) billingRunMapsLocked() {
	if m.billingRuns == nil {
		m.billingRuns = map[string]BillingRun{}
		m.billingRunItems = map[string]map[string]BillingRunItem{}
		m.billingRunSchedules = map[string]BillingRunSchedule{}
	}
}

func (m *MemStore) billingRunWithCountsLocked(run BillingRun) BillingRun {
	run.Sources = slices.Clone(run.Sources)
	run.Counts = map[string]int{}
	for _, item := range m.billingRunItems[run.ID] {
		run.Counts[item.Outcome]++
	}
	return run
}

func (m *MemStore) activeBillingRunLocked(accountID string) (BillingRun, bool) {
	for _, run := range m.billingRuns {
		if run.AccountID == accountID && run.Status != BillingRunCompleted {
			return run, true
		}
	}
	return BillingRun{}, false
}

func (m *MemStore) insertBillingRunLocked(run BillingRun) (BillingRun, error) {
	if active, ok := m.activeBillingRunLocked(run.AccountID); ok {
		return m.billingRunWithCountsLocked(active), ErrConflict
	}
	run.ID, run.Status, run.CreatedAt = uuid.NewString(), BillingRunQueued, time.Now().UTC()
	run.Sources = slices.Clone(run.Sources)
	run.DiscoveredAt, run.StartedAt, run.CompletedAt, run.NotifiedAt, run.LeaseOwner, run.LeaseUntil = nil, nil, nil, nil, "", nil
	m.billingRuns[run.ID] = run
	m.billingRunItems[run.ID] = map[string]BillingRunItem{}
	return m.billingRunWithCountsLocked(run), nil
}

func (m *MemStore) CreateBillingRun(_ context.Context, run BillingRun) (BillingRun, error) {
	if err := validateBillingRun(run); err != nil {
		return BillingRun{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	return m.insertBillingRunLocked(run)
}

func (m *MemStore) GetBillingRun(_ context.Context, accountID, runID string) (BillingRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	run, ok := m.billingRuns[runID]
	if !ok || run.AccountID != accountID {
		return BillingRun{}, ErrNotFound
	}
	return m.billingRunWithCountsLocked(run), nil
}

func (m *MemStore) ListBillingRuns(_ context.Context, accountID string, limit int) ([]BillingRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	out := []BillingRun{}
	for _, run := range m.billingRuns {
		if run.AccountID == accountID {
			out = append(out, m.billingRunWithCountsLocked(run))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ListBillingRunItems(_ context.Context, accountID, runID, outcome, after string, limit int) ([]BillingRunItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	if run, ok := m.billingRuns[runID]; !ok || run.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := []BillingRunItem{}
	for _, item := range m.billingRunItems[runID] {
		if (outcome == "" || item.Outcome == outcome) && item.Key() > after {
			for _, e := range m.statementInvoiceExports {
				if item.StatementID != "" && e.SourceKind == item.SourceKind && e.StatementID == item.StatementID {
					item.PaymentStatus = e.PaymentStatus
				}
			}
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ClaimBillingRun(_ context.Context, owner string, now time.Time, ttl time.Duration) (BillingRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	var claim *BillingRun
	for _, run := range m.billingRuns {
		if run.Status == BillingRunCompleted || (run.LeaseUntil != nil && run.LeaseUntil.After(now)) {
			continue
		}
		if claim == nil || run.CreatedAt.Before(claim.CreatedAt) {
			candidate := run
			claim = &candidate
		}
	}
	if claim == nil {
		return BillingRun{}, ErrNotFound
	}
	until := now.Add(ttl).UTC()
	claim.Status, claim.LeaseOwner, claim.LeaseUntil = BillingRunRunning, owner, &until
	if claim.StartedAt == nil {
		started := now.UTC()
		claim.StartedAt = &started
	}
	m.billingRuns[claim.ID] = *claim
	return m.billingRunWithCountsLocked(*claim), nil
}

func (m *MemStore) leasedBillingRunLocked(runID, owner string) (BillingRun, error) {
	run, ok := m.billingRuns[runID]
	if !ok {
		return BillingRun{}, ErrNotFound
	}
	if run.LeaseOwner != owner || run.Status == BillingRunCompleted {
		return BillingRun{}, ErrBillingRunLeaseLost
	}
	return run, nil
}

func (m *MemStore) DiscoverBillingRunItems(_ context.Context, runID, owner, tenantReason string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	run, err := m.leasedBillingRunLocked(runID, owner)
	if err != nil {
		return err
	}
	if run.DiscoveredAt != nil {
		return nil
	}
	inMonth := func(b APIConsumerUsageBucket) bool {
		return b.AccountID == run.AccountID && b.BillableUnits > 0 && !b.WindowStart.Before(run.PeriodStart) && b.WindowStart.Before(run.PeriodEnd)
	}
	attributed, tenants := map[string]bool{}, map[string]bool{}
	for key, bucket := range m.platformTenantUsage {
		if !inMonth(bucket) {
			continue
		}
		if parts := strings.SplitN(key, "\x00", 3); len(parts) == 3 {
			tenants[parts[1]] = true
		}
		if bucket.ConsumerKey != "" {
			attributed[bucket.AppID+"\x00"+bucket.ConsumerKey] = true
		}
	}
	items := m.billingRunItems[runID]
	add := func(item BillingRunItem) {
		item.RunID, item.NextAttemptAt, item.UpdatedAt = runID, now.UTC(), now.UTC()
		if _, ok := items[item.Key()]; !ok {
			items[item.Key()] = item
		}
	}
	if run.Includes(BillingRunSourceAppConsumers) {
		m.discoverFeeConsumersLocked(run, add)
		for _, bucket := range m.apiConsumerUsage {
			consumer, ok := m.apiConsumers[bucket.ConsumerKey]
			if !inMonth(bucket) || bucket.ConsumerKey == AnonymousConsumerKey || !ok || consumer.AppID != bucket.AppID {
				continue
			}
			item := BillingRunItem{SourceKind: StatementSourceAppConsumer, SourceID: consumer.ID, AppID: consumer.AppID,
				ExternalRef: consumer.ExternalRef, Outcome: BillingRunItemPending}
			if run.Includes(BillingRunSourcePlatformTenants) && attributed[bucket.AppID+"\x00"+bucket.ConsumerKey] {
				item.Outcome, item.Reason = BillingRunItemSkipped, tenantReason
			}
			add(item)
		}
	}
	if run.Includes(BillingRunSourcePlatformTenants) {
		for tenantID := range tenants {
			if tenant, ok := m.platformTenants[tenantID]; ok && tenant.AccountID == run.AccountID {
				add(BillingRunItem{SourceKind: StatementSourcePlatformTenant, SourceID: tenant.ID, ExternalRef: tenant.ExternalRef, Outcome: BillingRunItemPending})
			}
		}
	}
	discovered := now.UTC()
	run.DiscoveredAt = &discovered
	m.billingRuns[runID] = run
	return nil
}

// discoverFeeConsumersLocked adds the consumers of apps whose cards charge
// a base fee or minimum (ADR-1316): they owe it without usage.
func (m *MemStore) discoverFeeConsumersLocked(run BillingRun, add func(BillingRunItem)) {
	feeApps := map[string]bool{}
	for _, card := range m.apiConsumerRateCards {
		if card.AccountID == run.AccountID && card.MonthlyFees() && card.EffectiveFrom.Before(run.PeriodEnd) {
			feeApps[card.AppID] = true
		}
	}
	for _, consumer := range m.apiConsumers {
		if consumer.AccountID != run.AccountID || !feeApps[consumer.AppID] || !consumer.CreatedAt.Before(run.PeriodEnd) ||
			(consumer.RevokedAt != nil && consumer.RevokedAt.Before(run.PeriodStart)) {
			continue
		}
		add(BillingRunItem{SourceKind: StatementSourceAppConsumer, SourceID: consumer.ID, AppID: consumer.AppID,
			ExternalRef: consumer.ExternalRef, Outcome: BillingRunItemPending})
	}
}

func (m *MemStore) ListDueBillingRunItems(_ context.Context, runID, owner string, now time.Time, limit int) ([]BillingRunItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	if _, err := m.leasedBillingRunLocked(runID, owner); err != nil {
		return nil, err
	}
	out := []BillingRunItem{}
	for _, item := range m.billingRunItems[runID] {
		if item.Outcome == BillingRunItemPending && !item.NextAttemptAt.After(now) {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() > out[j].Key() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) UpdateBillingRunItem(_ context.Context, owner string, item BillingRunItem) error {
	if !validBillingRunItemOutcome(item.Outcome) || len(item.Reason) > 1000 {
		return fmt.Errorf("%w: invalid billing run item", ErrInvalidArgument)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	if _, err := m.leasedBillingRunLocked(item.RunID, owner); err != nil {
		return err
	}
	current, ok := m.billingRunItems[item.RunID][item.Key()]
	if !ok {
		return ErrNotFound
	}
	item.AppID, item.ExternalRef, item.UpdatedAt = current.AppID, current.ExternalRef, time.Now().UTC()
	m.billingRunItems[item.RunID][item.Key()] = item
	return nil
}

func (m *MemStore) ReleaseBillingRun(_ context.Context, runID, owner string, now time.Time) (BillingRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	run, err := m.leasedBillingRunLocked(runID, owner)
	if err != nil {
		return BillingRun{}, err
	}
	pending := false
	for _, item := range m.billingRunItems[runID] {
		pending = pending || item.Outcome == BillingRunItemPending
	}
	run.LeaseOwner, run.LeaseUntil = "", nil
	if run.DiscoveredAt != nil && !pending {
		completed := now.UTC()
		run.Status, run.CompletedAt = BillingRunCompleted, &completed
	}
	m.billingRuns[runID] = run
	return m.billingRunWithCountsLocked(run), nil
}

func (m *MemStore) PutBillingRunSchedule(_ context.Context, s BillingRunSchedule) (BillingRunSchedule, error) {
	if err := validateBillingRunSchedule(s); err != nil {
		return BillingRunSchedule{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	now := time.Now().UTC()
	s.Sources, s.UpdatedAt = slices.Clone(s.Sources), now
	if existing, ok := m.billingRunSchedules[s.AccountID]; ok {
		s.CreatedAt, s.LastPeriodStart = existing.CreatedAt, existing.LastPeriodStart
	} else {
		s.CreatedAt, s.LastPeriodStart = now, nil
	}
	m.billingRunSchedules[s.AccountID] = s
	return s, nil
}

func (m *MemStore) GetBillingRunSchedule(_ context.Context, accountID string) (BillingRunSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	s, ok := m.billingRunSchedules[accountID]
	if !ok {
		return BillingRunSchedule{}, ErrNotFound
	}
	s.Sources = slices.Clone(s.Sources)
	return s, nil
}

func (m *MemStore) DeleteBillingRunSchedule(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	if _, ok := m.billingRunSchedules[accountID]; !ok {
		return ErrNotFound
	}
	delete(m.billingRunSchedules, accountID)
	return nil
}

func (m *MemStore) ListDueBillingRunSchedules(_ context.Context, now time.Time, limit int) ([]BillingRunSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	out := []BillingRunSchedule{}
	for _, s := range m.billingRunSchedules {
		if !s.NextRunAt.After(now) {
			s.Sources = slices.Clone(s.Sources)
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(out[j].NextRunAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) StartScheduledBillingRun(_ context.Context, schedule BillingRunSchedule, run BillingRun, next BillingRunSchedule) (BillingRun, error) {
	if err := validateBillingRun(run); err != nil {
		return BillingRun{}, err
	}
	if err := validateBillingRunSchedule(next); err != nil {
		return BillingRun{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	current, ok := m.billingRunSchedules[schedule.AccountID]
	if !ok || !current.NextPeriodStart.Equal(schedule.NextPeriodStart) {
		return BillingRun{}, ErrConflict
	}
	var created BillingRun
	for _, existing := range m.billingRuns {
		if existing.AccountID == run.AccountID && existing.Trigger == BillingRunTriggerSchedule && existing.PeriodStart.Equal(run.PeriodStart) {
			created = m.billingRunWithCountsLocked(existing)
		}
	}
	if created.ID == "" {
		var err error
		if created, err = m.insertBillingRunLocked(run); err != nil {
			return BillingRun{}, err
		}
	}
	last := schedule.NextPeriodStart
	current.NextPeriodStart, current.NextRunAt, current.LastPeriodStart, current.UpdatedAt = next.NextPeriodStart, next.NextRunAt, &last, time.Now().UTC()
	m.billingRunSchedules[schedule.AccountID] = current
	return created, nil
}

// BillingRunNotificationStore records which completed runs the account was
// told about (ADR-1504).
type BillingRunNotificationStore interface {
	// ListBillingRunsToNotify returns completed runs not yet notified,
	// oldest completion first.
	ListBillingRunsToNotify(ctx context.Context, limit int) ([]BillingRun, error)
	MarkBillingRunNotified(ctx context.Context, runID string, at time.Time) error
}

var (
	_ BillingRunNotificationStore = (*MemStore)(nil)
	_ BillingRunNotificationStore = (*PgStore)(nil)
)

func (m *MemStore) ListBillingRunsToNotify(_ context.Context, limit int) ([]BillingRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	out := []BillingRun{}
	for _, run := range m.billingRuns {
		if run.Status == BillingRunCompleted && run.NotifiedAt == nil {
			out = append(out, m.billingRunWithCountsLocked(run))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CompletedAt.Before(*out[j].CompletedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) MarkBillingRunNotified(_ context.Context, runID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billingRunMapsLocked()
	run, ok := m.billingRuns[runID]
	if !ok {
		return ErrNotFound
	}
	if run.NotifiedAt == nil {
		notified := at.UTC()
		run.NotifiedAt = &notified
		m.billingRuns[runID] = run
	}
	return nil
}
