package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/bits"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261031165104: managed PostgreSQL overage is billed. Compute past the
// month's allowance and storage past the plan's pool are priced from the
// month's cumulative usage and delivered in daily month-to-date steps on a
// compare-and-set ledger, as object storage is (ADR-20261027093220). This
// file is step 1: shadow billing records what live would bill and sends
// nothing.

// Billing modes.
const (
	BillingModeShadow = "shadow"
	BillingModeLive   = "live"
)

// OverageCharge is a subject's priced overage for a month so far.
type OverageCharge struct {
	ComputeOverUnitSeconds  int64
	StorageOverByteSeconds  int64
	ComputeMillicents       int64
	StorageMillicents       int64
	TotalMillicents         int64
	ComputeAllowanceSeconds int64
	StoragePoolBytes        int64
}

const gibBytes = int64(1) << 30

// PriceOverage prices usage for the month starting at periodStart up to asOf
// against a compute allowance (CU-seconds) and a storage pool (bytes). Each
// part is floored once from cumulative usage, so a step that bills the
// difference from what was delivered never compounds rounding.
func PriceOverage(usage UsageSnapshot, computeAllowanceSeconds, storagePoolBytes int64, periodStart, asOf time.Time) (OverageCharge, error) {
	periodStart = monthStart(periodStart)
	periodEnd := monthStart(periodStart.AddDate(0, 1, 0))
	if computeAllowanceSeconds < 0 || storagePoolBytes < 0 || usage.ComputeUnitSeconds < 0 || usage.StorageByteSeconds < 0 || asOf.Before(periodStart) {
		return OverageCharge{}, ErrInvalid
	}
	charge := OverageCharge{ComputeAllowanceSeconds: computeAllowanceSeconds, StoragePoolBytes: storagePoolBytes}
	charge.ComputeOverUnitSeconds = max(0, usage.ComputeUnitSeconds-computeAllowanceSeconds)
	if asOf.After(periodEnd) {
		asOf = periodEnd
	}
	elapsed := int64(asOf.Sub(periodStart) / time.Second)
	monthSeconds := int64(periodEnd.Sub(periodStart) / time.Second)
	held, ok := mulDiv(uint64(storagePoolBytes), uint64(elapsed), 1)
	if !ok {
		return OverageCharge{}, ErrInvalid
	}
	charge.StorageOverByteSeconds = max(0, usage.StorageByteSeconds-held)
	compute, ok := mulDiv(uint64(charge.ComputeOverUnitSeconds), api.ManagedPostgresComputeOverageMillicentsPerCUHour, 3600)
	if !ok {
		return OverageCharge{}, ErrInvalid
	}
	storage, ok := mulDiv(uint64(charge.StorageOverByteSeconds), api.ManagedPostgresStorageOverageMillicentsPerGiBMonth, uint64(gibBytes)*uint64(monthSeconds))
	if !ok {
		return OverageCharge{}, ErrInvalid
	}
	charge.ComputeMillicents, charge.StorageMillicents = compute, storage
	if compute > math.MaxInt64-storage {
		return OverageCharge{}, ErrInvalid
	}
	charge.TotalMillicents = compute + storage
	return charge, nil
}

// mulDiv returns floor(a*b/c) when it fits an int64.
func mulDiv(a, b, c uint64) (int64, bool) {
	if c == 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(a, b)
	if hi >= c {
		return 0, false
	}
	q, _ := bits.Div64(hi, lo, c)
	if q > math.MaxInt64 {
		return 0, false
	}
	return int64(q), true
}

// BillingProgress is one subject's charge delivered so far for one UTC month
// and mode. A step reserves the most it can deliver as PendingMillicents,
// fixes it to its priced delta, publishes it (live only) and adds it to
// DeliveredMillicents. Every write is a compare-and-set on Version.
type BillingProgress struct {
	Provider            string
	SubjectID           string
	PeriodStart         time.Time
	Mode                string
	DeliveredMillicents int64
	PendingMillicents   int64
	// PendingFixed marks a pending step whose delta is final and may already
	// be published; it is completed, never recomputed.
	PendingFixed bool
	// ClosedAt is set once the month's final step is delivered.
	ClosedAt *time.Time
	// Version increments on every write; zero means no row yet.
	Version   int64
	UpdatedAt time.Time
}

