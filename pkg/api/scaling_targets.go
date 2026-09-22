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
	// ScalingMetricQueueLag targets the consumer group lag each worker should
	// drain.
	ScalingMetricQueueLag = "queue_lag"
	// ScalingMetricCustom targets a customer-pushed application metric
	// (ADR-202), named by ScalingTarget.Name. Like queue_depth and
	// queue_lag it is compared FLEET-WIDE: the value is the total backlog
	// one instance should carry, so desired = ceil(measured / target).
	//
	// This is the only metric whose reading Gregale does not measure
	// itself, which is the point — "unprocessed rows in my orders table"
	// is not derivable from request traffic, and an app can serve zero
	// requests while being catastrophically behind.
	ScalingMetricCustom = "custom"
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
		ScalingMetricQueueLag,
		ScalingMetricCustom,
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
		// The uniqueness key is metric+name, not metric alone: two custom
		// targets on DIFFERENT metrics are legitimate and common (an
		// orders backlog and a document backlog), while two on the same
		// name are the dead-config case the check exists for.
		key := t.Metric
		if t.Metric == ScalingMetricCustom {
			key += ":" + t.Name
		}
		if _, dup := seen[key]; dup {
			// Two targets on one metric have no defined meaning: the
			// arbiter takes a max, so the looser one is dead config the
			// author almost certainly believed was in effect.
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s declares %q more than once; each metric may appear at most once (custom metrics, once per name).",
					field, key))
		}
		seen[key] = struct{}{}
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
		// ADR-202: a name identifies WHICH pushed metric to watch, so it
		// is required for custom and meaningless anywhere else. Accepting
		// it on a platform-measured metric would store a field nothing
		// reads — the accepted-but-inert shape this codebase keeps having
		// to remove.
		if t.Metric == ScalingMetricCustom {
			if !validCustomMetricName(t.Name) {
				return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
					"Invalid scaling policy",
					fmt.Sprintf("%s[%d].name %q is required for metric %q and must match [a-z][a-z0-9_]{0,%d}.",
						field, i, t.Name, ScalingMetricCustom, CustomMetricNameMaxBytes-1))
			}
		} else if t.Name != "" {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].name is only valid with metric %q; %q is measured by the platform and has no name.",
					field, i, ScalingMetricCustom, t.Metric))
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
	if (t.Metric == ScalingMetricQueueDepth || t.Metric == ScalingMetricQueueLag) && t.Value <= 0 {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("target.value must be > 0 for %s; got %v.", t.Metric, t.Value))
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

// validCustomMetricName mirrors the app_custom_metrics_name_shape CHECK in
// the migration. Enforced in Go as well as in the database so a customer
// gets a 422 naming the rule rather than a constraint violation, and so the
// scaling policy and the pushed metric cannot disagree about what a legal
// name is.
func validCustomMetricName(name string) bool {
	if name == "" || len(name) > CustomMetricNameMaxBytes {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && ((r >= '0' && r <= '9') || r == '_'):
		default:
			return false
		}
	}
	return true
}

// ValidateCustomMetricName checks a pushed metric name against the same
// shape the app_custom_metrics CHECK enforces.
func ValidateCustomMetricName(name string) *Problem {
	if validCustomMetricName(name) {
		return nil
	}
	return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
		"Invalid custom metric",
		fmt.Sprintf("name %q must match [a-z][a-z0-9_]{0,%d}.", name, CustomMetricNameMaxBytes-1))
}

// ValidateCustomMetricValue bounds a pushed value.
//
// NaN is checked before the range comparison because every comparison
// against NaN is false, so a range check alone would pass it straight
// through to the scheduler, where it becomes the numerator of a capacity
// calculation. The upper bound exists because without one a single bad push
// demands the plan cap's worth of instances on the very next tick.
func ValidateCustomMetricValue(v float64) *Problem {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid custom metric", fmt.Sprintf("value must be a finite number; got %v.", v))
	case v < 0:
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid custom metric",
			fmt.Sprintf("value must be >= 0; a negative backlog has no meaning. Got %v.", v))
	case v > CustomMetricMaxValue:
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid custom metric",
			fmt.Sprintf("value must be <= %v; a larger reading is a broken producer, and "+
				"without this bound one bad push would demand the plan cap on the next tick. Got %v.",
				CustomMetricMaxValue, v))
	}
	return nil
}

// ErrCustomMetricLimitReached is the 422 for a push of a NEW name by an app
// already holding the maximum. A push to an EXISTING name is always accepted
// — it is an upsert and cannot grow the count.
func ErrCustomMetricLimitReached(limit int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeCustomMetricLimit,
		"Custom metric limit reached",
		fmt.Sprintf("this app already holds %d custom metrics, which is the maximum. "+
			"Pushing a new value for an existing metric always works; delete an unused "+
			"metric to free a slot. See "+docsBase+"/scaling-policy#custom-metrics",
			limit))
}
