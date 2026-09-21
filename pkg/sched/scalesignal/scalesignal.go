// Package scalesignal is the arbiter for multi-signal autoscaling (ADR-194).
//
// An app declares a list of scaling targets — `{metric, value}` pairs. Each
// one is an independent statement of how much load one instance should carry.
// This package turns a set of measured observations into a single desired
// replica count by evaluating every target and taking the maximum. That is
// the whole "combine" primitive: the developer lists signals, the platform
// owns the algorithm, and there is no per-metric behaviour policy to design.
//
// It imports pkg/api for the closed metric set and nothing else. In
// particular it imports neither pkg/sched/targets nor pkg/sched/scaleup:
// those two cannot import each other — both doc comments record the cycle,
// and both re-declare AdmitResult locally to avoid it — so an arbiter they
// share must sit below both.
//
// Each trigger arbitrates over the targets it can actually observe, and the
// two run as separate arms of one select in the schedd loop. They serialize,
// so they cannot double-admit: whichever runs first admits, and the other
// re-reads the raised concurrency before it arbitrates. Across a tick pair
// the result is the max over all declared signals, which is the ADR-194
// contract.
package scalesignal

import (
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

// Class is how a metric relates to capacity. The distinction is load-bearing
// and must not be collapsed into one formula — see desiredFor.
type Class int

const (
	// ClassPerInstanceRate measures work per instance per unit time.
	// Total demand is measured × concurrency, so capacity divides.
	ClassPerInstanceRate Class = iota
	// ClassBacklog measures an absolute queue the whole fleet drains.
	// Total demand is the reading itself; concurrency does not multiply it.
	ClassBacklog
	// ClassSaturation measures how hard an instance is working, not how
	// much work it is doing. It cannot be divided into a capacity count.
	ClassSaturation
)

// classes assigns a capacity class to every metric api.ScalingMetrics()
// accepts. The two maps are checked against each other by
// TestEveryDeclaredMetricHasASource: a metric the validator accepts but this
// map does not classify is a metric no trigger can read, which is exactly the
// phantom ADR-194 exists to make impossible.
var classes = map[string]Class{
	api.ScalingMetricRPS:                ClassPerInstanceRate,
	api.ScalingMetricConcurrentRequests: ClassPerInstanceRate,
	api.ScalingMetricQueueDepth:         ClassBacklog,
	api.ScalingMetricQueueLag:           ClassBacklog,
	api.ScalingMetricCPU:                ClassSaturation,
}

// ClassOf reports the capacity class of a metric, and whether the metric is
// in the closed set at all.
func ClassOf(metric string) (Class, bool) {
	c, ok := classes[metric]
	return c, ok
}

// Observation is one declared target paired with its current reading.
//
// Measured carries the natural unit of the metric's class: per-instance for
// ClassPerInstanceRate and ClassSaturation, fleet-absolute for ClassBacklog.
// Have is false when the trigger has no current sample — a cold app, a nil
// reader, or a ring buffer that has not filled. An observation without a
// sample is never hot; the platform does not admit on a missing signal.
type Observation struct {
	Metric   string
	Target   float64
	Measured float64
	Have     bool
}

// Hot reports whether this observation alone calls for more capacity.
//
// The comparison is strictly greater than the target for every class. The
// strictness matters: an instance sitting exactly at its target is performing
// as declared, and the next unit of work can ride on it. Only exceeding the
// target means the next unit would push it past what the developer asked for.
func (o Observation) Hot(concurrency int) bool {
	class, ok := ClassOf(o.Metric)
	if !ok || !o.Have || o.Target <= 0 {
		return false
	}
	if class == ClassBacklog {
		// A backlog target is per-drainer, so the fleet's capacity to
		// absorb it scales with the number of drainers. A depth of 40
		// across 5 workers with a target of 10 is not hot.
		workers := concurrency
		if workers < 1 {
			// A parked app has no drainers, but any backlog at all must
			// still be able to wake one. Treating concurrency 0 as 1
			// makes "depth > target" the cold-start condition.
			workers = 1
		}
		return o.Measured > o.Target*float64(workers)
	}
	return o.Measured > o.Target
}

// desiredFor converts one hot observation into a desired replica count.
//
// The per-class arithmetic is deliberately not uniform:
//
//   - ClassPerInstanceRate: the reading is what ONE instance carries, so
//     total demand is measured × concurrency and the count that meets the
//     target is that total over the target. Ceil, so a fractional remainder
//     still gets an instance rather than being rounded into an SLO miss.
//     At concurrency 0 there is no fleet to multiply, so the cold start is
//     one instance and the next tick measures the real per-instance load.
//
//   - ClassBacklog: the reading is already fleet-total. Multiplying by
//     concurrency would square the demand — the bug this separate branch
//     exists to prevent.
//
//   - ClassSaturation: a 90% CPU reading does not mean "provision 90/70
//     instances". CPU has no linear relationship to capacity once a runtime
//     starts contending for it, and a ratio here would multiply the fleet off
//     one noisy sample. It contributes a single step instead, and the next
//     tick re-measures. This preserves exactly what scaleup.decide already
//     did with the legacy CPU column.
func (o Observation) desiredFor(concurrency int) int {
	class, ok := ClassOf(o.Metric)
	if !ok {
		return 0
	}
	switch class {
	case ClassBacklog:
		return int(math.Ceil(o.Measured / o.Target))
	case ClassSaturation:
		return concurrency + 1
	default: // ClassPerInstanceRate
		if concurrency <= 0 {
			return 1
		}
		return int(math.Ceil(o.Measured * float64(concurrency) / o.Target))
	}
}

// Result is the arbitrated outcome across every declared target.
type Result struct {
	// Hot is true when at least one observation exceeded its target.
	// When false, Desired is meaningless and the caller must not admit.
	Hot bool
	// Desired is the replica count that satisfies the most demanding
	// declared target. Always at least concurrency+1 when Hot, so a hot
	// signal always makes progress; not yet bounded by the plan cap or
	// the per-tick burst, which stay the caller's policy.
	Desired int
	// Winner is the metric that produced Desired. Carried for the
	// decision log and the scale-up metric label, so an operator can see
	// WHICH signal drove an admission rather than only that one did.
	Winner string
}

// Arbitrate evaluates every observation and returns the maximum desired
// count across the hot ones.
//
// Callers pass their own live concurrency; the result is not clamped to a
// cap here because the plan ceiling, ScalingPolicy.MaxInstances and the
// per-tick burst bound differ per trigger and are applied at the call site
// where the ledger is in hand.
func Arbitrate(concurrency int, obs []Observation) Result {
	out := Result{}
	for _, o := range obs {
		if !o.Hot(concurrency) {
			continue
		}
		desired := o.desiredFor(concurrency)
		if desired <= concurrency {
			// Every class must make progress when hot. A rate metric can
			// land here through integer truncation on a small fleet, and a
			// backlog whose depth is below the fleet's total capacity is
			// filtered by Hot before it reaches this point.
			desired = concurrency + 1
		}
		if !out.Hot || desired > out.Desired {
			out.Hot = true
			out.Desired = desired
			out.Winner = o.Metric
		}
	}
	return out
}
