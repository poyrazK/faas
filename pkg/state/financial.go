package state

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

var ErrFinancialAllocationLimit = errors.New("financial allocation limit exceeded")

type FinancialUsageRecord struct {
	Sequence         int64              `json:"sequence"`
	InstanceID       string             `json:"instance_id"`
	Plan             api.Plan           `json:"plan"`
	Unit             string             `json:"unit"`
	PriceVersion     string             `json:"price_version,omitempty"`
	AdjustmentActor  string             `json:"adjustment_actor,omitempty"`
	AdjustmentReason string             `json:"adjustment_reason,omitempty"`
	Evidence         financial.Evidence `json:"evidence"`
}

type FinancialPriceSnapshot struct {
	AccountID     string          `json:"account_id"`
	PeriodStart   time.Time       `json:"period_start"`
	PeriodEnd     time.Time       `json:"period_end"`
	Price         financial.Price `json:"price"`
	Plan          api.Plan        `json:"plan"`
	EffectiveFrom time.Time       `json:"effective_from"`
	DeliveryMode  string          `json:"delivery_mode"`
	RecordedAt    time.Time       `json:"recorded_at"`
}

// MonthComputeIncludedMBSeconds returns the compute allowance (mb_seconds)
// that applies to a calendar month's usage up to through, from that month's
// recorded price activations (ADR-1162, amended by ADR-20261030093512). Each
// plan's monthly allowance counts for the share of the month it held, and the
// plan active at through is taken to hold until the month ends: one plan all
// month grants its whole allowance, while a few minutes on a larger plan grant
// a few minutes' share of it. An activation applies from its recorded minute,
// never retroactively, and the earliest one also covers the month before it.
// With no recorded activation the current plan applies, which is the legacy
// single-plan behaviour.
func MonthComputeIncludedMBSeconds(current api.Plan, snapshots []FinancialPriceSnapshot, through time.Time) int64 {
	var compute []FinancialPriceSnapshot
	for _, s := range snapshots {
		if s.Price.Meter == "compute" {
			compute = append(compute, s)
		}
	}
	if len(compute) == 0 {
		return api.IncludedMBSeconds(current)
	}
	slices.SortStableFunc(compute, func(a, b FinancialPriceSnapshot) int {
		if c := a.EffectiveFrom.Compare(b.EffectiveFrom); c != 0 {
			return c
		}
		return a.RecordedAt.Compare(b.RecordedAt)
	})
	active := 1
	for active < len(compute) && compute[active].EffectiveFrom.Before(through) {
		active++
	}
	compute = compute[:active]
	monthStart, monthEnd := compute[0].PeriodStart.UTC(), compute[0].PeriodEnd.UTC()
	if monthStart.IsZero() {
		first := compute[0].EffectiveFrom.UTC()
		monthStart = time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	if !monthEnd.After(monthStart) {
		monthEnd = monthStart.AddDate(0, 1, 0)
	}
	month := int64(monthEnd.Sub(monthStart) / time.Second)
	// Whole seconds keep the products in int64: the largest allowance in
	// mb_seconds times a month of seconds is below 1e17.
	clamp := func(t time.Time) time.Time {
		switch {
		case t.Before(monthStart):
			return monthStart
		case t.After(monthEnd):
			return monthEnd
		}
		return t.UTC()
	}
	var weighted int64
	for i, s := range compute {
		from, until := monthStart, monthEnd
		if i > 0 {
			from = clamp(s.EffectiveFrom)
		}
		if i+1 < len(compute) {
			until = clamp(compute[i+1].EffectiveFrom)
		}
		if until.After(from) {
			weighted += s.Price.IncludedQuantity * int64(until.Sub(from)/time.Second)
		}
	}
	return weighted / month
}

// monthOverageCents is the compute overage, in cents, the provider pusher
// bills for the month's usage so far (MonthBilledComputeMBSeconds), so the
// spend cap, schedd's wake gate, the dashboard and the provider agree.
// meterd records a plan change within a minute; until it has, the current
// plan counts from now.
func monthOverageCents(current api.Plan, snapshots []FinancialPriceSnapshot, now time.Time, mbSeconds int64, usageBefore func(time.Time) (int64, error)) (int64, error) {
	billed, err := MonthBilledComputeMBSeconds(current, withCurrentPlan(current, snapshots, now), now, mbSeconds, usageBefore)
	if err != nil {
		return 0, err
	}
	return api.OverageCentsForBillableMBSeconds(billed), nil
}

// MonthBilledComputeMBSeconds returns the compute overage (mb_seconds) the
// provider pusher bills for a month's usage before through, given usage, the
// month's usage before through, and usageBefore(t), its usage before t.
//
// The pusher bills each hour above the allowance in force at that hour
// (MonthComputeIncludedMBSeconds through the hour's end) and never revisits an
// hour: after a downgrade, usage the larger allowance covered is not
// back-billed; after an upgrade, overage already billed is not credited
// (ADR-20261030093512). The allowance is constant between plan activations,
// so the month splits at each activation's hour into segments, and each
// segment bills its usage above that segment's allowance. Without an
// activation after the first this is usage above the allowance, and
// usageBefore is not called. The spend cap compares this figure, not usage
// above today's allowance, so a plan change can neither overrun the cap nor
// spend it on usage that is never billed (ADR-20261031100000).
func MonthBilledComputeMBSeconds(current api.Plan, snapshots []FinancialPriceSnapshot, through time.Time, usage int64, usageBefore func(time.Time) (int64, error)) (int64, error) {
	var activations []time.Time
	var first time.Time
	for _, s := range snapshots {
		if s.Price.Meter != "compute" {
			continue
		}
		if first.IsZero() || s.EffectiveFrom.Before(first) {
			first = s.EffectiveFrom
		}
		activations = append(activations, s.EffectiveFrom)
	}
	// An hour uses an activation from the hour containing it on.
	var bounds []time.Time
	for _, at := range activations {
		if at.Equal(first) {
			continue
		}
		hour := at.UTC().Truncate(time.Hour)
		if hour.Before(through) && !slices.ContainsFunc(bounds, hour.Equal) {
			bounds = append(bounds, hour)
		}
	}
	slices.SortFunc(bounds, func(a, b time.Time) int { return a.Compare(b) })
	segmentStart := time.Time{}
	usageAtStart := int64(0)
	var billed int64
	for i := 0; i <= len(bounds); i++ {
		end, usageAtEnd := through, usage
		if i < len(bounds) {
			end = bounds[i]
			var err error
			if usageAtEnd, err = usageBefore(end); err != nil {
				return 0, err
			}
		}
		// The first segment's hours end by its end; a later segment's hours
		// use its own activation from its first hour on.
		allowanceAt := end
		if i > 0 {
			allowanceAt = segmentStart.Add(time.Hour)
		}
		included := MonthComputeIncludedMBSeconds(current, snapshots, allowanceAt)
		billed += api.BillableMBSecondsAbove(included, usageAtEnd) - api.BillableMBSecondsAbove(included, usageAtStart)
		segmentStart, usageAtStart = end, usageAtEnd
	}
	return billed, nil
}

// MonthPlanAt returns the plan whose compute activation applies to usage up to
// through, by the rule MonthComputeIncludedMBSeconds uses: the latest recorded
// activation before through, or the earliest when none is. With no recorded
// activation it is current. The provider pusher bills no hour whose plan is
// Free (ADR-20261101143527).
func MonthPlanAt(current api.Plan, snapshots []FinancialPriceSnapshot, through time.Time) api.Plan {
	var active *FinancialPriceSnapshot
	for i := range snapshots {
		s := &snapshots[i]
		if s.Price.Meter != "compute" || s.Plan == "" {
			continue
		}
		switch {
		case active == nil:
			active = s
		case s.EffectiveFrom.Before(through) && (!active.EffectiveFrom.Before(through) || laterActivation(s, active)):
			active = s
		case !active.EffectiveFrom.Before(through) && !s.EffectiveFrom.Before(through) && laterActivation(active, s):
			active = s // none applies yet: the earliest covers the month before it
		}
	}
	if active == nil {
		return current
	}
	return active.Plan
}

func laterActivation(a, b *FinancialPriceSnapshot) bool {
	if c := a.EffectiveFrom.Compare(b.EffectiveFrom); c != 0 {
		return c > 0
	}
	return a.RecordedAt.After(b.RecordedAt)
}

// withCurrentPlan adds current as an activation just before now when the
// latest recorded activation is another plan's.
func withCurrentPlan(current api.Plan, snapshots []FinancialPriceSnapshot, now time.Time) []FinancialPriceSnapshot {
	var latest *FinancialPriceSnapshot
	for i := range snapshots {
		s := &snapshots[i]
		if s.Price.Meter == "compute" && s.EffectiveFrom.Before(now) && (latest == nil || !s.EffectiveFrom.Before(latest.EffectiveFrom)) {
			latest = s
		}
	}
	if latest == nil || latest.Plan == "" || latest.Plan == current {
		return snapshots
	}
	activated := *latest
	activated.Plan, activated.EffectiveFrom, activated.RecordedAt = current, now.Add(-time.Second), now
	activated.Price = financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(current)}
	return append(slices.Clone(snapshots), activated)
}

