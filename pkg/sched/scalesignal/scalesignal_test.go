// adr: 194 — multi-signal scaling targets.
package scalesignal

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestEveryDeclaredMetricHasASource is the gate ADR-194 exists to install.
//
// Before it, the closed metric set in pkg/api was a list of names that
// validation accepted and nothing consumed. "rps" was accepted through
// scaling.target for releases while pkg/sched/scaleup read only the legacy
// autoscale_target_rps column, and "p99_latency_ms" had no source at all —
// both validated, persisted, round-tripped through the API, rendered in
// deploy diff, and scaled nothing.
//
// The coupling this test pins is simple: a metric the validator accepts must
// be classified here, because the classification is what every trigger uses
// to turn a reading into a desired instance count. Adding a name to
// api.ScalingMetrics() without teaching the arbiter what it means now fails
// the build instead of shipping a contract the platform does not honour.
func TestEveryDeclaredMetricHasASource(t *testing.T) {
	for _, metric := range api.ScalingMetrics() {
		if _, ok := ClassOf(metric); !ok {
			t.Errorf("api.ScalingMetrics() accepts %q but scalesignal does not classify it: "+
				"no trigger can read this metric, so declaring it would validate and do nothing. "+
				"Either add it to scalesignal.classes with a real source, or remove it from the closed set.", metric)
		}
	}
	// And the converse: a classified metric the validator rejects is dead
	// code in the arbiter, which is the same drift pointing the other way.
	for metric := range classes {
		if !api.ValidScalingMetric(metric) {
			t.Errorf("scalesignal classifies %q but api.ValidScalingMetric rejects it: "+
				"no app can ever declare this metric, so the arbitration branch is unreachable.", metric)
		}
	}
	// p99_latency_ms is the specific phantom ADR-194 removed. Pin it so a
	// future contributor re-adding it has to confront this test.
	if api.ValidScalingMetric(api.ScalingMetricP99LatencyMS) {
		t.Errorf("%q is back in the closed set; it has no source. See ADR-194.", api.ScalingMetricP99LatencyMS)
	}
}

func TestArbitrate_PerInstanceRateDividesTotalDemand(t *testing.T) {
	// 4 instances each carrying 30 in-flight against a target of 10 is 120
	// total demand, which needs 12 instances.
	res := Arbitrate(4, []Observation{{
		Metric: api.ScalingMetricConcurrentRequests, Target: 10, Measured: 30, Have: true,
	}})
	if !res.Hot || res.Desired != 12 {
		t.Fatalf("Arbitrate = %+v, want hot with desired 12", res)
	}
}

func TestArbitrate_BacklogDoesNotMultiplyByConcurrency(t *testing.T) {
	// A backlog of 90 with a per-worker budget of 10 needs 9 workers — not
	// 9 x concurrency. This is the bug the separate ClassBacklog branch
	// exists to prevent; with the rate formula it would demand 27.
	res := Arbitrate(3, []Observation{{
		Metric: api.ScalingMetricQueueDepth, Target: 10, Measured: 90, Have: true,
	}})
	if !res.Hot || res.Desired != 9 {
		t.Fatalf("Arbitrate = %+v, want hot with desired 9 (depth/target, NOT depth*concurrency/target)", res)
	}
}

func TestArbitrate_BacklogIsHotOnlyAboveFleetCapacity(t *testing.T) {
	// 5 workers with a budget of 10 each absorb 50. A depth of 40 is
	// within capacity and must not scale.
	cold := Arbitrate(5, []Observation{{
		Metric: api.ScalingMetricQueueDepth, Target: 10, Measured: 40, Have: true,
	}})
	if cold.Hot {
		t.Fatalf("depth 40 across 5 workers at budget 10 = %+v, want not hot", cold)
	}
	// A parked app must still wake on any backlog: concurrency 0 counts as
	// one drainer, so depth 11 > 10 is the cold-start condition.
	wake := Arbitrate(0, []Observation{{
		Metric: api.ScalingMetricQueueDepth, Target: 10, Measured: 11, Have: true,
	}})
	if !wake.Hot || wake.Desired != 2 {
		t.Fatalf("parked app with depth 11 = %+v, want hot with desired 2", wake)
	}
}

func TestArbitrate_SaturationStepsRatherThanScales(t *testing.T) {
	// 95% CPU against a 50% target must NOT provision 95/50 x concurrency.
	// CPU is not a capacity measurement; it contributes one step and the
	// next tick re-measures.
	res := Arbitrate(4, []Observation{{
		Metric: api.ScalingMetricCPU, Target: 50, Measured: 95, Have: true,
	}})
	if !res.Hot || res.Desired != 5 {
		t.Fatalf("Arbitrate = %+v, want hot with desired 5 (concurrency+1), not a ratio", res)
	}
}

