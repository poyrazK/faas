package api

import (
	"math"
	"strings"
	"time"
)

// EventDeliveryHealthSLOOptions bounds observed window metrics. Rate targets are
// outcomes per second, not unique-event percentages or a historical error budget.
type EventDeliveryHealthSLOOptions struct {
	RoutingLatency, CompletionLatency           time.Duration
	RetryRatePerSecond, DeadLetterRatePerSecond float64
	RoutingFailureBudgetPct                     float64
	MinimumSamples                              int64
	Sustain                                     time.Duration
}

type EventDeliveryHealthSLOObservation struct {
	Metric              string    `json:"metric"`
	Value               float64   `json:"value"`
	Threshold           float64   `json:"threshold"`
	Samples             int64     `json:"samples"`
	MinimumSamples      int64     `json:"minimum_samples"`
	Coverage            string    `json:"coverage"`
	Status              string    `json:"status"`
	Reason              string    `json:"reason"`
	ObservedAt          time.Time `json:"observed_at"`
	WindowStart         time.Time `json:"window_start"`
	ErrorBudgetBurnRate float64   `json:"error_budget_burn_rate,omitempty"`
}

func (o EventDeliveryHealthSLOOptions) validate() error {
	if o.MinimumSamples < 1 || o.MinimumSamples > EventDeliveryHealthWatchCountMax || o.Sustain < EventDeliveryHealthWatchThresholdMin || o.Sustain > EventDeliveryHealthWatchThresholdMax {
		return ErrValidation("SLO requires a positive bounded minimum sample count and sustain duration (100ms-24h)")
	}
	if !finiteSLOValue(o.RoutingFailureBudgetPct) || o.RoutingFailureBudgetPct > EventDeliveryHealthSLOPercentageMax || (o.RoutingFailureBudgetPct > 0 && o.RoutingFailureBudgetPct < EventDeliveryHealthSLOPercentageMin) {
		return ErrValidation("invalid routing failure budget percentage")
	}
	enabled := o.RoutingFailureBudgetPct > 0
	for _, d := range []time.Duration{o.RoutingLatency, o.CompletionLatency} {
		if d < 0 || d > EventDeliveryHealthWatchThresholdMax || (d > 0 && d < EventDeliveryHealthWatchThresholdMin) {
			return ErrValidation("invalid SLO latency target")
		}
		enabled = enabled || d > 0
	}
	for _, v := range []float64{o.RetryRatePerSecond, o.DeadLetterRatePerSecond} {
		if !finiteSLOValue(v) || v > float64(EventDeliveryHealthWatchCountMax) {
			return ErrValidation("invalid SLO outcome rate target")
		}
		enabled = enabled || v > 0
	}
	if !enabled {
		return ErrValidation("enable at least one SLO target")
	}
	return nil
}