// FinancialStore is the additive ADR-566 store seam. ThroughID fixes an
// immutable read snapshot across pages even while meterd appends new evidence.
type FinancialStore interface {
	FinancialEvidenceHead(context.Context, string, time.Time, time.Time) (int64, error)
	FinancialEvidenceCoverage(context.Context) (time.Time, error)
	ListFinancialUsageEvidence(context.Context, string, time.Time, time.Time, int64, int64, int) ([]FinancialUsageRecord, error)
	PutFinancialPriceSnapshot(context.Context, FinancialPriceSnapshot) (FinancialPriceSnapshot, error)
	ListFinancialPriceSnapshots(context.Context, string, time.Time) ([]FinancialPriceSnapshot, error)
	RecordFinancialSamplingWindow(context.Context, time.Time, bool, bool) error
	FinancialSamplingCoverage(context.Context, time.Time, time.Time) (FinancialSamplingCoverage, error)
	// FinancialCompletedComputeMinutes lists the minutes in [start,end) that
	// meterd recorded as compute-complete, oldest first. meterd re-bills only
	// closed minutes missing from this list (ADR-790).
	FinancialCompletedComputeMinutes(context.Context, time.Time, time.Time) ([]time.Time, error)
	AggregateFinancialUsage(context.Context, string, time.Time, time.Time, int64) ([]FinancialUsageAggregate, error)
	AppendFinancialAdjustment(context.Context, FinancialAdjustment) (FinancialUsageRecord, error)
}

