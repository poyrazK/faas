package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type deliveryHealthDeadlineRatioFlags struct {
	warning, recovery, minimum *int64
}

func newDeliveryHealthDeadlineRatioFlags(fs *flag.FlagSet) *deliveryHealthDeadlineRatioFlags {
	f := &deliveryHealthDeadlineRatioFlags{}
	registerDeadlinePercent(fs, "deadline-expired-ratio-threshold", "expired percentage warning (0.01..100, up to two decimal places); zero disables", &f.warning)
	registerDeadlinePercent(fs, "deadline-expired-ratio-recovery", "inclusive recovery percentage below warning; omitted defaults to warning minus 0.01 percentage points", &f.recovery)
	fs.Func("deadline-expired-ratio-min-recipients", "required minimum pending recipients with deadlines (1..9007199254740991)", func(raw string) error {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 || value > api.EventSubscriptionPendingSummaryCountMax {
			return fmt.Errorf("expected minimum recipients 1..9007199254740991")
		}
		if f.minimum != nil {
			return fmt.Errorf("duplicate --deadline-expired-ratio-min-recipients")
		}
		f.minimum = &value
		return nil
	})
	return f
}

func registerDeadlinePercent(fs *flag.FlagSet, name, description string, target **int64) {
	fs.Func(name, description, func(raw string) error {
		if *target != nil {
			return fmt.Errorf("duplicate --%s", name)
		}
		value, err := parseDeadlinePercent(raw)
		if err != nil {
			return err
		}
		*target = &value
		return nil
	})
}

// Convert decimal percentages directly to basis points without float rounding.
func parseDeadlinePercent(raw string) (int64, error) {
	whole, fraction, decimal := strings.Cut(raw, ".")
	invalid := func() (int64, error) {
		return 0, fmt.Errorf("expected decimal percentage 0..100 with at most two fractional digits")
	}
	if len(whole) < 1 || len(whole) > 3 || len(fraction) > 2 || decimal && fraction == "" {
		return invalid()
	}
	for _, digit := range whole + fraction {
		if digit < '0' || digit > '9' {
			return invalid()
		}
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return invalid()
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	part, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil || n*100+part > api.EventDeliveryDeadlineRatioBasisPointsMax {
		return invalid()
	}
	return n*100 + part, nil
}

func (f *deliveryHealthDeadlineRatioFlags) options() (*api.EventDeliveryDeadlineRatioOptions, error) {
	if f == nil {
		return nil, nil
	}
	if f.warning == nil || *f.warning == 0 {
		if f.recovery != nil || f.minimum != nil {
			return nil, fmt.Errorf("ratio recovery/minimum require an enabled --deadline-expired-ratio-threshold")
		}
		return nil, nil
	}
	if f.minimum == nil {
		return nil, fmt.Errorf("ratio alerts require --deadline-expired-ratio-min-recipients")
	}
	if f.recovery != nil && *f.recovery >= *f.warning {
		return nil, fmt.Errorf("ratio recovery percentage must be strictly below warning")
	}
	return &api.EventDeliveryDeadlineRatioOptions{WarningBasisPoints: *f.warning, RecoveryBasisPoints: f.recovery, MinimumRecipients: *f.minimum}, nil
}

func isDeadlineRatioMetric(metric string) bool {
	return strings.HasPrefix(metric, "deadline_window.expired_ratio_basis_points.")
}

func printDeadlineRatioHealth(out api.EventDeliveryHealthWatchResult) {
	for _, c := range out.Conditions {
		if !isDeadlineRatioMetric(c.Metric) {
			continue
		}
		_, raw, ok := strings.Cut(c.Metric, ".minimum_recipients.")
		minimum, err := strconv.ParseInt(raw, 10, 64)
		if !ok || err != nil || minimum < 1 || minimum > api.EventSubscriptionPendingSummaryCountMax || strconv.FormatInt(minimum, 10) != raw {
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "Deadline ratio %s: %s | warning >= %.2f%% for %.3fs | recovery <= %.2f%% for %.3fs | minimum cohort %d\n", oneLine(c.Metric), oneLine(c.State), c.Threshold/100, c.SustainSeconds, c.RecoveryThreshold/100, c.RecoverySustainSeconds, minimum)
		e := out.RateEvidence
		if e == nil || e.Status != "eligible" || !e.WindowEvidenceAvailable || e.DeadlineWindow == nil || !api.EventSubscriptionDeadlineWindowCountsComplete(*e.DeadlineWindow) {
			_, _ = fmt.Fprintln(osStdout, "  Observed ratio unavailable | cohort unavailable; inspect confirmation diagnostics")
			continue
		}
		s := e.DeadlineWindow.Summary
		ratio := "undefined (zero denominator)"
		if s.DeadlineRecipients > 0 {
			ratio = fmt.Sprintf("~%.4f%%", float64(s.AlreadyExpiredCount)/float64(s.DeadlineRecipients)*100)
		}
		cohort := "qualifies for ratio evaluation"
		if s.DeadlineRecipients < minimum {
			cohort = "below minimum; confirmation unknown"
		}
		_, _ = fmt.Fprintf(osStdout, "  Observed ratio %s | cohort %d expired / %d pending with deadlines | %s | observed %s; confirmation requires fresh advancing evidence\n", ratio, s.AlreadyExpiredCount, s.DeadlineRecipients, cohort, s.ObservedAt.Format(time.RFC3339Nano))
	}
}

func printDeadlineRatioAlert(a api.EventDeliveryHealthAlert) error {
	_, err := fmt.Fprintf(os.Stderr, "%s %s | app %s | subscription %s | incident %s | sequence %d | value ~%.4f%% | warning >= %.2f%% | recovery <= %.2f%% | %s\n", a.Metric, a.Transition, a.AppID, a.SubscriptionID, a.IncidentID, a.Sequence, a.Value/100, a.Threshold/100, a.RecoveryThreshold/100, a.Reason)
	return err
}
