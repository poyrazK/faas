package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Managed PostgreSQL overage invoicing (ADR-20261031110037). Compute a subject
// used above its plan allowance in a UTC month, with overage on, is charged at
// api.ManagedPostgresOverageMillicentsPerComputeUnitHour, never past the
// overage ceiling. apid delivers the month's charge in steps after each cap
// sweep and keeps delivering a closed month for
// api.ManagedPostgresOverageCloseWindow while late usage windows arrive.
//
// Amendment 2: storage a subject with overage on holds past its plan's pool,
// averaged over the UTC month, is charged on the same step at
// api.ManagedPostgresStorageOverageMillicentsPerGiBMonth, never past one pool
// for the month.

const unitSecondsPerHour = 3600

// OverageCharge is the compute a subject is charged for in a month and its
// price. The allowance is the larger of the plan's and the one recorded when
// the subject reached it, so an upgrade never raises a charge.
func OverageCharge(usedUnitSeconds, allowanceUnitSeconds int64) (billedUnitSeconds, millicents int64) {
	if usedUnitSeconds <= 0 || allowanceUnitSeconds <= 0 {
		return 0, 0
	}
	room := allowanceUnitSeconds * (api.ManagedPostgresOverageCeilingMultiplier - 1)
	billedUnitSeconds = min(max(usedUnitSeconds-allowanceUnitSeconds, 0), room)
	// Floor: a fraction of a millicent is never charged.
	return billedUnitSeconds, billedUnitSeconds * api.ManagedPostgresOverageMillicentsPerComputeUnitHour / unitSecondsPerHour
}

// StorageOverageCharge is the storage a subject is charged for in a month and
// its price. The month's pool is poolBytes held for every second of the
// month, so only the month's average past the pool is charged, and a step
// never shrinks as the month goes on. At most one more pool is charged, as
// the doubled pool allows.
func StorageOverageCharge(usedByteSeconds, poolBytes int64, month time.Time) (billedByteSeconds, millicents int64) {
	monthSeconds := int64(month.AddDate(0, 1, 0).Sub(month) / time.Second)
	if usedByteSeconds <= 0 || poolBytes <= 0 || monthSeconds <= 0 {
		return 0, 0
	}
	allowance := new(big.Int).Mul(big.NewInt(poolBytes), big.NewInt(monthSeconds))
	room := new(big.Int).Mul(allowance, big.NewInt(api.ManagedPostgresOverageCeilingMultiplier-1))
	over := new(big.Int).Sub(big.NewInt(usedByteSeconds), allowance)
	if over.Sign() <= 0 {
		return 0, 0
	}
	if over.Cmp(room) > 0 {
		over = room
	}
	// Floor: a fraction of a millicent is never charged.
	price := new(big.Int).Mul(over, big.NewInt(api.ManagedPostgresStorageOverageMillicentsPerGiBMonth))
	price.Quo(price, new(big.Int).Mul(big.NewInt(1<<30), big.NewInt(monthSeconds)))
	return over.Int64(), price.Int64()
}

// EgressOverageCharge is the database egress a subject is charged for in a
// month and its price (ADR-20261110160000 slice 3). Apps and databases share
// the plan's egress allowance and apps use it first: databases are billed for
// what the month's total sent past the allowance beyond what the apps alone
// sent past it, at most one more allowance, as the 2x ceiling allows.
func EgressOverageCharge(databaseBytes, appBytes, allowanceBytes int64) (billedBytes, millicents int64) {
	databaseBytes, appBytes = max(databaseBytes, 0), max(appBytes, 0)
	if databaseBytes == 0 || allowanceBytes <= 0 {
		return 0, 0
	}
	// What the apps left of the allowance absorbs database egress first.
	room := max(allowanceBytes-appBytes, 0)
	billedBytes = min(max(databaseBytes-room, 0), allowanceBytes)
	return billedBytes, egressOverageMillicents(billedBytes)
}

// egressOverageMillicents prices billed database egress, flooring a fraction
// of a millicent.
func egressOverageMillicents(billedBytes int64) int64 {
	if billedBytes <= 0 {
		return 0
	}
	price := new(big.Int).Mul(big.NewInt(billedBytes), big.NewInt(api.ManagedPostgresEgressOverageMillicentsPerGB))
	return price.Quo(price, big.NewInt(1_000_000_000)).Int64()
}

