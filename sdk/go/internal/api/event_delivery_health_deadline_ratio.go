package api

import (
	"math"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// EventDeliveryDeadlineRatioOptions measures expired / all pending recipients with deadlines.
// WarningBasisPoints is inclusive (100 basis points = 1%). Nil recovery defaults
// to warning minus one; explicit zero requires no expired work in a qualifying cohort.
type EventDeliveryDeadlineRatioOptions struct {
	WarningBasisPoints  int64
	RecoveryBasisPoints *int64
	MinimumRecipients   int64
}

func cloneDeadlineRatioOptions(o *EventDeliveryDeadlineRatioOptions) *EventDeliveryDeadlineRatioOptions {
	if o == nil {
		return nil
	}
	snapshot := *o
	if o.RecoveryBasisPoints != nil {
		value := *o.RecoveryBasisPoints
		snapshot.RecoveryBasisPoints = &value
	}
	return &snapshot
}
func validateDeadlineRatioOptions(o *EventDeliveryDeadlineRatioOptions) error {
	if o == nil {
		return nil
	}
	if o.WarningBasisPoints < 1 || o.WarningBasisPoints > EventDeliveryDeadlineRatioBasisPointsMax || o.MinimumRecipients < 1 || o.MinimumRecipients > EventSubscriptionPendingSummaryCountMax {
		return ErrValidation("expired-ratio alerts require warning basis points 1..10000 and minimum recipients 1..9007199254740991")
	}
	if o.RecoveryBasisPoints != nil && (*o.RecoveryBasisPoints < 0 || *o.RecoveryBasisPoints >= o.WarningBasisPoints) {
		return ErrValidation("expired-ratio recovery basis points require 0 <= recovery < warning")
	}
	return nil
}
func deadlineRatioWindow(metric string) (time.Duration, int64, bool) {
	raw, ok := strings.CutPrefix(metric, "deadline_window.expired_ratio_basis_points.")
	if !ok {
		return 0, 0, false
	}
	horizon, minimum, ok := strings.Cut(raw, ".minimum_recipients.")
	if !ok {
		return 0, 0, false
	}
	millis, err := strconv.ParseInt(horizon, 10, 64)
	if err != nil || !ValidEventSubscriptionDeadlineWindow(millis) || strconv.FormatInt(millis, 10) != horizon {
		return 0, 0, false
	}
	count, err := strconv.ParseInt(minimum, 10, 64)
	if err != nil || count < 1 || count > EventSubscriptionPendingSummaryCountMax || strconv.FormatInt(count, 10) != minimum {
		return 0, 0, false
	}
	return time.Duration(millis) * time.Millisecond, count, true
}
func newDeadlineRatioCondition(o *EventDeliveryDeadlineWindowOptions, sustain, recovery time.Duration) *deliveryHealthCondition {
	metric := "deadline_window.expired_ratio_basis_points." + strconv.FormatInt(o.Within.Milliseconds(), 10) + ".minimum_recipients." + strconv.FormatInt(o.ExpiredRatio.MinimumRecipients, 10)
	return newDeadlineWindowCountCondition(metric, o.ExpiredRatio.WarningBasisPoints, deadlineRecoveryCount(o.ExpiredRatio.WarningBasisPoints, o.ExpiredRatio.RecoveryBasisPoints), sustain, recovery)
}
func deadlineRatioValue(window EventSubscriptionDeadlineWindow, minimum int64) (float64, bool) {
	total, expired := window.Summary.DeadlineRecipients, window.Summary.AlreadyExpiredCount
	if minimum < 1 || total < minimum || total > EventSubscriptionPendingSummaryCountMax || expired < 0 || expired > total {
		return 0, false
	}
	// Counts fit 53 bits, but multiplication by the basis-point scale needs 128.
	hi, lo := bits.Mul64(uint64(expired), uint64(EventDeliveryDeadlineRatioBasisPointsMax))
	whole, remainder := bits.Div64(hi, lo, uint64(total))
	value := float64(whole)
	if remainder == 0 {
		return value, true
	}
	value += float64(remainder) / float64(total)
	// Preserve strict interior membership between adjacent integer boundaries.
	// Warning/recovery thresholds are integers, so neither division nor JSON's
	// round-trip float representation can cross an exact policy boundary.
	if value <= float64(whole) {
		value = math.Nextafter(float64(whole), math.Inf(1))
	}
	if value >= float64(whole+1) {
		value = math.Nextafter(float64(whole+1), math.Inf(-1))
	}
	return value, true
}
func validRateRiskValue(metric string, value float64) bool {
	if !finiteSLOValue(value) || value > rateRiskMetricValueMax(metric) {
		return false
	}
	_, _, ratio := deadlineRatioWindow(metric)
	return ratio || math.Trunc(value) == value
}

const EventDeliveryDeadlineRatioBasisPointsMax int64 = 10000
