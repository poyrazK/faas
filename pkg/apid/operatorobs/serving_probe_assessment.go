package operatorobs

import (
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func servingProbeAssess(intent servingProbeIntent, samples servingProbeSamples, queryErr error, now time.Time) []api.ObsServingProbe {
	out := make([]api.ObsServingProbe, 0, len(intent.pairs))
	for key, policy := range intent.pairs {
		probe := api.ObsServingProbe{Node: key.node, URLID: servingProbeURLID(key.url), State: "unknown", Reason: "query_failed", ExpectedFirstByteSeconds: policy.budget, MaxResultAgeSeconds: policy.age}
		if queryErr == nil {
			probe = samples.assessProbe(probe, key.url, policy, now)
		}
		out = append(out, probe)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].URLID < out[j].URLID
	})
	return out
}

func (samples servingProbeSamples) assessProbe(probe api.ObsServingProbe, targetURL string, policy servingProbePolicy, now time.Time) api.ObsServingProbe {
	key := servingProbeKey{metric: probeState, node: probe.Node, url: targetURL}
	value, present := samples.values[key]
	probe.Reason = "coverage_missing"
	if !present {
		return probe
	}
	at, valid := inputSampleTime(samples.times[key], now)
	probe.Reason = "coverage_invalid"
	if !valid || value < 0 || value > 6 || value != math.Trunc(value) {
		return probe
	}
	probe.EvaluatedAt = &at
	if at.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) {
		probe.Reason = "coverage_stale"
		return probe
	}
	for _, desired := range []struct {
		metric string
		value  float64
	}{{probeEvalBudget, policy.budget}, {probeEvalAge, policy.age}} {
		meta := servingProbeKey{metric: desired.metric, node: probe.Node}
		timestamp, exists := samples.times[meta]
		if samples.values[meta] != desired.value || !exists || timestamp != samples.times[key] {
			probe.Reason = "policy_not_evaluated"
			return probe
		}
	}
	clock := servingProbeKey{metric: probeRun, node: probe.Node}
	if samples.times[clock] == samples.times[key] {
		if run, valid := inputSampleTime(samples.values[clock], now); valid {
			probe.LastRunAt = &run
		}
	}
	probe.State = ServingProbeStates()[int(value)]
	probe.Reason = []string{"", "result_missing", "result_stale", "invalid_telemetry", "content_check_disabled", "latency_budget_mismatch", "probe_failing"}[int(value)]
	reporting := value != 1
	probe.Reporting = &reporting
	// A last healthy evaluation cannot hold green while its run clock ages
	// beyond the currently configured budget between rule evaluations.
	if value == 0 || value >= 4 {
		if probe.LastRunAt == nil {
			probe.State, probe.Reason, probe.Reporting = "unknown", "run_clock_unavailable", nil
		} else if float64(now.UnixMilli()-probe.LastRunAt.UnixMilli())/1000 > policy.age {
			probe.State, probe.Reason = "stale", "result_stale"
		}
	}
	return probe
}

// ServingProbeCounts summarizes every expected pair; unknown evaluations do
// not become reporting results or healthy coverage.
func ServingProbeCounts(probes []api.ObsServingProbe) api.ObsServingProbeCounts {
	out := api.ObsServingProbeCounts{Expected: len(probes)}
	for _, probe := range probes {
		if probe.Reporting != nil && *probe.Reporting {
			out.Reporting++
		}
		switch probe.State {
		case "healthy":
			out.Healthy++
		case "missing":
			out.Missing++
		case "stale":
			out.Stale++
		case "invalid_telemetry":
			out.Invalid++
		case "content_disabled":
			out.ContentDisabled++
		case "budget_mismatch":
			out.BudgetMismatch++
		case "failing":
			out.Failing++
		default:
			out.Unknown++
		}
	}
	return out
}

func ServingProbeOverall(counts api.ObsServingProbeCounts) (string, string) {
	if counts.Expected == 0 {
		return "not_applicable", "zero_expected_probes"
	}
	if counts.Missing+counts.Stale+counts.Invalid+counts.ContentDisabled+counts.BudgetMismatch+counts.Failing > 0 {
		return "degraded", "probe_fault"
	}
	if counts.Unknown > 0 {
		return "partial", "incomplete_evidence"
	}
	return "healthy", ""
}