type FinancialAdjustment struct {
	AccountID        string
	SourceID         string
	CorrectsSourceID string
	Quantity         int64
	Actor            string
	Reason           string
}

func validFinancialAdjustment(a FinancialAdjustment) bool {
	return uuid.Validate(a.AccountID) == nil && strings.TrimSpace(a.SourceID) != "" && len(a.SourceID) <= api.FinancialSourceIDBytes && !strings.HasPrefix(a.SourceID, "usage:") && strings.TrimSpace(a.CorrectsSourceID) != "" && len(a.CorrectsSourceID) <= api.FinancialSourceIDBytes && a.SourceID != a.CorrectsSourceID && a.Quantity < 0 && strings.TrimSpace(a.Actor) != "" && len(a.Actor) <= api.FinancialAdjustmentActorBytes && strings.TrimSpace(a.Reason) != "" && len(a.Reason) <= api.FinancialAdjustmentReasonBytes
}

type FinancialUsageAggregate struct {
	PriceVersion string
	Plan         api.Plan
	Unit         string
	Meter        string
	Attribution  financial.Attribution
	Quantity     int64
	SourceCount  int64
}

type FinancialSamplingCoverage struct {
	ComputeMinutes int64     `json:"compute_minutes"`
	EgressMinutes  int64     `json:"egress_minutes"`
	ObservedAt     time.Time `json:"observed_at"`
}

func validFinancialSamplingWindow(start, end time.Time) bool {
	return !start.IsZero() && !start.After(end) && end.Sub(start) <= 32*24*time.Hour && start.Equal(start.Truncate(time.Minute)) && end.Equal(end.Truncate(time.Minute))
}

func validFinancialWindow(account string, start, end time.Time) bool {
	return account != "" && !start.IsZero() && start.Before(end) && end.Sub(start) <= 32*24*time.Hour
}

// validStoredFinancialPrice validates a persisted price snapshot. Snapshots
// written before the USD switch carry the legacy currency and stay valid
// history (ADR-1430); every other rule is the one a new snapshot must meet.
// Without this, one pre-switch row made the whole period unreadable and
// meterd's pricing capture never became ready (rc.279 rollout).
func validStoredFinancialPrice(p FinancialPriceSnapshot) bool {
	if p.Price.Currency == financial.LegacyBillingCurrency {
		p.Price.Currency = financial.BillingCurrency
	}
	return validFinancialPrice(p)
}

func validFinancialPrice(p FinancialPriceSnapshot) bool {
	start := p.PeriodStart.UTC()
	if !p.Plan.Valid() || p.EffectiveFrom.Before(start) || !p.EffectiveFrom.Before(p.PeriodEnd) || (p.DeliveryMode != "live" && p.DeliveryMode != "shadow" && p.DeliveryMode != "off") {
		return false
	}
	if !validFinancialWindow(p.AccountID, start, p.PeriodEnd) || start.Day() != 1 || start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 || start.Nanosecond() != 0 || !p.PeriodEnd.Equal(start.AddDate(0, 1, 0)) || len(p.Price.Meter) > 64 || len(p.Price.Version) > 128 {
		return false
	}
	_, err := financial.CostMeter(p.AccountID, start, p.PeriodEnd, p.Price, nil)
	return err == nil
}
