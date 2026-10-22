package state

import (
	"context"
	"errors"
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
// recorded price activations (ADR-1162). It is the largest compute grant whose
// activation precedes through, so a mid-month downgrade never shrinks the
// month's allowance and an upgrade applies from its recorded activation, not
// retroactively. Usage before the first recorded activation uses the earliest
// one. With no recorded activation the current plan applies, which is the
// legacy single-plan behaviour.
func MonthComputeIncludedMBSeconds(current api.Plan, snapshots []FinancialPriceSnapshot, through time.Time) int64 {
	var earliest *FinancialPriceSnapshot
	best := int64(-1)
	for i := range snapshots {
		s := &snapshots[i]
		if s.Price.Meter != "compute" {
			continue
		}
		if earliest == nil || s.EffectiveFrom.Before(earliest.EffectiveFrom) {
			earliest = s
		}
		if s.EffectiveFrom.Before(through) {
			best = max(best, s.Price.IncludedQuantity)
		}
	}
	switch {
	case best >= 0:
		return best
	case earliest != nil:
		return earliest.Price.IncludedQuantity
	default:
		return api.IncludedMBSeconds(current)
	}
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