func TestArbitrate_TakesTheMaxAcrossSignals(t *testing.T) {
	// This is the "Gregale combines the signals itself" contract: the
	// developer declares three targets and never writes a rule. CPU is hot
	// (one step -> 3), rps is hot and demands 4, in-flight is hot and
	// demands 8. The fleet is provisioned for the most demanding.
	res := Arbitrate(2, []Observation{
		{Metric: api.ScalingMetricCPU, Target: 70, Measured: 90, Have: true},
		{Metric: api.ScalingMetricRPS, Target: 50, Measured: 100, Have: true},
		{Metric: api.ScalingMetricConcurrentRequests, Target: 10, Measured: 40, Have: true},
	})
	if !res.Hot || res.Desired != 8 {
		t.Fatalf("Arbitrate = %+v, want desired 8", res)
	}
	if res.Winner != api.ScalingMetricConcurrentRequests {
		t.Fatalf("Winner = %q, want the metric that produced the max", res.Winner)
	}
}

func TestArbitrate_MissingReadingNeverAdmits(t *testing.T) {
	// A declared signal with no sample must not scale on its own, and must
	// not suppress a different signal that does have one. Both halves
	// matter: the first is "never admit blindly", the second is what makes
	// a list of targets robust to one unavailable source.
	alone := Arbitrate(2, []Observation{
		{Metric: api.ScalingMetricQueueDepth, Target: 10, Measured: 999, Have: false},
	})
	if alone.Hot {
		t.Fatalf("observation with Have=false = %+v, want not hot", alone)
	}
	withSibling := Arbitrate(2, []Observation{
		{Metric: api.ScalingMetricQueueDepth, Target: 10, Measured: 999, Have: false},
		{Metric: api.ScalingMetricCPU, Target: 70, Measured: 90, Have: true},
	})
	if !withSibling.Hot || withSibling.Winner != api.ScalingMetricCPU {
		t.Fatalf("Arbitrate = %+v, want the readable signal to still win", withSibling)
	}
}

func TestArbitrate_AtTargetIsNotHot(t *testing.T) {
	// Strictly greater than, for every class. An instance sitting exactly
	// at its declared target is performing as asked and the next unit of
	// work can ride on it.
	for _, o := range []Observation{
		{Metric: api.ScalingMetricRPS, Target: 50, Measured: 50, Have: true},
		{Metric: api.ScalingMetricCPU, Target: 70, Measured: 70, Have: true},
		{Metric: api.ScalingMetricConcurrentRequests, Target: 10, Measured: 10, Have: true},
	} {
		if res := Arbitrate(3, []Observation{o}); res.Hot {
			t.Errorf("%s exactly at target = %+v, want not hot", o.Metric, res)
		}
	}
}

func TestArbitrate_HotSignalAlwaysMakesProgress(t *testing.T) {
	// A rate barely over target on a large fleet can round back down to
	// the current count. Hot must always add at least one instance, or the
	// app stays saturated forever while reporting an admit.
	res := Arbitrate(10, []Observation{{
		Metric: api.ScalingMetricRPS, Target: 100, Measured: 100.01, Have: true,
	}})
	if !res.Hot || res.Desired <= 10 {
		t.Fatalf("Arbitrate = %+v, want desired > 10", res)
	}
}

func TestArbitrate_UnknownMetricIsInert(t *testing.T) {
	// Defensive: a stored policy from before a metric was retired must not
	// crash or scale. p99_latency_ms rows still exist on disk.
	res := Arbitrate(2, []Observation{{
		Metric: api.ScalingMetricP99LatencyMS, Target: 250, Measured: 9000, Have: true,
	}})
	if res.Hot {
		t.Fatalf("unclassified metric = %+v, want inert", res)
	}
}

func TestArbitrate_ColdStartOnRateMetric(t *testing.T) {
	// With no instances there is no fleet to multiply, so a rate signal
	// asks for exactly one and the next tick measures the real load.
	res := Arbitrate(0, []Observation{{
		Metric: api.ScalingMetricRPS, Target: 50, Measured: 400, Have: true,
	}})
	if !res.Hot || res.Desired != 1 {
		t.Fatalf("Arbitrate = %+v, want hot with desired 1", res)
	}
}