// egressAllowance reads the subject's shared egress allowance at at; none
// when the biller has no egress source.
func (b *OverageBiller) egressAllowance(ctx context.Context, subjectID string, at time.Time) (EgressAllowance, error) {
	if b.options.Egress == nil {
		return EgressAllowance{}, nil
	}
	return b.options.Egress(ctx, subjectID, at)
}

// OverageDelivery is the cumulative charge delivered to one billing provider
// for a subject's month.
type OverageDelivery struct {
	Provider          string
	SubjectID         string
	PeriodStart       time.Time
	Live              bool
	BilledUnitSeconds int64
	Millicents        int64
	UpdatedAt         time.Time

	// BilledStorageByteSeconds is the storage past the pool the cumulative
	// charge includes (ADR-20261031110037 amendment 2).
	BilledStorageByteSeconds int64

	// BilledEgressBytes is the database egress past the shared egress
	// allowance the cumulative charge includes (ADR-20261110160000 slice 3).
	BilledEgressBytes int64
}

// OverageSubject is a subject that reached its allowance in a month, as the
// cap sweep recorded it.
type OverageSubject struct {
	SubjectID string
	Allowance int64
	Overage   bool
}

// OverageBillingStore is an optional UsageStore capability.
// RecordOverageDelivery advances a month's delivered charge only from
// previousMillicents, so two apid replicas cannot both deliver one step.
//
// A step is reserved before it is sent (ADR-20261101143527): ReserveOverage
// holds at most one step per provider, subject and month, OveragePending
// returns it until CompleteOverage records it as delivered and clears it, in
// one transaction. A step that was sent but not recorded is therefore sent
// again unchanged, and the provider deduplicates it on its target.
type OverageBillingStore interface {
	// PendingOverage pages reservations independently of the window for new
	// charges. through is the latest UTC month that may be recovered.
	PendingOverage(context.Context, string, OveragePendingRef, time.Time, int) ([]OveragePendingRef, error)
	OverageSubjects(ctx context.Context, periodStart time.Time) ([]OverageSubject, error)
	OverageDelivered(ctx context.Context, provider, subjectID string, periodStart time.Time) (OverageDelivery, error)
	// RecordOverageDelivery advances the month's delivery from previous, the
	// row as read (zero when none). A live delivery may replace a shadow
	// row, which charged nothing; a shadow delivery never replaces a live
	// one. It reports false when another writer moved the row first.
	RecordOverageDelivery(ctx context.Context, delivery OverageDelivery, previous OverageDelivery) (bool, error)
	OveragePending(ctx context.Context, provider, subjectID string, periodStart time.Time) (OveragePendingStep, bool, error)
	ReserveOverage(ctx context.Context, pending OveragePendingStep) (bool, error)
	CompleteOverage(ctx context.Context, pending OveragePendingStep) (bool, error)
}

// OveragePendingRef is both a reservation key and its stable recovery cursor.
type OveragePendingRef struct {
	SubjectID   string
	PeriodStart time.Time
}

// OveragePendingStep is a step reserved for one provider before it is sent.
// Live is the delivery mode it was reserved in: a shadow step is never sent.
// Previous is the month's delivery row as read when the step was computed;
// completing the step advances the row from it, as RecordOverageDelivery does.
type OveragePendingStep struct {
	Provider string
	Live     bool
	Step     OverageChargeStep
	Previous OverageDelivery
}

func validOveragePending(p OveragePendingStep) bool {
	s := p.Step
	return p.Provider != "" && s.SubjectID != "" && !s.PeriodStart.IsZero() && s.PeriodStart.Equal(monthStart(s.PeriodStart)) &&
		!s.At.Before(s.PeriodStart) && s.UsedUnitSeconds >= 0 && s.AllowanceSeconds >= 0 && s.BilledUnitSeconds >= 0 &&
		s.UsedStorageByteSeconds >= 0 && s.StoragePoolBytes >= 0 && s.BilledStorageByteSeconds >= 0 &&
		s.UsedEgressBytes >= 0 && s.AppEgressBytes >= 0 && s.EgressAllowanceBytes >= 0 && s.BilledEgressBytes >= 0 &&
		s.DeliveredMillicents >= 0 && s.TargetMillicents > s.DeliveredMillicents && p.Previous.Millicents >= 0
}