// BillingProgressStore persists BillingProgress rows.
type BillingProgressStore interface {
	// GetBillingProgress returns ErrNotFound when the month has no row in
	// that mode yet.
	GetBillingProgress(ctx context.Context, provider, subjectID string, periodStart time.Time, mode string) (BillingProgress, error)
	// SaveBillingProgress writes p if the stored row is still at p.Version
	// (zero inserts) and returns it at the next version. A lost race returns
	// ErrConflict.
	SaveBillingProgress(ctx context.Context, p BillingProgress, now time.Time) (BillingProgress, error)
}

func (p BillingProgress) valid() bool {
	return p.Provider != "" && p.SubjectID != "" && !p.PeriodStart.IsZero() && p.PeriodStart.Equal(monthStart(p.PeriodStart)) &&
		(p.Mode == BillingModeShadow || p.Mode == BillingModeLive) && p.Version >= 0 &&
		p.DeliveredMillicents >= 0 && p.PendingMillicents >= 0 && p.DeliveredMillicents <= math.MaxInt64-p.PendingMillicents &&
		(!p.PendingFixed || p.PendingMillicents > 0)
}

// due reports a month whose last step is an interval old, or whose
// reservation a failed step left behind for a retry interval.
func (p BillingProgress) due(now time.Time) bool {
	age := now.Sub(p.UpdatedAt)
	return age >= api.ManagedPostgresBillingProgressInterval || p.PendingMillicents > 0 && age >= api.ManagedPostgresBillingProgressRetry
}

// BillingPolicy is the billing provider's rollout boundary: its name, the
// mode, and the first UTC month it applies to.
type BillingPolicy struct {
	Provider      string
	Mode          string
	EffectiveFrom time.Time
}

func (p BillingPolicy) valid() bool {
	return p.Provider != "" && (p.Mode == BillingModeShadow || p.Mode == BillingModeLive) &&
		!p.EffectiveFrom.IsZero() && p.EffectiveFrom.Equal(monthStart(p.EffectiveFrom))
}

// OverageEntitlement is what a subject's plan bills against. Billable is
// false for a plan that never pays overage (Free) or a subject with overage
// off; its databases are suspended at the allowance instead.
type OverageEntitlement struct {
	Billable                bool
	ComputeAllowanceSeconds int64
	StoragePoolBytes        int64
}

// BillingSweepSummary counts one sweep.
type BillingSweepSummary struct {
	Mode        string
	Subjects    int
	Stepped     int
	Closed      int
	NotBillable int
	Stale       int
	NotDue      int
	Failed      int
	// StepMillicents is the sum of this sweep's step deltas.
	StepMillicents int64
	CompletedAt    time.Time
}

// BillingSweeperOptions configures a BillingSweeper.
type BillingSweeperOptions struct {
	// Policy returns the billing provider's policy; ok=false is off.
	Policy func() (BillingPolicy, bool)
	// Entitlement returns what subjectID's plan bills against.
	Entitlement func(ctx context.Context, subjectID string) (OverageEntitlement, error)
	Now         func() time.Time
	BatchSize   int
	Logger      *slog.Logger
	Observe     func(BillingSweepSummary)
}

// BillingStore is what the sweep reads and writes.
type BillingStore interface {
	UsageStore
	CapSubjectStore
	BillingProgressStore
}

// BillingSweeper delivers month-to-date overage charges. The usage
// collector's AfterSweep runs it under the usage sweep lock, so one apid
// sweeps at a time; the ledger's compare-and-set covers the rest.
type BillingSweeper struct {
	registry    *Registry
	store       BillingStore
	policy      func() (BillingPolicy, bool)
	entitlement func(context.Context, string) (OverageEntitlement, error)
	now         func() time.Time
	batchSize   int
	logger      *slog.Logger
	observe     func(BillingSweepSummary)
}

