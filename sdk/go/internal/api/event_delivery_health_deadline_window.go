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
type EventDeliveryDeadlineWindowOptions struct {
	Within          time.Duration
	Upcoming        int64
	Expired         int64
	UpcomingReasons map[string]int64
	ExpiredReasons  map[string]int64
}

func cloneDeadlineWindowOptions(o *EventDeliveryDeadlineWindowOptions) *EventDeliveryDeadlineWindowOptions {
	if o == nil {
		return nil
	}
	snapshot := *o
	snapshot.UpcomingReasons = maps.Clone(o.UpcomingReasons)
	snapshot.ExpiredReasons = maps.Clone(o.ExpiredReasons)
	return &snapshot
}
func validateDeadlineWindowOptions(o *EventDeliveryDeadlineWindowOptions) error {
	if o == nil {
		return nil
	}
	if o.Within%time.Millisecond != 0 || !ValidEventSubscriptionDeadlineWindow(o.Within.Milliseconds()) || o.Upcoming < 0 || o.Upcoming > EventSubscriptionPendingSummaryCountMax || o.Expired < 0 || o.Expired > EventSubscriptionPendingSummaryCountMax {
		return ErrValidation("deadline-window alerts require whole milliseconds 100..86400000 and bounded counts")
	}
	enabled := o.Upcoming > 0 || o.Expired > 0
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
		return ErrValidation("enable at least one total or reason-specific deadline count threshold")
	}
	return nil
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
	}{{"upcoming", o.Upcoming}, {"expired", o.Expired}} {
		metric := "deadline_window." + choice.kind + "_recipients." + strconv.FormatInt(o.Within.Milliseconds(), 10)
		if choice.count > 0 {
			out = append(out, newDeadlineWindowCountCondition(metric, choice.count, sustain, recovery))
		}
	}
	// Preserve the existing total-condition order. Map insertion order cannot affect resume.
	for _, choice := range []struct {
		kind    string
		reasons map[string]int64
	}{{"upcoming", o.UpcomingReasons}, {"expired", o.ExpiredReasons}} {
		reasons := make([]string, 0, len(choice.reasons))
		for reason, count := range choice.reasons {
			if count > 0 {
				reasons = append(reasons, reason)
			}
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			metric := "deadline_window." + choice.kind + "_recipients." + strconv.FormatInt(o.Within.Milliseconds(), 10) + ".reason." + reason
			out = append(out, newDeadlineWindowCountCondition(metric, choice.reasons[reason], sustain, recovery))
		}
	}
	return out
}
func newDeadlineWindowCountCondition(metric string, count int64, sustain, recovery time.Duration) *deliveryHealthCondition {
	return &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: metric, State: "unknown", Threshold: float64(count), MinimumSamples: 2, SustainSeconds: sustain.Seconds(), RecoveryThreshold: float64(count - 1), RecoverySustainSeconds: recovery.Seconds()}}
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