// OverageChargeStep raises a subject's month charge from Delivered to Target.
// A provider keys its idempotency on the subject, the month and Target.
type OverageChargeStep struct {
	SubjectID           string
	PeriodStart         time.Time
	At                  time.Time
	UsedUnitSeconds     int64
	AllowanceSeconds    int64
	BilledUnitSeconds   int64
	DeliveredMillicents int64
	TargetMillicents    int64

	// Storage held past the plan's pool (ADR-20261031110037 amendment 2).
	UsedStorageByteSeconds   int64
	StoragePoolBytes         int64
	BilledStorageByteSeconds int64

	// Database egress past the shared egress allowance, apps first
	// (ADR-20261110160000 slice 3).
	UsedEgressBytes      int64
	AppEgressBytes       int64
	EgressAllowanceBytes int64
	BilledEgressBytes    int64
}

// OverageAllowance resolves billing allowances for a UTC month. Closed
// months must use their recorded terms, rather than today's plan.
type OverageAllowance func(ctx context.Context, subjectID string, month time.Time) (CapLimits, error)

// OverageBillerOptions configures delivery. Publish is nil in shadow mode:
// steps are recorded but never sent.
type OverageBillerOptions struct {
	Provider      string
	EffectiveFrom time.Time
	Publish       func(context.Context, OverageChargeStep) error
	Allowance     OverageAllowance
	// PlanSpans, when set, splits month through at at the subject's plan
	// changes, oldest first, each with its plan's allowance; each span's usage
	// is billed against its own allowance (ADR-20261103131742 amendment 2).
	PlanSpans func(ctx context.Context, subjectID string, month, at time.Time) ([]AllowanceSpan, error)
	// Egress reads a subject's shared egress allowance, its apps' egress and
	// whether its egress overage election lifts the allowance, at the step's
	// time (ADR-20261110160000 slice 3). Nil bills no database egress.
	Egress EgressAllowanceFunc
	Now    func() time.Time
	Logger *slog.Logger
}

// OverageBiller delivers managed PostgreSQL overage charges.
type OverageBiller struct {
	usage   UsageStore
	billing OverageBillingStore
	options OverageBillerOptions
}

// OverageBillingSummary counts one delivery pass.
type OverageBillingSummary struct {
	Subjects   int
	Delivered  int
	Shadowed   int
	Failed     int
	Millicents int64
}

