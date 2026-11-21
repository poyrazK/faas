package operatorobs

import (
	"cmp"
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const runtimePreparationPrefix = "imaged_image_preparation_"

func runtimePreparationMetricNames() []string {
	return []string{"pending", "phase_age_unknown", "oldest_phase_started_timestamp_seconds", "collection_success", "collection_last_success_timestamp_seconds", "observation_enabled"}
}

func runtimePreparationQuery() string {
	names := runtimePreparationMetricNames()
	for i := range names {
		names[i] = runtimePreparationPrefix + names[i]
	}
	return runtimeGaugeQuery("imaged", names)
}

func readRuntimePreparationHealth(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) *RuntimePreparationHealth {
	out := &RuntimePreparationHealth{RuntimePreparationSummary: newRuntimePreparationSummary(), Items: []RuntimePreparationItem{}}
	out.Counts.Expected = result.job.Expected
	keys, known := map[string]string{}, map[string]bool{}
	for i, name := range runtimePreparationMetricNames() {
		phases := []string{""}
		if i < 3 {
			phases = runtimePreparationPhases()
		}
		for _, phase := range phases {
			known[runtimePreparationPrefix+name+"/"+phase] = true
		}
	}
	samples := runtimeGaugeSamples{reason: "source_unavailable"}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
		if len(keys) > 0 {
			samples = readRuntimeGaugeSamplesDimension(ctx, client, *result.samples, "imaged", runtimePreparationQuery(), known, "phase", now)
		}
	}
	for _, target := range result.items {
		item := assessRuntimePreparationItem(target, keys[target.TargetID], samples, result.samples, selected, now)
		accountRuntimePreparationItem(&out.RuntimePreparationSummary, item)
		out.Items = append(out.Items, item)
	}
	out.Incomplete = runtimePreparationIncomplete(out.Counts, result.job.State != "available")
	out.State, out.Reason = runtimePreparationState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimePreparationItem) int {
		if p := cmp.Compare(runtimePreparationPriority(a), runtimePreparationPriority(b)); p != 0 {
			return p
		}
		return cmp.Compare(runtimePreparationOldest(b), runtimePreparationOldest(a))
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimePreparationIncomplete(c RuntimePreparationCounts, inventoryUnknown bool) bool {
	return inventoryUnknown || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
}

func runtimePreparationState(c RuntimePreparationCounts, incomplete bool) (string, string) {
	switch {
	case c.WaitingScopes > 0:
		return "waiting", "pending_preparations_observed"
	case incomplete:
		return "incomplete", "incomplete_preparation_evidence"
	case c.Complete > 0:
		return "empty", ""
	default:
		return "unknown", "preparation_evidence_unavailable"
	}
}

func runtimePreparationItemState(item RuntimePreparationItem) string {
	states := []string{item.Collection.State, item.Observation.State}
	for _, phase := range item.Phases {
		states = append(states, phase.State)
	}
	for _, state := range states {
		if slices.Contains([]string{"missing", "stale", "conflicting", "unavailable"}, state) {
			return state
		}
	}
	return "available"
}

func assessRuntimePreparationItem(target api.ObsRuntimeBuildItem, key string, samples runtimeGaugeSamples, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimePreparationItem {
	bad, at := runtimeTargetSourceStatus(target, key, builds, selected, now)
	out := RuntimePreparationItem{Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Phases: []RuntimePreparationPhase{}}
	component := func(names []string) (RuntimeBuilderSignal, map[string]float64) {
		if bad.State != "" {
			return bad, nil
		}
		return samples.coherentComponent(key, runtimePreparationPrefix, names, selected, now)
	}
	s, v := component([]string{"collection_success", "collection_last_success_timestamp_seconds"})
	out.Collection = runtimeHandoffCollection(s, v, now)
	s, v = component([]string{"observation_enabled"})
	out.Observation = RuntimePreparationObservation{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State == "available" {
		if v["observation_enabled"] != 0 && v["observation_enabled"] != 1 {
			out.Observation.RuntimeHandoffSignal = RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}
		} else {
			out.Observation.Values = &RuntimePreparationObservationValues{Enabled: v["observation_enabled"] == 1}
		}
	}
	for _, name := range runtimePreparationPhases() {
		phase := RuntimePreparationPhase{Phase: name, RuntimeHandoffSignal: runtimePreparationPrerequisite(out.Collection, selected.after, now)}
		if phase.State == "" {
			s, v := samples.component(key, runtimePreparationPrefix, name, []string{"pending", "phase_age_unknown", "oldest_phase_started_timestamp_seconds"}, selected, now)
			if s.State == "available" {
				for _, metric := range []string{"pending", "phase_age_unknown", "oldest_phase_started_timestamp_seconds"} {
					if samples.rows[key][runtimePreparationPrefix+metric+"/"+name].at != samples.rows[key][runtimePreparationPrefix+"collection_success/"].at {
						s = RuntimeBuilderSignal{State: "conflicting", Reason: "conflicting_sample_times"}
						break
					}
				}
			}
			phase = runtimePreparationPhase(name, s, v, now)
		}
		out.Phases = append(out.Phases, phase)
	}
	out.State = runtimePreparationItemState(out)
	return out
}

func accountRuntimePreparationItem(h *RuntimePreparationSummary, item RuntimePreparationItem) {
	c := &h.Counts
	if item.TargetID != "" {
		c.Observed++
	}
	if item.Unexpected {
		c.Unexpected++
	}
	switch item.State {
	case "available":
		c.Complete++
	case "missing":
		c.Missing++
	case "stale":
		c.Stale++
	case "conflicting":
		c.Conflicting++
	default:
		c.Unavailable++
	}
	waiting, empty, unknown := false, true, false
	for i, phase := range item.Phases {
		if phase.State == "waiting" {
			h.Phases[i].WaitingScopes++
			waiting = true
		}
		if phase.State == "empty" {
			h.Phases[i].EmptyScopes++
		} else {
			empty = false
		}
		if phase.Values != nil && phase.Values.UnknownAge > 0 {
			h.Phases[i].UnknownAgeScopes++
			unknown = true
		}
	}
	if waiting {
		c.WaitingScopes++
	}
	if empty {
		c.EmptyScopes++
	}
	if unknown {
		c.UnknownAgeScopes++
	}
	if item.Collection.Values != nil && !item.Collection.Values.Success {
		c.UnsuccessfulCollections++
	}
	if v := item.Observation.Values; v != nil {
		if v.Enabled {
			c.EnabledScopes++
		} else {
			c.DisabledScopes++
		}
	}
}

func runtimePreparationPriority(item RuntimePreparationItem) int {
	for _, phase := range item.Phases {
		if phase.State == "waiting" {
			return 0
		}
	}
	if item.State != "available" {
		return 1
	}
	return 2
}

func runtimePreparationOldest(item RuntimePreparationItem) float64 {
	age := float64(0)
	for _, p := range item.Phases {
		if p.Values != nil && p.Values.OldestKnownAgeSeconds != nil {
			age = max(age, *p.Values.OldestKnownAgeSeconds)
		}
	}
	return age
}
