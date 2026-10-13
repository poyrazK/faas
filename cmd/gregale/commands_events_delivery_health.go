package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsSubscriptionDeliveryHealth(args []string) int {
	flags, positional := splitArgsForFlags(args, "watch", "alerts-json", "resume")
	fs := newFlagSet("events subscription-delivery-health", flag.ContinueOnError)
	window := fs.String("window", "5m", "observation window: 5m|15m|1h|6h|24h")
	watchFlags := newDeliveryHealthWatchFlags(fs)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events subscription-delivery-health <app> <subscription-id> [--window 5m]", "events")
		return 1
	}
	if _, err := api.EventConsumerHealthWindow(*window); err != nil {
		return printErr("Invalid window", err)
	}
	if err := watchFlags.validate(fs); err != nil {
		return printErr("Invalid watch flags", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *watchFlags.watch {
		return runDeliveryHealthWatch(client, positional[0], positional[1], *window, watchFlags)
	}
	out, err := client.GetEventConsumerDeliveryHealth(context.Background(), positional[0], positional[1], *window)
	if err != nil {
		return printErr("Consumer delivery health failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printEventConsumerDeliveryHealth(out)
	return 0
}

func printEventConsumerDeliveryHealth(out api.EventConsumerDeliveryHealth) {
	r, e, sample := out.Routing, out.Execution, out.PendingSample
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | pending routing %d | processing routing %d | oldest pending %.0fs\n", oneLine(r.SubscriptionID), r.PendingRecipients, r.ProcessingRecipients, r.OldestAgeSeconds)
	_, _ = fmt.Fprintf(osStdout, "Execution history %s (%s) | collection start %v | evidence lost %v\n", oneLine(e.HistoryStatus), oneLine(e.HistoryReason), e.CoverageStartedAt, e.CoverageLostAt)
	_, _ = fmt.Fprintf(osStdout, "Window evidence %s (%s) | scope %s | collection start %v | lost through %v | retain until %v\n", oneLine(e.WindowHistoryStatus), oneLine(e.WindowHistoryReason), oneLine(e.WindowCoverage), e.WindowCoverageStartedAt, e.WindowCoverageLostThroughAt, e.WindowEvidenceRetainUntil)
	_, _ = fmt.Fprintf(osStdout, "Retained executions: queued %d | running %d | retrying %d | dead letters %d | truncated %t | history complete %t\n", e.Queued, e.Running, e.Retrying, e.DeadLettered, e.Truncated, e.HistoryComplete)
	if e.LatestSuccessfulCompletionAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Latest retained successful completion: %s\n", e.LatestSuccessfulCompletionAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	} else {
		_, _ = fmt.Fprintln(osStdout, "Latest retained successful completion: unavailable")
	}
	_, _ = fmt.Fprintf(osStdout, "Controls: paused %t | configured rate %d/s\n", r.Paused, r.RatePerSecond)
	if r.CircuitBreaker != nil {
		_, _ = fmt.Fprintf(osStdout, "Circuit: %s | reason %s\n", oneLine(r.CircuitBreaker.State), oneLine(r.CircuitBreaker.Reason))
	}
	if reasons := out.WaitingReasons; reasons != nil {
		_, _ = fmt.Fprintf(osStdout, "Complete pending reasons: %v | pending %d | complete %t | observed %s\n", reasons.Counts, reasons.PendingRecipients, reasons.Complete, reasons.ObservedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if counts := out.CapacityWaits; counts != nil {
		_, _ = fmt.Fprintf(osStdout, "Capacity waits: consumer %d | app %d | account %d | complete %t | observed %s\n", counts.Consumer, counts.App, counts.Account, counts.Complete, counts.ObservedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	_, _ = fmt.Fprintf(osStdout, "Oldest pending sample: %d | truncated %t | waiting reasons %v\n", len(sample.Recipients), sample.Truncated, sample.WaitingReasons)
	if len(sample.Recipients) > 0 {
		first := sample.Recipients[0]
		_, _ = fmt.Fprintf(osStdout, "Oldest sampled event: source %s | ID %s | accepted %s | blocker %s\n", oneLine(first.EventSource), oneLine(first.EventID), first.AcceptedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), oneLine(first.WaitingReason))
	}
	_, _ = fmt.Fprintf(osStdout, "Independent reads: routing %s | execution %s | pending %s | routing history compacted %t\n", r.ObservedAt.Format("2006-01-02T15:04:05Z07:00"), e.ObservedAt.Format("2006-01-02T15:04:05Z07:00"), sample.ObservedAt.Format("2006-01-02T15:04:05Z07:00"), r.HistoryCompacted)
}

type deliveryHealthWatchFlags struct {
	watch, alertsJSON *bool
	resume            *bool
	checkpoint        *string
	ackInbox          *string
	options           api.EventDeliveryHealthWatchOptions
	slo               api.EventDeliveryHealthSLOOptions
}

func newDeliveryHealthWatchFlags(fs *flag.FlagSet) *deliveryHealthWatchFlags {
	f := &deliveryHealthWatchFlags{}
	f.watch = fs.Bool("watch", false, "watch configured delivery health thresholds")
	f.checkpoint = fs.String("checkpoint", "", "persist a bounded delivery health watch checkpoint")
	f.ackInbox = fs.String("ack-inbox", "", "atomically replaced JSON acknowledgement batch; requires checkpoint")
	f.resume = fs.Bool("resume", false, "resume the selected checkpoint with fresh evidence")
	f.alertsJSON = fs.Bool("alerts-json", false, "emit local lifecycle alert JSON lines on stderr")
	fs.DurationVar(&f.options.Interval, "interval", api.EventDeliveryHealthWatchIntervalDefault, "watch polling interval (100ms-1m)")
	fs.DurationVar(&f.options.Timeout, "timeout", api.EventDeliveryHealthWatchTimeoutDefault, "watch deadline (up to 24h)")
	fs.DurationVar(&f.options.MaxAge, "max-age", api.EventDeliveryHealthWatchAgeDefault, "maximum component sample age (100ms-24h)")
	fs.DurationVar(&f.options.OldestPending, "oldest-pending-threshold", 0, "pending acceptance age threshold; zero disables")
	fs.Int64Var(&f.options.Retrying, "retrying-threshold", 0, "retained retrying execution count threshold; zero disables")
	fs.Int64Var(&f.options.DeadLetters, "dead-letters-threshold", 0, "retained dead-letter execution count threshold; zero disables")
	fs.DurationVar(&f.options.Paused, "pause-threshold", 0, "subscription pause duration threshold; zero disables")
	fs.DurationVar(&f.options.CircuitBlocked, "circuit-threshold", 0, "locally sampled circuit restriction duration; zero disables")
	fs.DurationVar(&f.options.CapacityBlocked, "capacity-threshold", 0, "locally sampled capacity wait duration; zero disables")
	fs.Func("waiting-reason-count", "repeatable REASON=N pending recipient threshold", func(value string) error {
		reason, raw, ok := strings.Cut(value, "=")
		n, err := strconv.ParseInt(raw, 10, 64)
		if !ok || err != nil || n <= 0 || n > api.EventDeliveryHealthWatchCountMax {
			return fmt.Errorf("expected REASON=N with a positive bounded count")
		}
		if _, known := api.NewEventPendingWaitingReasonCounts()[reason]; !known {
			return fmt.Errorf("unknown waiting reason %q", reason)
		}
		if f.options.WaitingReasonCounts == nil {
			f.options.WaitingReasonCounts = map[string]int64{}
		}
		if _, exists := f.options.WaitingReasonCounts[reason]; exists {
			return fmt.Errorf("duplicate waiting reason count %q", reason)
		}
		f.options.WaitingReasonCounts[reason] = n
		return nil
	})
	fs.Func("waiting-reason-duration", "repeatable REASON=DURATION sustained nonzero wait threshold", func(value string) error {
		reason, raw, ok := strings.Cut(value, "=")
		d, err := time.ParseDuration(raw)
		if !ok || err != nil || d < api.EventDeliveryHealthWatchThresholdMin || d > api.EventDeliveryHealthWatchThresholdMax {
			return fmt.Errorf("expected REASON=DURATION (100ms-24h)")
		}
		if _, known := api.NewEventPendingWaitingReasonCounts()[reason]; !known {
			return fmt.Errorf("unknown waiting reason %q", reason)
		}
		if f.options.WaitingReasonDurations == nil {
			f.options.WaitingReasonDurations = map[string]time.Duration{}
		}
		if _, exists := f.options.WaitingReasonDurations[reason]; exists {
			return fmt.Errorf("duplicate waiting reason duration %q", reason)
		}
		f.options.WaitingReasonDurations[reason] = d
		return nil
	})
	fs.DurationVar(&f.slo.RoutingLatency, "slo-routing-latency", 0, "recorded routing p95 latency target; zero disables")
	fs.DurationVar(&f.slo.CompletionLatency, "slo-completion-latency", 0, "successful completion p95 target; incomplete history is unknown")
	fs.Float64Var(&f.slo.RetryRatePerSecond, "slo-retry-rate", 0, "recorded routing retries/second target; zero disables")
	fs.Float64Var(&f.slo.DeadLetterRatePerSecond, "slo-dead-letter-rate", 0, "dead letters/second target; incomplete history is unknown")
	fs.Float64Var(&f.slo.RoutingFailureBudgetPct, "slo-routing-failure-budget", 0, "recorded terminal routing failure budget percentage; zero disables")
	fs.Int64Var(&f.slo.MinimumSamples, "slo-min-samples", api.EventDeliveryHealthSLOMinimumSamplesDefault, "minimum window samples for each SLO")
	fs.DurationVar(&f.slo.Sustain, "slo-sustain", api.EventDeliveryHealthSLOSustainDefault, "consecutive fresh breach duration before SLO alert")
	return f
}

func (f *deliveryHealthWatchFlags) validate(fs *flag.FlagSet) error {
	var err error
	sloSelected, sloTarget := false, false
	fs.Visit(func(option *flag.Flag) {
		if strings.HasPrefix(option.Name, "slo-") {
			sloSelected = true
			if option.Name != "slo-min-samples" && option.Name != "slo-sustain" {
				sloTarget = true
			}
		}
		if !*f.watch && option.Name != "window" && option.Name != "watch" {
			err = fmt.Errorf("--%s requires --watch", option.Name)
		}
	})
	if sloSelected && !sloTarget {
		return fmt.Errorf("SLO sample/sustain settings require an SLO target")
	}
	if sloSelected {
		f.options.SLO = &f.slo
	}
	if *f.ackInbox != "" && *f.checkpoint == "" {
		return fmt.Errorf("--ack-inbox requires --checkpoint")
	}
	if *f.resume && *f.checkpoint == "" {
		return fmt.Errorf("--resume requires --checkpoint")
	}
	return err
}

func runDeliveryHealthWatch(client *api.Client, app, sub, window string, f *deliveryHealthWatchFlags) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	options := f.options
	options.Window = window
	checkpoint, err := prepareDeliveryHealthCheckpoint(f, &options)
	if err != nil {
		return printErr("Cannot use delivery health checkpoint", err)
	}
	if checkpoint != nil {
		defer func() { _ = checkpoint.close() }()
	}
	options.OnProgress = func(progress api.EventDeliveryHealthWatchResult) error {
		if checkpoint != nil {
			if progress.Checkpoint == nil {
				return fmt.Errorf("missing delivery health checkpoint")
			}
			if err := api.ValidateEventDeliveryHealthCheckpoint(*progress.Checkpoint); err != nil {
				return err
			}
			if err := checkpoint.save(progress.Checkpoint); err != nil {
				return err
			}
		}
		for _, alert := range progress.Alerts {
			if *f.alertsJSON {
				if err := json.NewEncoder(os.Stderr).Encode(alert); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintf(os.Stderr, "%s %s | app %s | subscription %s | incident %s | sequence %d | value %.3f | threshold %.3f | %s\n", alert.Metric, alert.Transition, alert.AppID, alert.SubscriptionID, alert.IncidentID, alert.Sequence, alert.Value, alert.Threshold, alert.Reason); err != nil {
					return err
				}
			}
		}
		return nil
	}
	result, err := client.WatchEventConsumerDeliveryHealth(ctx, app, sub, options)
	if checkpoint != nil && result.Checkpoint != nil && result.StopReason != "validation_failure" && result.StopReason != "callback_failure" {
		if saveErr := checkpoint.save(result.Checkpoint); saveErr != nil {
			return printErr("Cannot save delivery health checkpoint", saveErr)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		if result.Health != nil {
			printEventConsumerDeliveryHealth(*result.Health)
		}
		for _, v := range result.SLO {
			_, _ = fmt.Fprintf(osStdout, "SLO %s: %s (%s) | value %.6f | target %.6f | samples %d/%d | coverage %s | budget burn %.3fx\n", v.Metric, v.Status, v.Reason, v.Value, v.Threshold, v.Samples, v.MinimumSamples, v.Coverage, v.ErrorBudgetBurnRate)
		}
		_, _ = fmt.Fprintf(osStdout, "Watch stopped: %s | observations %d | alert sequence %d\n", result.StopReason, result.Observations, result.Sequence)
	}
	if err != nil {
		return printErr("Delivery health watch stopped", err)
	}
	return 0
}
