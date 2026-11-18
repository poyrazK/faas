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

const runtimeSnapshotPrimePrefix = "schedd_snapshot_prime_"

func runtimeSnapshotPrimeMetricNames() []string {
	return []string{"pending", "age_unknown", "oldest_attempt_timestamp_seconds", "collection_success", "collection_last_success_timestamp_seconds", "observation_enabled"}
}

func runtimeSnapshotPrimeQuery(job string) string {
	names := runtimeSnapshotPrimeMetricNames()
	for i := range names {
		names[i] = runtimeSnapshotPrimePrefix + names[i]
	}
	return runtimeGaugeQuery(job, names)
}

func readRuntimeSnapshotPrimeJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeSnapshotPrimeItem {
	items := []RuntimeSnapshotPrimeItem{}
	keys, known := map[string]string{}, map[string]bool{}
	for i, name := range runtimeSnapshotPrimeMetricNames() {
		classes := []string{""}
		if i < 3 {
			classes = runtimeSnapshotPrimeClasses()
		}
		for _, class := range classes {
			known[runtimeSnapshotPrimePrefix+name+"/"+class] = true
		}
	}
	samples := runtimeGaugeSamples{reason: "source_unavailable"}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
		if len(keys) > 0 {
			samples = readRuntimeGaugeSamplesDimension(ctx, client, *result.samples, result.job.Job, runtimeSnapshotPrimeQuery(result.job.Job), known, "class", now)
		}
	}
	for _, target := range result.items {
		items = append(items, assessRuntimeSnapshotPrimeItem(result.job.Job, target, keys[target.TargetID], samples, result.samples, selected, now))
	}
	return items
}

func aggregateRuntimeSnapshotPrime(base api.ObsRuntimeBuilds, rows [][]RuntimeSnapshotPrimeItem) *RuntimeSnapshotPrimeHealth {
	jobs := runtimeSnapshotPrimeJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimeSnapshotPrimeHealth{RuntimeSnapshotPrimeSummary: newRuntimeSnapshotPrimeSummary(), Items: []RuntimeSnapshotPrimeItem{}}
	inventoryUnknown := false
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected
		inventoryUnknown = inventoryUnknown || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeSnapshotPrimeItem(&out.RuntimeSnapshotPrimeSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = runtimeSnapshotPrimeIncomplete(out.Counts, inventoryUnknown)
	out.State, out.Reason = runtimeSnapshotPrimeState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeSnapshotPrimeItem) int {
		if p := cmp.Compare(runtimeSnapshotPrimePriority(a), runtimeSnapshotPrimePriority(b)); p != 0 {
			return p
		}
		return cmp.Compare(runtimeSnapshotPrimeOldest(b), runtimeSnapshotPrimeOldest(a))
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeSnapshotPrimeIncomplete(c RuntimeSnapshotPrimeCounts, inventoryUnknown bool) bool {
	return inventoryUnknown || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
}

func runtimeSnapshotPrimeState(c RuntimeSnapshotPrimeCounts, incomplete bool) (string, string) {
	switch {
	case c.NonemptyScopes > 0:
		return "nonempty", "snapshot_attempts_observed"
	case incomplete:
		return "incomplete", "incomplete_snapshot_prime_evidence"
	case c.Complete > 0:
		return "empty", ""
	default:
		return "unknown", "snapshot_prime_evidence_unavailable"
	}
}

func runtimeSnapshotPrimeItemState(item RuntimeSnapshotPrimeItem) string {
	states := []string{item.Collection.State, item.Observation.State}
	for _, class := range item.Classes {
		states = append(states, class.State)
	}
	for _, state := range states {
		if slices.Contains([]string{"missing", "stale", "conflicting", "unavailable"}, state) {
			return state
		}
	}
	return "available"
}

func assessRuntimeSnapshotPrimeItem(job string, target api.ObsRuntimeBuildItem, key string, samples runtimeGaugeSamples, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimeSnapshotPrimeItem {
	bad, at := runtimeTargetSourceStatus(target, key, builds, selected, now)
	out := RuntimeSnapshotPrimeItem{Job: job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Classes: []RuntimeSnapshotPrimeClass{}}
	component := func(names []string) (RuntimeBuilderSignal, map[string]float64) {
		if bad.State != "" {
			return bad, nil
		}
		return samples.coherentComponent(key, runtimeSnapshotPrimePrefix, names, selected, now)
	}
	s, v := component([]string{"collection_success", "collection_last_success_timestamp_seconds"})
	out.Collection = runtimeHandoffCollection(s, v, now)
	s, v = component([]string{"observation_enabled"})
	out.Observation = RuntimeSnapshotPrimeObservation{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State == "available" {
		if v["observation_enabled"] != 0 && v["observation_enabled"] != 1 {
			out.Observation.RuntimeHandoffSignal = RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}
		} else {
			out.Observation.Values = &RuntimeSnapshotPrimeObservationValues{Enabled: v["observation_enabled"] == 1}
		}
	}
	for _, name := range runtimeSnapshotPrimeClasses() {
		class := RuntimeSnapshotPrimeClass{Class: name, RuntimeHandoffSignal: runtimeSnapshotPrimePrerequisite(out.Collection, selected.after, now)}
		if class.State == "" {
			s, v := samples.component(key, runtimeSnapshotPrimePrefix, name, []string{"pending", "age_unknown", "oldest_attempt_timestamp_seconds"}, selected, now)
			if s.State == "available" {
				for _, metric := range []string{"pending", "age_unknown", "oldest_attempt_timestamp_seconds"} {
					if samples.rows[key][runtimeSnapshotPrimePrefix+metric+"/"+name].at != samples.rows[key][runtimeSnapshotPrimePrefix+"collection_success/"].at {
						s = RuntimeBuilderSignal{State: "conflicting", Reason: "conflicting_sample_times"}
						break
					}
				}
			}
			class = runtimeSnapshotPrimeClass(name, s, v, now)
		}
		out.Classes = append(out.Classes, class)
	}
	out.State = runtimeSnapshotPrimeItemState(out)
	return out
}

func accountRuntimeSnapshotPrimeItem(h *RuntimeSnapshotPrimeSummary, item RuntimeSnapshotPrimeItem) {
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
	nonempty, empty, unknown := false, true, false
	for i, class := range item.Classes {
		if class.State == "nonempty" {
			h.Classes[i].NonemptyScopes++
			nonempty = true
		}
		if class.State == "empty" {
			h.Classes[i].EmptyScopes++
		} else {
			empty = false
		}
		if class.Values != nil && class.Values.UnknownAge > 0 {
			h.Classes[i].UnknownAgeScopes++
			unknown = true
		}
	}
	if nonempty {
		c.NonemptyScopes++
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

func runtimeSnapshotPrimePriority(item RuntimeSnapshotPrimeItem) int {
	for _, c := range item.Classes {
		if c.State == "nonempty" {
			return 0
		}
	}
	if item.State != "available" {
		return 1
	}
	return 2
}
func runtimeSnapshotPrimeOldest(item RuntimeSnapshotPrimeItem) float64 {
	age := float64(0)
	for _, c := range item.Classes {
		if c.Values != nil && c.Values.OldestKnownAgeSeconds != nil {
			age = max(age, *c.Values.OldestKnownAgeSeconds)
		}
	}
	return age
}