func NewBillingSweeper(registry *Registry, store BillingStore, options BillingSweeperOptions) (*BillingSweeper, error) {
	if registry == nil || store == nil || options.Policy == nil || options.Entitlement == nil {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.BatchSize == 0 {
		options.BatchSize = 100
	}
	if options.BatchSize < 1 || options.BatchSize > 100 {
		return nil, ErrInvalid
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &BillingSweeper{registry: registry, store: store, policy: options.Policy, entitlement: options.Entitlement,
		now: options.Now, batchSize: options.BatchSize, logger: options.Logger, observe: options.Observe}, nil
}

// Sweep steps every billable subject's current month that is due, and closes
// the previous month once it has settled.
func (w *BillingSweeper) Sweep(ctx context.Context) (summary BillingSweepSummary, err error) {
	defer func() {
		summary.CompletedAt = w.now().UTC()
		if w.observe != nil {
			w.observe(summary)
		}
	}()
	policy, enabled := w.policy()
	if !enabled {
		return summary, nil
	}
	if !policy.valid() {
		return summary, fmt.Errorf("%w: billing policy", ErrInvalid)
	}
	if policy.Mode == BillingModeLive {
		// The live publish is step 3 (ADR-20261031165104); until then a live
		// policy must not acknowledge charges it never sends.
		return summary, fmt.Errorf("%w: live managed PostgreSQL billing", ErrUnsupported)
	}
	summary.Mode = policy.Mode
	now := w.now().UTC()
	period := monthStart(now)
	subjects, err := w.subjects(ctx, period.AddDate(0, -1, 0))
	if err != nil {
		return summary, err
	}
	summary.Subjects = len(subjects)
	var failures []error
	for _, subject := range subjects {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if err := w.sweepSubject(ctx, policy, subject, now, &summary); err != nil {
			summary.Failed++
			failures = append(failures, fmt.Errorf("subject %s: %w", subject, err))
		}
	}
	return summary, errors.Join(failures...)
}

func (w *BillingSweeper) sweepSubject(ctx context.Context, policy BillingPolicy, subject string, now time.Time, summary *BillingSweepSummary) error {
	period := monthStart(now)
	previous := period.AddDate(0, -1, 0)
	if !previous.Before(policy.EffectiveFrom) && !now.Before(period.Add(api.ManagedPostgresBillingSettle)) {
		if err := w.closeMonth(ctx, policy, subject, previous, period, summary); err != nil {
			return fmt.Errorf("close %s: %w", previous.Format("2006-01"), err)
		}
	}
	if period.Before(policy.EffectiveFrom) {
		return nil
	}
	progress, err := w.store.GetBillingProgress(ctx, policy.Provider, subject, period, policy.Mode)
	if err == nil && !progress.due(now) {
		summary.NotDue++
		return nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	entitlement, err := w.entitlement(ctx, subject)
	if err != nil {
		return err
	}
	if !entitlement.Billable {
		summary.NotBillable++
		return nil
	}
	snapshot, err := w.store.UsageSnapshot(ctx, subject, now)
	if err != nil {
		return err
	}
	if snapshot.Stale(w.registry.UsagePolicy(), now) {
		// Stale usage never bills; the next sweep tries again.
		summary.Stale++
		return nil
	}
	charge, err := PriceOverage(snapshot, entitlement.ComputeAllowanceSeconds, entitlement.StoragePoolBytes, period, now)
	if err != nil {
		return err
	}
	delta, err := w.step(ctx, policy, subject, period, charge.TotalMillicents, now, false)
	if err != nil {
		return err
	}
	summary.Stepped++
	summary.StepMillicents += delta
	return nil
}

// closeMonth brings a month that had a row up to its final charge and marks
// it closed. A subject no longer billable gets no new charge: only a step
// already fixed is finished.
func (w *BillingSweeper) closeMonth(ctx context.Context, policy BillingPolicy, subject string, start, end time.Time, summary *BillingSweepSummary) error {
	progress, err := w.store.GetBillingProgress(ctx, policy.Provider, subject, start, policy.Mode)
	if errors.Is(err, ErrNotFound) || err == nil && progress.ClosedAt != nil {
		return nil
	}
	if err != nil {
		return err
	}
	now := w.now().UTC()
	entitlement, err := w.entitlement(ctx, subject)
	if err != nil {
		return err
	}
	target := progress.DeliveredMillicents
	if entitlement.Billable {
		snapshot, err := w.store.UsageSnapshot(ctx, subject, start)
		if err != nil {
			return err
		}
		charge, err := PriceOverage(snapshot, entitlement.ComputeAllowanceSeconds, entitlement.StoragePoolBytes, start, end)
		if err != nil {
			return err
		}
		target = max(target, charge.TotalMillicents)
	}
	delta, err := w.step(ctx, policy, subject, start, target, now, true)
	if err != nil {
		return err
	}
	summary.Closed++
	summary.StepMillicents += delta
	return nil
}

// step raises the subject's delivered charge for the month toward target in
// the ledger's compare-and-set writes: reserve, fix, (publish, live only),
// complete. A step already fixed by an earlier attempt is completed first.
// close marks the month closed with its last write. It returns the delta.
func (w *BillingSweeper) step(ctx context.Context, policy BillingPolicy, subject string, period time.Time, target int64, now time.Time, close bool) (int64, error) {
	progress, err := w.store.GetBillingProgress(ctx, policy.Provider, subject, period, policy.Mode)
	if errors.Is(err, ErrNotFound) {
		progress = BillingProgress{Provider: policy.Provider, SubjectID: subject, PeriodStart: period, Mode: policy.Mode}
	} else if err != nil {
		return 0, err
	}
	var finished int64
	if progress.PendingFixed {
		// An earlier attempt fixed this delta; finish it as it was.
		finished = progress.PendingMillicents
		progress.DeliveredMillicents += progress.PendingMillicents
		progress.PendingMillicents, progress.PendingFixed = 0, false
		if progress, err = w.store.SaveBillingProgress(ctx, progress, now); err != nil {
			return 0, err
		}
	}
	delta := max(0, target-progress.DeliveredMillicents)
	if delta > 0 {
		progress.PendingMillicents, progress.PendingFixed = delta, false
		if progress, err = w.store.SaveBillingProgress(ctx, progress, now); err != nil {
			return finished, err
		}
		progress.PendingFixed = true
		if progress, err = w.store.SaveBillingProgress(ctx, progress, now); err != nil {
			return finished, err
		}
		progress.DeliveredMillicents += progress.PendingMillicents
		progress.PendingMillicents, progress.PendingFixed = 0, false
	}
	if close {
		closedAt := now
		progress.ClosedAt = &closedAt
	}
	// The last write records the step even when nothing was owed, so the
	// month has a row to close and the next step waits an interval.
	if _, err := w.store.SaveBillingProgress(ctx, progress, now); err != nil {
		return finished, err
	}
	return finished + delta, nil
}

// subjects lists the usage subjects of every database that existed since
// from: a database deleted this month still has this month's usage to bill.
func (w *BillingSweeper) subjects(ctx context.Context, from time.Time) ([]string, error) {
	var ids []string
	seen := map[string]bool{}
	accounts := map[string]string{}
	var after UsageDatabaseCursor
	for {
		databases, err := w.store.ListUsageDatabases(ctx, after, w.batchSize)
		if err != nil {
			return nil, err
		}
		for _, database := range databases {
			if seen[database.ID] || database.DeletedAt != nil && database.DeletedAt.Before(from) {
				continue
			}
			seen[database.ID] = true
			ids = append(ids, database.ID)
			accounts[database.ID] = database.AccountID
		}
		if len(databases) < w.batchSize {
			break
		}
		last := databases[len(databases)-1]
		after = UsageDatabaseCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	subjects := map[string]string{}
	if len(ids) > 0 {
		var err error
		if subjects, err = w.store.UsageSubjects(ctx, ids); err != nil {
			return nil, err
		}
	}
	unique := map[string]bool{}
	for _, id := range ids {
		subject := subjects[id]
		if subject == "" {
			subject = accounts[id]
		}
		unique[subject] = true
	}
	out := make([]string, 0, len(unique))
	for subject := range unique {
		out = append(out, subject)
	}
	sort.Strings(out)
	return out, nil
}
