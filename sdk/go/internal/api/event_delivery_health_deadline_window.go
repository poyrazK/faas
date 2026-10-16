package api

import (
	"strconv"
	"strings"
	"time"
)

// EventDeliveryDeadlineWindowOptions selects upcoming and/or expired count alerts.
// Counts are independent; expired recipients never count as upcoming.
type EventDeliveryDeadlineWindowOptions struct {
	Within   time.Duration
	Upcoming int64
	Expired  int64
}

func validateDeadlineWindowOptions(o *EventDeliveryDeadlineWindowOptions) error {
	if o == nil {
		return nil
	}
	if o.Within%time.Millisecond != 0 || !ValidEventSubscriptionDeadlineWindow(o.Within.Milliseconds()) || o.Upcoming < 0 || o.Upcoming > EventSubscriptionPendingSummaryCountMax || o.Expired < 0 || o.Expired > EventSubscriptionPendingSummaryCountMax || o.Upcoming == 0 && o.Expired == 0 {
		return ErrValidation("deadline-window alerts require whole milliseconds 100..86400000 and at least one count 1..9007199254740991")
	}
	return nil
}
func deadlineCountWindow(metric string) (time.Duration, bool, bool) {
	raw, expired, ok := "", false, false
	if raw, ok = strings.CutPrefix(metric, "deadline_window.upcoming_recipients."); !ok {
		raw, ok = strings.CutPrefix(metric, "deadline_window.expired_recipients.")
		expired = true
	}
	if !ok {
		return 0, false, false
	}
	millis, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || !ValidEventSubscriptionDeadlineWindow(millis) || strconv.FormatInt(millis, 10) != raw {
		return 0, false, false
	}
	return time.Duration(millis) * time.Millisecond, expired, true
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
		if choice.count > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "deadline_window." + choice.kind + "_recipients." + strconv.FormatInt(o.Within.Milliseconds(), 10), State: "unknown", Threshold: float64(choice.count), MinimumSamples: 2, SustainSeconds: sustain.Seconds(), RecoveryThreshold: float64(choice.count - 1), RecoverySustainSeconds: recovery.Seconds()}})
		}
	}
	return out
}
