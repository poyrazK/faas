package api

import (
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EventDeliveryDeadlineWindowOptions selects total and/or reason-specific count alerts.
// Counts are independent; expired recipients never count as upcoming.
// ExpiredRatio optionally monitors expired / all pending recipients with deadlines.
type EventDeliveryDeadlineWindowOptions struct {
	ExpiredRatio    *EventDeliveryDeadlineRatioOptions
	Within          time.Duration
	Upcoming        int64
	Expired         int64
	UpcomingReasons map[string]int64
	ExpiredReasons  map[string]int64
	// Recovery counts are inclusive and must be below their enabled warning count.
	// Nil totals and omitted reason keys default to warning count minus one.
	UpcomingRecovery        *int64
	ExpiredRecovery         *int64
	UpcomingReasonsRecovery map[string]int64
	ExpiredReasonsRecovery  map[string]int64
}

func cloneDeadlineWindowOptions(o *EventDeliveryDeadlineWindowOptions) *EventDeliveryDeadlineWindowOptions {
	if o == nil {
		return nil
	}
	snapshot := *o
	snapshot.ExpiredRatio = cloneDeadlineRatioOptions(o.ExpiredRatio)
	snapshot.UpcomingReasons = maps.Clone(o.UpcomingReasons)
	snapshot.ExpiredReasons = maps.Clone(o.ExpiredReasons)
	snapshot.UpcomingReasonsRecovery = maps.Clone(o.UpcomingReasonsRecovery)
	snapshot.ExpiredReasonsRecovery = maps.Clone(o.ExpiredReasonsRecovery)
	if o.UpcomingRecovery != nil {
		value := *o.UpcomingRecovery
		snapshot.UpcomingRecovery = &value
	}
	if o.ExpiredRecovery != nil {
		value := *o.ExpiredRecovery
		snapshot.ExpiredRecovery = &value
	}
	return &snapshot
}
func validateDeadlineWindowOptions(o *EventDeliveryDeadlineWindowOptions) error {
	if o == nil {
		return nil
	}
	if o.Within%time.Millisecond != 0 || !ValidEventSubscriptionDeadlineWindow(o.Within.Milliseconds()) || o.Upcoming < 0 || o.Upcoming > EventSubscriptionPendingSummaryCountMax || o.Expired < 0 || o.Expired > EventSubscriptionPendingSummaryCountMax {
		return ErrValidation("deadline-window alerts require whole milliseconds 100..86400000 and bounded counts")
	}
	if err := validateDeadlineRatioOptions(o.ExpiredRatio); err != nil {
		return err
	}
	enabled := o.Upcoming > 0 || o.Expired > 0 || o.ExpiredRatio != nil
	known := NewEventPendingWaitingReasonCounts()
	for _, thresholds := range []map[string]int64{o.UpcomingReasons, o.ExpiredReasons} {
		for reason, count := range thresholds {
			if _, ok := known[reason]; !ok || count < 0 || count > EventSubscriptionPendingSummaryCountMax {
				return ErrValidation("deadline reason thresholds require known waiting reasons and counts 0..9007199254740991")
			}
			enabled = enabled || count > 0
		}
	}
	if !enabled {
		return ErrValidation("enable at least one total, reason-specific or expired-ratio deadline threshold")
	}
	return validateDeadlineWindowRecovery(o)
}
func validateDeadlineWindowRecovery(o *EventDeliveryDeadlineWindowOptions) error {
	for _, choice := range []struct {
		warning  int64
		recovery *int64
	}{{o.Upcoming, o.UpcomingRecovery}, {o.Expired, o.ExpiredRecovery}} {
		if choice.recovery != nil && (*choice.recovery < 0 || *choice.recovery >= choice.warning) {
			return ErrValidation("deadline recovery counts require an enabled warning and 0 <= recovery < warning")
		}
	}
	for _, choice := range []struct {
		warnings   map[string]int64
		recoveries map[string]int64
	}{{o.UpcomingReasons, o.UpcomingReasonsRecovery}, {o.ExpiredReasons, o.ExpiredReasonsRecovery}} {
		for reason, count := range choice.recoveries {
			if count < 0 || count >= choice.warnings[reason] {
				return ErrValidation("deadline reason recovery counts require an enabled known reason and 0 <= recovery < warning")
			}
		}
	}
	return nil
}
func deadlineRecoveryCount(warning int64, recovery *int64) int64 {
	if recovery != nil {
		return *recovery
	}
	return warning - 1
}
func deadlineCountWindow(metric string) (time.Duration, bool, string, bool) {
	raw, expired, ok := "", false, false
	if raw, ok = strings.CutPrefix(metric, "deadline_window.upcoming_recipients."); !ok {
		raw, ok = strings.CutPrefix(metric, "deadline_window.expired_recipients.")
		expired = true
	}
	if !ok {
		return 0, false, "", false
	}
	millisRaw, reason, hasReason := strings.Cut(raw, ".reason.")
	if hasReason {
		if _, known := NewEventPendingWaitingReasonCounts()[reason]; !known {
			return 0, false, "", false
		}
	}
	millis, err := strconv.ParseInt(millisRaw, 10, 64)
	if err != nil || !ValidEventSubscriptionDeadlineWindow(millis) || strconv.FormatInt(millis, 10) != millisRaw {
		return 0, false, "", false
	}
	return time.Duration(millis) * time.Millisecond, expired, reason, true
}
func newDeadlineWindowConditions(o *EventDeliveryDeadlineWindowOptions, sustain, recovery time.Duration) []*deliveryHealthCondition {
	if o == nil {
		return nil
	}
	out := []*deliveryHealthCondition{}
	for _, choice := range []struct {
		kind  string
		count int64
		clear *int64
	}{{"upcoming", o.Upcoming, o.UpcomingRecovery}, {"expired", o.Expired, o.ExpiredRecovery}} {
		metric := "deadline_window." + choice.kind + "_recipients." + strconv.FormatInt(o.Within.Milliseconds(), 10)
		if choice.count > 0 {
			out = append(out, newDeadlineWindowCountCondition(metric, choice.count, deadlineRecoveryCount(choice.count, choice.clear), sustain, recovery))
		}
	}
	// Preserve the existing total-condition order. Map insertion order cannot affect resume.
	for _, choice := range []struct {
		kind    string
		reasons map[string]int64
		clears  map[string]int64
	}{{"upcoming", o.UpcomingReasons, o.UpcomingReasonsRecovery}, {"expired", o.ExpiredReasons, o.ExpiredReasonsRecovery}} {
		reasons := make([]string, 0, len(choice.reasons))
		for reason, count := range choice.reasons {
			if count > 0 {
				reasons = append(reasons, reason)
			}
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			metric := "deadline_window." + choice.kind + "_recipients." + strconv.FormatInt(o.Within.Milliseconds(), 10) + ".reason." + reason
			clear := choice.reasons[reason] - 1
			if count, ok := choice.clears[reason]; ok {
				clear = count
			}
			out = append(out, newDeadlineWindowCountCondition(metric, choice.reasons[reason], clear, sustain, recovery))
		}
	}
	if o.ExpiredRatio != nil {
		out = append(out, newDeadlineRatioCondition(o, sustain, recovery))
	}
	return out
}
func newDeadlineWindowCountCondition(metric string, count, clear int64, sustain, recovery time.Duration) *deliveryHealthCondition {
	return &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: metric, State: "unknown", Threshold: float64(count), MinimumSamples: 2, SustainSeconds: sustain.Seconds(), RecoveryThreshold: float64(clear), RecoverySustainSeconds: recovery.Seconds()}}
}
func deadlineWindowCountValue(window EventSubscriptionDeadlineWindow, expired bool, reason string) (float64, bool) {
	if reason == "" {
		if expired {
			return float64(window.Summary.AlreadyExpiredCount), true
		}
		return float64(window.UpcomingRecipients), true
	}
	counts := window.UpcomingWaitingReasons
	if expired {
		counts = window.ExpiredWaitingReasons
	}
	count, ok := counts[reason]
	return float64(count), ok
}
