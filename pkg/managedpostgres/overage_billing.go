package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Managed PostgreSQL overage invoicing (ADR-20261031110037). Compute a subject
// used above its plan allowance in a UTC month, with overage on, is charged at
// api.ManagedPostgresOverageMillicentsPerComputeUnitHour, never past the
// overage ceiling. apid delivers the month's charge in steps after each cap
// sweep and keeps delivering a closed month for
// api.ManagedPostgresOverageCloseWindow while late usage windows arrive.

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
	OverageSubjects(ctx context.Context, periodStart time.Time) ([]OverageSubject, error)
	OverageDelivered(ctx context.Context, provider, subjectID string, periodStart time.Time) (OverageDelivery, error)
	RecordOverageDelivery(ctx context.Context, delivery OverageDelivery, previousMillicents int64) (bool, error)
	OveragePending(ctx context.Context, provider, subjectID string, periodStart time.Time) (OveragePendingStep, bool, error)
	ReserveOverage(ctx context.Context, pending OveragePendingStep) (bool, error)
	CompleteOverage(ctx context.Context, pending OveragePendingStep) (bool, error)
}

// OveragePendingStep is a step reserved for one provider before it is sent.
// Live is the delivery mode it was reserved in: a shadow step is never sent.
type OveragePendingStep struct {
	Provider string
	Live     bool
	Step     OverageChargeStep
}

func validOveragePending(p OveragePendingStep) bool {
	s := p.Step
	return p.Provider != "" && s.SubjectID != "" && !s.PeriodStart.IsZero() && s.PeriodStart.Equal(monthStart(s.PeriodStart)) &&
		!s.At.Before(s.PeriodStart) && s.UsedUnitSeconds >= 0 && s.AllowanceSeconds >= 0 && s.BilledUnitSeconds >= 0 &&
		s.DeliveredMillicents >= 0 && s.TargetMillicents > s.DeliveredMillicents
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
}

// OverageBillerOptions configures delivery. Publish is nil in shadow mode:
// steps are recorded but never sent.
type OverageBillerOptions struct {
	Provider      string
	EffectiveFrom time.Time
	Publish       func(context.Context, OverageChargeStep) error
	Allowance     func(ctx context.Context, subjectID string) (CapLimits, error)
	Now           func() time.Time
	Logger        *slog.Logger
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

// Deliver brings every subject's charge for the current month, and for the
// previous one inside the close window, up to its usage.
func (b *OverageBiller) Deliver(ctx context.Context) (OverageBillingSummary, error) {
	now := b.options.Now().UTC()
	current := monthStart(now)
	months := []time.Time{current}
	if previous := current.AddDate(0, -1, 0); now.Sub(current) < api.ManagedPostgresOverageCloseWindow {
		months = []time.Time{previous, current}
	}
	var summary OverageBillingSummary
	var failures []error
	for _, month := range months {
		if month.Before(b.options.EffectiveFrom) {
			continue
		}
		subjects, err := b.billing.OverageSubjects(ctx, month)
		if err != nil {
			return summary, err
		}
		at := now
		if month.Before(current) {
			at = current.Add(-time.Nanosecond)
		}
		for _, subject := range subjects {
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
	limits, err := b.options.Allowance(ctx, subject.SubjectID)
	if errors.Is(err, ErrNotFound) {
		// A deleted subject has no billing customer left to charge.
		return nil
	}
	if err != nil {
		return err
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
	// Overage counts when the subject reached its allowance with it on, has it
	// on now, or was already charged this month.
	if !subject.Overage && !limits.Overage && delivered.Millicents == 0 {
		return nil
	}
	snapshot, err := b.usage.UsageSnapshot(ctx, subject.SubjectID, at)
	if err != nil {
		return err
	}
	billed, target := OverageCharge(snapshot.ComputeUnitSeconds, max(limits.Allowance, subject.Allowance))
	if target <= delivered.Millicents {
		return nil
	}
	pending = OveragePendingStep{Provider: b.options.Provider, Live: b.options.Publish != nil, Step: OverageChargeStep{
		SubjectID: subject.SubjectID, PeriodStart: month, At: now,
		UsedUnitSeconds: snapshot.ComputeUnitSeconds, AllowanceSeconds: max(limits.Allowance, subject.Allowance),
		BilledUnitSeconds: billed, DeliveredMillicents: delivered.Millicents, TargetMillicents: target}}
	reserved, err := b.billing.ReserveOverage(ctx, pending)
	if err != nil || !reserved {
		// Another replica holds this month's step; it sends it.
		return err
	}
	return b.send(ctx, pending, summary)
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
		"previous_millicents", step.DeliveredMillicents, "target_millicents", step.TargetMillicents)
	return nil
}
