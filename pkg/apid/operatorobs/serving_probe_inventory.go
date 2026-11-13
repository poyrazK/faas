package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (samples servingProbeSamples) keys(metric string) []servingProbeKey {
	var keys []servingProbeKey
	for key := range samples.values {
		if key.metric == metric {
			keys = append(keys, key)
		}
	}
	return keys
}

func (samples servingProbeSamples) intent(now time.Time) servingProbeIntent {
	out := servingProbeIntent{status: api.ObsServingProbeInventory{State: "unavailable", Reason: "inventory_invalid"}, pairs: map[servingProbeKey]servingProbePolicy{}}
	configured, count := samples.keys(probeConfigured), samples.keys(probePairs)
	if len(configured) == 0 && len(count) == 0 {
		out.status.Reason = "inventory_missing"
		return out
	}
	if len(configured) != 1 || len(count) != 1 || len(samples.values) != len(samples.times) || configured[0].source != count[0].source {
		return out
	}
	source := configured[0].source
	var oldest *time.Time
	for key, value := range samples.values {
		rawTime, exists := samples.times[key]
		at, valid := inputSampleTime(rawTime, now)
		if !exists || !valid || key.metric != "up" && key.source != source || value < 0 {
			return out
		}
		if oldest == nil || at.Before(*oldest) {
			oldest = &at
		}
	}
	if oldest != nil && oldest.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) {
		out.status.State, out.status.Reason, out.status.ObservedAt = "stale", "inventory_stale", oldest
		return out
	}
	flag, total := samples.values[configured[0]], samples.values[count[0]]
	up := servingProbeKey{metric: "up", source: source}
	if (flag != 0 && flag != 1) || total != math.Trunc(total) || total > api.ObsServingProbePairsMax || samples.values[up] != 1 {
		return out
	}
	policies := map[string]servingProbePolicy{}
	for _, key := range samples.keys(probeExpected) {
		if samples.values[key] != 1 {
			return out
		}
		budgetKey := servingProbeKey{metric: probeBudget, source: source, node: key.node}
		ageKey := servingProbeKey{metric: probeAge, source: source, node: key.node}
		policy := servingProbePolicy{budget: samples.values[budgetKey], age: samples.values[ageKey]}
		if policy.budget <= 0 || policy.age <= 0 {
			return out
		}
		policies[key.node] = policy
		out.pairs[key] = policy
	}
	if len(out.pairs) != int(total) || flag == 0 && total != 0 || len(samples.keys(probeBudget)) != len(policies) || len(samples.keys(probeAge)) != len(policies) {
		return out
	}
	// Never emit a truncated expectation if a transport or inventory bound fails.
	if len(out.pairs) > api.ObsServingProbePairsMax {
		return out
	}
	on, expected := flag == 1, int(total)
	out.status = api.ObsServingProbeInventory{State: "healthy", Configured: &on, ExpectedPairs: &expected, ObservedAt: oldest}
	return out
}
