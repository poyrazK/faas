package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type deliveryHealthDeadlineFlags struct {
	window            api.EventDeliveryDeadlineWindowOptions
	ratio             *deliveryHealthDeadlineRatioFlags
	sustain, recovery time.Duration
}

func newDeliveryHealthDeadlineFlags(fs *flag.FlagSet) *deliveryHealthDeadlineFlags {
	f := &deliveryHealthDeadlineFlags{}
	f.ratio = newDeliveryHealthDeadlineRatioFlags(fs)
	fs.DurationVar(&f.window.Within, "deadline-window", 0, "upcoming deadline horizon (whole milliseconds, 100ms-24h); required for deadline alerts")
	fs.Int64Var(&f.window.Upcoming, "deadline-upcoming-threshold", 0, "upcoming deadline count warning; zero disables")
	fs.Int64Var(&f.window.Expired, "deadline-expired-threshold", 0, "all expired deadline count warning; zero disables")
	fs.DurationVar(&f.sustain, "deadline-sustain", api.EventDeliveryRateRiskSustainDefault, "deadline warning confirmation duration (100ms-24h)")
	fs.DurationVar(&f.recovery, "deadline-recovery-sustain", api.EventDeliveryRateRiskRecoveryDefault, "deadline recovery confirmation duration (100ms-24h)")
	registerDeadlineRecovery(fs, "deadline-upcoming-recovery", &f.window.UpcomingRecovery)
	registerDeadlineRecovery(fs, "deadline-expired-recovery", &f.window.ExpiredRecovery)
	registerDeadlineReasonCounts(fs, "deadline-upcoming-reason", &f.window.UpcomingReasons)
	registerDeadlineReasonCounts(fs, "deadline-expired-reason", &f.window.ExpiredReasons)
	registerDeadlineReasonCounts(fs, "deadline-upcoming-reason-recovery", &f.window.UpcomingReasonsRecovery)
	registerDeadlineReasonCounts(fs, "deadline-expired-reason-recovery", &f.window.ExpiredReasonsRecovery)
	return f
}

func registerDeadlineRecovery(fs *flag.FlagSet, name string, target **int64) {
	fs.Func(name, "inclusive recovery count below enabled warning (omitted: warning minus one; zero requires zero)", func(raw string) error {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 || value > api.EventSubscriptionPendingSummaryCountMax {
			return fmt.Errorf("expected a count 0..9007199254740991")
		}
		if *target != nil {
			return fmt.Errorf("duplicate --%s", name)
		}
		*target = &value
		return nil
	})
}

func registerDeadlineReasonCounts(fs *flag.FlagSet, name string, target *map[string]int64) {
	fs.Func(name, "repeatable REASON=N deadline cohort count; recovery requires an enabled warning for that reason", func(value string) error {
		reason, raw, ok := strings.Cut(value, "=")
		count, err := strconv.ParseInt(raw, 10, 64)
		if !ok || err != nil || count < 0 || count > api.EventSubscriptionPendingSummaryCountMax {
			return fmt.Errorf("expected REASON=N with a count 0..9007199254740991")
		}
		if _, known := api.NewEventPendingWaitingReasonCounts()[reason]; !known {
			return fmt.Errorf("unknown waiting reason %q", reason)
		}
		if *target == nil {
			*target = map[string]int64{}
		}
		if _, exists := (*target)[reason]; exists {
			return fmt.Errorf("duplicate deadline reason %q in --%s", reason, name)
		}
		(*target)[reason] = count
		return nil
	})
}

func (f *deliveryHealthDeadlineFlags) options() (*api.EventDeliveryRateRiskOptions, error) {
	if f.window.Within%time.Millisecond != 0 || !api.ValidEventSubscriptionDeadlineWindow(f.window.Within.Milliseconds()) {
		return nil, fmt.Errorf("deadline alerts require --deadline-window in whole milliseconds, 100ms-24h")
	}
	if f.sustain < api.EventDeliveryHealthWatchThresholdMin || f.sustain > api.EventDeliveryHealthWatchThresholdMax || f.recovery < api.EventDeliveryHealthWatchThresholdMin || f.recovery > api.EventDeliveryHealthWatchThresholdMax {
		return nil, fmt.Errorf("deadline warning and recovery sustain must be 100ms-24h")
	}
	ratio, err := f.ratio.options()
	if err != nil {
		return nil, err
	}
	f.window.ExpiredRatio = ratio
	enabled := ratio != nil
	for _, cohort := range []struct {
		warning             int64
		recovery            *int64
		reasons, recoveries map[string]int64
	}{
		{f.window.Upcoming, f.window.UpcomingRecovery, f.window.UpcomingReasons, f.window.UpcomingReasonsRecovery},
		{f.window.Expired, f.window.ExpiredRecovery, f.window.ExpiredReasons, f.window.ExpiredReasonsRecovery},
	} {
		if cohort.warning < 0 || cohort.warning > api.EventSubscriptionPendingSummaryCountMax {
			return nil, fmt.Errorf("deadline warning counts must be 0..9007199254740991")
		}
		if cohort.recovery != nil && (*cohort.recovery < 0 || *cohort.recovery >= cohort.warning) {
			return nil, fmt.Errorf("deadline recovery requires an enabled warning and 0 <= recovery < warning")
		}
		enabled = enabled || cohort.warning > 0
		for _, count := range cohort.reasons {
			enabled = enabled || count > 0
		}
		for reason, count := range cohort.recoveries {
			if count >= cohort.reasons[reason] {
				return nil, fmt.Errorf("deadline recovery for %q requires an enabled warning and 0 <= recovery < warning", reason)
			}
		}
	}
	if !enabled {
		return nil, fmt.Errorf("deadline settings require at least one positive total, reason or ratio warning threshold")
	}
	return &api.EventDeliveryRateRiskOptions{DeadlineWindow: &f.window, Sustain: f.sustain, RecoverySustain: f.recovery}, nil
}

func printDeadlineHealthConditions(conditions []api.EventDeliveryHealthCondition) {
	for _, c := range conditions {
		if !strings.HasPrefix(c.Metric, "deadline_window.") || isDeadlineRatioMetric(c.Metric) {
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "Deadline %s: %s | last count %.0f | warning >= %.0f for %.3fs | recovery <= %.0f for %.3fs\n", oneLine(c.Metric), oneLine(c.State), c.Value, c.Threshold, c.SustainSeconds, c.RecoveryThreshold, c.RecoverySustainSeconds)
	}
}