func NewOverageBiller(usage UsageStore, options OverageBillerOptions) (*OverageBiller, error) {
	billing, ok := usage.(OverageBillingStore)
	if !ok {
		return nil, errors.New("managed postgres: the usage store cannot record overage deliveries")
	}
	from := options.EffectiveFrom.UTC()
	if options.Provider == "" || options.Allowance == nil || from.IsZero() || !from.Equal(monthStart(from)) {
		return nil, errors.New("managed postgres: overage billing needs a provider, an allowance source and a UTC-month effective date")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &OverageBiller{usage: usage, billing: billing, options: options}, nil
}

// Deliver recovers reserved steps from every elapsed billing month, then
// brings current-month and still-open previous-month charges up to their usage.
func (b *OverageBiller) Deliver(ctx context.Context) (OverageBillingSummary, error) {
	now := b.options.Now().UTC()
	current := monthStart(now)
	months := []time.Time{current}
	if previous := current.AddDate(0, -1, 0); now.Sub(current) < api.ManagedPostgresOverageCloseWindow {
		months = []time.Time{previous, current}
	}
	var summary OverageBillingSummary
	handled, recoveryErr := b.recoverPending(ctx, current, &summary)
	var failures []error
	if recoveryErr != nil {
		failures = append(failures, recoveryErr)
	}
	for _, month := range months {
		if month.Before(b.options.EffectiveFrom) {
			continue
		}
		subjects, err := b.billing.OverageSubjects(ctx, month)
		if err != nil {
			return summary, errors.Join(append(failures, err)...)
		}
		at := now
		if month.Before(current) {
			at = current.Add(-time.Nanosecond)
		}
		for _, subject := range subjects {
			if handled[OveragePendingRef{SubjectID: subject.SubjectID, PeriodStart: month}] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			summary.Subjects++
			if err := b.deliver(ctx, subject, month, at, now, &summary); err != nil {
				summary.Failed++
				failures = append(failures, fmt.Errorf("subject %s %s: %w", subject.SubjectID, month.Format("2006-01"), err))
			}
		}
	}
	return summary, errors.Join(failures...)
}

func (b *OverageBiller) deliver(ctx context.Context, subject OverageSubject, month, at, now time.Time, summary *OverageBillingSummary) error {
	limits, allowanceErr := b.options.Allowance(ctx, subject.SubjectID, month)
	if errors.Is(allowanceErr, ErrNotFound) {
		// A deleted subject has no billing customer left to charge.
		return nil
	}
	// A step reserved by an earlier pass is finished before a new one is
	// computed: it may have reached the provider (ADR-20261101143527).
	pending, found, err := b.billing.OveragePending(ctx, b.options.Provider, subject.SubjectID, month)
	if err != nil {
		return err
	}
	if found {
		return b.send(ctx, pending, summary)
	}
	delivered, err := b.billing.OverageDelivered(ctx, b.options.Provider, subject.SubjectID, month)
	if err != nil {
		return err
	}
	live := b.options.Publish != nil
	// The step starts from what this mode has delivered. A shadow row
	// charged nothing, so a live step starts from zero over it; a shadow
	// step never rewrites a live row, whose charge it would hide (bug hunt
	// 33).
	baseline := delivered.Millicents
	if delivered.Millicents > 0 && delivered.Live != live {
		if !live {
			return nil
		}
		baseline = 0
	}
	// Compute and storage overage count when the subject reached its
	// allowance with overage on, has it on now, or was already charged for
	// them this month. Database egress counts while the subject's egress
	// overage election lifts the egress allowance, or once it was charged
	// (ADR-20261110160000 slice 3): an egress election alone never charges
	// the compute or storage a sweep let past the allowance.
	pgOverage := subject.Overage || limits.Overage || delivered.BilledUnitSeconds > 0 || delivered.BilledStorageByteSeconds > 0
	egress, err := b.egressAllowance(ctx, subject.SubjectID, at)
	if err != nil {
		return err
	}
	egressOn := egress.OverageLifted || delivered.BilledEgressBytes > 0
	if !pgOverage && !egressOn && baseline == 0 {
		return nil
	}
	snapshot, err := b.usage.UsageSnapshot(ctx, subject.SubjectID, at)
	if err != nil {
		return err
	}
	if snapshot.ComputeUnitSeconds == 0 && snapshot.StorageByteSeconds == 0 && snapshot.EgressBytes == 0 {
		// An account first enabling overage this month is also listed for
		// last month. No usage needs no historical terms or charge.
		return nil
	}
	if allowanceErr != nil {
		return allowanceErr
	}
	// Storage past the pool is charged while overage is on, and for the rest
	// of a month that already charged it, so turning overage off never
	// lowers the month's cumulative charge.
	var charge spanCharge
	if pgOverage {
		charge, err = b.charge(ctx, subject, limits, month, at, snapshot, limits.Overage || delivered.BilledStorageByteSeconds > 0)
		if err != nil {
			return err
		}
	}
	var egressBilled int64
	if egressOn {
		var egressMillicents int64
		egressBilled, egressMillicents = EgressOverageCharge(snapshot.EgressBytes, egress.AppEgressBytes, egress.CeilingBytes)
		// A step never lowers what the month already billed for egress.
		if egressBilled < delivered.BilledEgressBytes {
			egressBilled, egressMillicents = delivered.BilledEgressBytes, egressOverageMillicents(delivered.BilledEgressBytes)
		}
		charge.millicents += egressMillicents
	}
	billed, target, storageBilled := charge.billedUnitSeconds, charge.millicents, charge.billedStorageByteSeconds
	if target <= baseline {
		return nil
	}
	// The provider de-duplicates on the step's starting point, not its
	// target: after an ambiguous publish (a timeout, apid restarting) the
	// next sweep starts from the same point. The step is also reserved
	// before it is sent (ADR-20261101143527), so that sweep sends the
	// identical step rather than computing a new one.
	pending = OveragePendingStep{Provider: b.options.Provider, Live: live, Previous: delivered, Step: OverageChargeStep{
		SubjectID: subject.SubjectID, PeriodStart: month, At: now,
		UsedUnitSeconds: snapshot.ComputeUnitSeconds, AllowanceSeconds: charge.allowance,
		BilledUnitSeconds: billed, DeliveredMillicents: baseline, TargetMillicents: target,
		UsedStorageByteSeconds: snapshot.StorageByteSeconds, StoragePoolBytes: charge.storageAllowance, BilledStorageByteSeconds: storageBilled,
		UsedEgressBytes: max(snapshot.EgressBytes, 0), AppEgressBytes: max(egress.AppEgressBytes, 0), EgressAllowanceBytes: max(egress.CeilingBytes, 0),
		BilledEgressBytes: egressBilled}}
	reserved, err := b.billing.ReserveOverage(ctx, pending)
	if err != nil || !reserved {
		// Another replica holds this month's step; it sends it.
		return err
	}
	return b.send(ctx, pending, summary)
}

// charge is the month's charge through at. With the subject's plan spans it
// bills each span's usage against that span's allowance (ADR-20261103131742
// amendment 2); without them, the month's usage against the allowance now,
// or the one recorded when the subject reached it, whichever is larger.
func (b *OverageBiller) charge(ctx context.Context, subject OverageSubject, limits CapLimits, month, at time.Time, snapshot UsageSnapshot, storage bool) (spanCharge, error) {
	if reader, ok := b.usage.(UsageBeforeReader); ok && b.options.PlanSpans != nil {
		spans, err := b.options.PlanSpans(ctx, subject.SubjectID, month, at)
		if err != nil {
			return spanCharge{}, err
		}
		if len(spans) > 0 {
			return chargeBySpan(ctx, reader, subject.SubjectID, month, spans, snapshot, storage)
		}
	}
	out := spanCharge{allowance: max(limits.Allowance, subject.Allowance), storageAllowance: limits.StorageAllowance}
	out.billedUnitSeconds, out.millicents = OverageCharge(snapshot.ComputeUnitSeconds, out.allowance)
	if storage {
		var storageMillicents int64
		out.billedStorageByteSeconds, storageMillicents = StorageOverageCharge(snapshot.StorageByteSeconds, limits.StorageAllowance, month)
		out.millicents += storageMillicents
	}
	return out, nil
}

// send delivers a reserved step and records it. A failure leaves the step
// reserved, so the next pass sends the same step again rather than a new one.
func (b *OverageBiller) send(ctx context.Context, pending OveragePendingStep, summary *OverageBillingSummary) error {
	step := pending.Step
	if pending.Live {
		if b.options.Publish == nil {
			return errors.New("a live step is reserved while delivery is in shadow mode")
		}
		if err := b.options.Publish(ctx, step); err != nil {
			return err
		}
	}
	recorded, err := b.billing.CompleteOverage(ctx, pending)
	if err != nil || !recorded {
		// The month moved on without this step (a release that predates
		// reservations recorded one); it was cleared.
		return err
	}
	summary.Millicents += step.TargetMillicents - step.DeliveredMillicents
	if pending.Live {
		summary.Delivered++
	} else {
		summary.Shadowed++
	}
	b.options.Logger.Info("managed postgres overage charged", "subject_id", step.SubjectID, "period", step.PeriodStart.Format("2006-01"),
		"live", pending.Live, "used_unit_seconds", step.UsedUnitSeconds, "billed_unit_seconds", step.BilledUnitSeconds,
		"billed_storage_byte_seconds", step.BilledStorageByteSeconds, "previous_millicents", step.DeliveredMillicents, "target_millicents", step.TargetMillicents)
	return nil
}
