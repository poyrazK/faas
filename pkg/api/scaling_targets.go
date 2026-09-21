package api

import (
	"fmt"
	"math"
	"net/http"
	"strings"
)

// The closed scaling-metric set (ADR-194).
//
// Every name here is backed by a live source and read by a scheduler trigger.
// That is enforced, not merely intended: pkg/sched/scalesignal classifies each
// of these, and TestEveryDeclaredMetricHasASource fails the build when a name
// is added here without a trigger that reads it. Two names spent releases in
// this set with no reader at all — "rps", which only ever worked through the
// legacy autoscale_target_rps column, and "p99_latency_ms", which had no
// source in any release — and that is the failure this coupling prevents.
const (
	// ScalingMetricRPS targets per-instance requests per second.
	ScalingMetricRPS = "rps"
	// ScalingMetricCPU targets the max per-instance CPU percentage.
	ScalingMetricCPU = "cpu"
	// ScalingMetricConcurrentRequests targets the max per-instance
	// in-flight request count.
	ScalingMetricConcurrentRequests = "concurrent_requests"
	// ScalingMetricQueueDepth targets the backlog each worker should
	// drain. Unlike the others this is compared fleet-wide: the fleet is
	// hot when depth exceeds target × workers.
	ScalingMetricQueueDepth = "queue_depth"
)

// ScalingMetricP99LatencyMS was in the closed set before ADR-194 and had no
// source in any release: no trigger implemented a latency axis and no
// component computed a per-app p99 for scaling. It is rejected on write now.
// The constant remains so the rejection path and the tests that pin it can
// name the value without a string literal drifting out of sync.
const ScalingMetricP99LatencyMS = "p99_latency_ms"

// ScalingMetrics returns the closed set in a stable order. Error messages
// build their "legal values" list from this so the message cannot drift from
// what the validator accepts.
func ScalingMetrics() []string {
	return []string{
		ScalingMetricRPS,
		ScalingMetricCPU,
		ScalingMetricConcurrentRequests,
		ScalingMetricQueueDepth,
	}
}

// ValidScalingMetric reports whether metric is in the closed set. The empty
// string is NOT valid here: an empty Metric means "no target declared", which
// callers express by omitting the entry, and accepting it inside a list would
// let `targets: [{}]` read as a configured policy.
func ValidScalingMetric(metric string) bool {
	for _, m := range ScalingMetrics() {
		if m == metric {
			return true
		}
	}
	return false
}

// ValidateScalingTargets checks a declared target list against the closed
// metric set and the per-metric value rules. Returns nil when the list is
// legal, and a 422 *Problem otherwise.
//
// This is the single implementation. The manifest loader, the PATCH handler
// and the deploy-diff quota gate all call it rather than repeating the switch
// — they had three copies of the metric set before ADR-194 and all three
// agreed on a set that the scheduler did not implement.
//
// `field` names the caller's surface ("target" or "targets") so the message
// points at what the user actually wrote.
func ValidateScalingTargets(field string, targets []ScalingTarget) *Problem {
	if len(targets) == 0 {
		return nil
	}
	if len(targets) > MaxScalingTargets {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("%s declares %d entries; at most %d are allowed.",
				field, len(targets), MaxScalingTargets))
	}
	seen := make(map[string]struct{}, len(targets))
	for i, t := range targets {
		if t.Metric == ScalingMetricP99LatencyMS {
			// Named explicitly rather than falling through to "not in the
			// closed set", because an operator who configured it is
			// entitled to know it never did anything and what to use now.
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].metric=%q is no longer accepted: the platform has never had a latency source to scale on. "+
					"Use %q instead — queueing on a saturated instance is what drives p99 up, and in-flight requests measure it directly.",
					field, i, ScalingMetricP99LatencyMS, ScalingMetricConcurrentRequests))
		}
		if !ValidScalingMetric(t.Metric) {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].metric=%q is not in the closed set (%s).",
					field, i, t.Metric, strings.Join(ScalingMetrics(), ", ")))
		}
		if _, dup := seen[t.Metric]; dup {
			// Two targets on one metric have no defined meaning: the
			// arbiter takes a max, so the looser one is dead config the
			// author almost certainly believed was in effect.
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s declares metric %q more than once; each metric may appear at most once.",
					field, t.Metric))
		}
		seen[t.Metric] = struct{}{}
		// NaN and +Inf must be caught before the range check, because
		// `NaN <= 0` is false and a NaN would sail through it into the
		// arbiter, where it becomes the divisor of a capacity calculation.
		if math.IsNaN(t.Value) || math.IsInf(t.Value, 0) {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].value must be a finite number; got %v.", field, i, t.Value))
		}
		// Every metric in the set is a positive quantity per instance.
		// Zero is rejected rather than read as "disabled" because a list
		// entry is already an explicit declaration — omitting it is how
		// you disable a signal — and because a zero divisor would make
		// the arbiter's capacity arithmetic infinite.
		if t.Value <= 0 {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].value must be > 0 for %s; got %v.",
					field, i, t.Metric, t.Value))
		}
		if t.Metric == ScalingMetricCPU && t.Value > 100 {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].value must be <= 100 for %s (it is a percentage); got %v.",
					field, i, ScalingMetricCPU, t.Value))
		}
	}
	return nil
}

// ValidateLegacyScalingTarget checks the singular `target` field.
//
// It is deliberately LOOSER than ValidateScalingTargets, and the difference
// is compatibility rather than oversight. The singular field has always
// accepted an empty Metric (the "fall back to the legacy columns" state) and
// a zero Value — `{"metric": "rps", "value": 0}` is documented in the apid
// handler as a shape that round-trips, and tightening it would reject stored
// policies on their next PATCH. A list entry has neither excuse: it is a
// fresh surface where an author who wants a signal off simply omits it.
//
// The one rule ADR-194 does apply here is the p99 rejection, which is not a
// tightening — that metric never had a source, so no app can be relying on
// its behaviour.
func ValidateLegacyScalingTarget(t *ScalingTarget) *Problem {
	if t == nil {
		return nil
	}
	if t.Metric == ScalingMetricP99LatencyMS {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("target.metric=%q is no longer accepted: the platform has never had a latency source to scale on. "+
				"Use %q instead — queueing on a saturated instance is what drives p99 up, and in-flight requests measure it directly.",
				ScalingMetricP99LatencyMS, ScalingMetricConcurrentRequests))
	}
	if t.Metric != "" && !ValidScalingMetric(t.Metric) {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("target.metric=%q is not in the closed set (%s).",
				t.Metric, strings.Join(ScalingMetrics(), ", ")))
	}
	if math.IsNaN(t.Value) || math.IsInf(t.Value, 0) {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("target.value must be a finite number; got %v.", t.Value))
	}
	if t.Value < 0 {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("target.value must be >= 0; got %v.", t.Value))
	}
	if t.Metric == ScalingMetricQueueDepth && t.Value <= 0 {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("target.value must be > 0 for queue_depth; got %v.", t.Value))
	}
	return nil
}

// ErrScalingTargetConflict rejects a policy that sets both the singular
// `target` and the plural `targets`. Guessing a precedence would silently
// discard one of two things the author explicitly wrote.
func ErrScalingTargetConflict() *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
		"Invalid scaling policy",
		"scaling policy sets both target and targets; use targets (target is the single-signal form of the same field).")
}