func finiteSLOValue(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

func sloTargets(o EventDeliveryHealthSLOOptions) []EventDeliveryHealthSLOObservation {
	out := []EventDeliveryHealthSLOObservation{}
	for _, t := range []struct {
		metric    string
		threshold float64
	}{
		{"slo.routing_latency_p95_seconds", o.RoutingLatency.Seconds()},
		{"slo.completion_latency_p95_seconds", o.CompletionLatency.Seconds()},
		{"slo.retry_rate_per_second", o.RetryRatePerSecond},
		{"slo.dead_letter_rate_per_second", o.DeadLetterRatePerSecond},
		{"slo.routing_failure_pct", o.RoutingFailureBudgetPct},
	} {
		if t.threshold > 0 {
			out = append(out, EventDeliveryHealthSLOObservation{Metric: t.metric, Threshold: t.threshold, MinimumSamples: o.MinimumSamples})
		}
	}
	return out
}

// EvaluateEventConsumerDeliverySLO evaluates retained window evidence only.
// Freshness and consecutive breach timing are enforced by the watch separately.
func EvaluateEventConsumerDeliverySLO(h EventConsumerDeliveryHealth, o EventDeliveryHealthSLOOptions) ([]EventDeliveryHealthSLOObservation, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	out := sloTargets(o)
	for i := range out {
		out[i] = deliveryHealthSLOObservation(h, out[i])
	}
	return out, nil
}

func sloSampleTotal(values ...int64) (int64, bool) {
	var n int64
	for _, v := range values {
		if v < 0 || v > EventDeliveryHealthWatchCountMax-n {
			return 0, false
		}
		n += v
	}
	return n, true
}

func deliveryHealthSLOObservation(h EventConsumerDeliveryHealth, out EventDeliveryHealthSLOObservation) EventDeliveryHealthSLOObservation {
	r, e := h.Routing, h.Execution
	out.Status, out.Reason = "unknown", "evidence_unavailable"
	valid := true
	switch out.Metric {
	case "slo.routing_latency_p95_seconds", "slo.retry_rate_per_second", "slo.routing_failure_pct":
		out.Coverage, out.ObservedAt, out.WindowStart = r.Coverage, r.ObservedAt, r.WindowStart
		valid = r.Coverage == "bounded_recorded_outcomes" && !r.HistoryCompacted
		if out.Metric == "slo.routing_latency_p95_seconds" {
			out.Value = r.RoutingLatencyP95Seconds
			out.Samples = r.SuccessfulRoutes
			valid = valid && r.SuccessfulRoutes >= 0 && r.SuccessfulRoutes <= EventDeliveryHealthWatchCountMax
		} else {
			var countsValid bool
			if out.Metric == "slo.routing_failure_pct" {
				out.Samples, countsValid = sloSampleTotal(r.SuccessfulRoutes, r.TerminalFailures)
				if countsValid && out.Samples > 0 {
					out.Value = EventDeliveryHealthSLOPercentageMax * float64(r.TerminalFailures) / float64(out.Samples)
					out.ErrorBudgetBurnRate = out.Value / out.Threshold
				}
			} else {
				out.Samples, countsValid = sloSampleTotal(r.SuccessfulRoutes, r.TerminalFailures, r.RetryScheduled)
				if r.ObservedAt.After(r.WindowStart) {
					out.Value = float64(r.RetryScheduled) / r.ObservedAt.Sub(r.WindowStart).Seconds()
				}
			}
			valid = valid && countsValid
		}
		if r.HistoryCompacted {
			out.Reason = "history_compacted"
		}
	case "slo.completion_latency_p95_seconds", "slo.dead_letter_rate_per_second":
		out.Coverage, out.ObservedAt, out.WindowStart = e.Coverage, e.ObservedAt, e.WindowStart
		valid = e.CompleteWindowEvidence()
		if e.WindowCoverage != "" {
			out.Coverage = e.WindowCoverage
		}
		if out.Metric == "slo.completion_latency_p95_seconds" {
			out.Value = e.CompletionLatencyP95Seconds
			out.Samples = e.WindowCompletions
			valid = valid && e.WindowCompletions >= 0 && e.WindowCompletions <= EventDeliveryHealthWatchCountMax && (e.WindowCoverage != "" || e.WindowCompletions <= e.Succeeded)
		} else {
			var countsValid bool
			out.Samples, countsValid = sloSampleTotal(e.SuccessfulAttempts, e.FailedAttempts)
			valid = valid && countsValid && e.WindowDeadLetters >= 0 && e.WindowDeadLetters <= e.FailedAttempts
			if e.ObservedAt.After(e.WindowStart) {
				out.Value = float64(e.WindowDeadLetters) / e.ObservedAt.Sub(e.WindowStart).Seconds()
			}
		}
		if !e.CompleteWindowEvidence() {
			if e.WindowCoverage != "" {
				out.Reason = e.WindowHistoryReason
			} else {
				out.Reason = "history_incomplete"
				if e.HistoryReason != "" {
					out.Reason = e.HistoryReason
				}
			}
		} else if e.Truncated && e.WindowCoverage == "" {
			out.Reason = "history_truncated"
		}
	}
	valid = valid && r.AppID != "" && r.SubscriptionID != "" && strings.EqualFold(r.AppID, e.AppID) && strings.EqualFold(r.SubscriptionID, e.SubscriptionID) && !out.WindowStart.IsZero() && out.ObservedAt.After(out.WindowStart) && out.ObservedAt.Sub(out.WindowStart) <= EventConsumerHealthMaxWindow && finiteSLOValue(out.Value)
	if !finiteSLOValue(out.Value) {
		out.Value = 0
	}
	if !valid {
		return out
	}
	if out.Samples < out.MinimumSamples {
		out.Reason = "insufficient_samples"
		return out
	}
	out.Status, out.Reason = "eligible", "window_evidence_available"
	return out
}

func updateDeliveryHealthSLOSustain(c *deliveryHealthCondition, value float64, now time.Time) bool {
	if value < c.Threshold {
		c.first = time.Time{}
		c.samples = 0
		return false
	}
	if c.first.IsZero() {
		c.first = now
		c.samples = 0
	}
	c.samples++
	return c.samples >= 2 && now.Sub(c.first).Seconds() >= c.SustainSeconds
}
