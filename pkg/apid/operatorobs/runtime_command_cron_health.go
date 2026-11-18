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

const runtimeCommandCronPrefix = "schedd_command_cron_"

func runtimeCommandCronMetricNames() []string {
	return []string{"schedules", "age_unknown", "oldest_due_timestamp_seconds", "collection_success", "collection_last_success_timestamp_seconds", "observation_enabled", "sweep_last_start_timestamp_seconds", "sweep_last_success_timestamp_seconds", "sweep_running"}
}

func runtimeCommandCronQuery(job string) string {
	names := runtimeCommandCronMetricNames()
	for i := range names {
		names[i] = runtimeCommandCronPrefix + names[i]
	}
	return runtimeGaugeQuery(job, names)
}

func readRuntimeCommandCronJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeCommandCronItem {
	items := []RuntimeCommandCronItem{}
	keys, known := map[string]string{}, map[string]bool{}
	for i, name := range runtimeCommandCronMetricNames() {
		classes := []string{""}
		if i < 3 {
			classes = runtimeCommandCronClasses()
		}
		for _, class := range classes {
			known[runtimeCommandCronPrefix+name+"/"+class] = true
		}
	}
	samples := runtimeGaugeSamples{reason: "source_unavailable"}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
		if len(keys) > 0 {
			samples = readRuntimeGaugeSamplesDimension(ctx, client, *result.samples, result.job.Job, runtimeCommandCronQuery(result.job.Job), known, "class", now)
		}
	}
	for _, target := range result.items {
		items = append(items, assessRuntimeCommandCronItem(result.job.Job, target, keys[target.TargetID], samples, result.samples, selected, now))
	}
	return items
}

func aggregateRuntimeCommandCron(base api.ObsRuntimeBuilds, rows [][]RuntimeCommandCronItem) *RuntimeCommandCronHealth {
	jobs := runtimeCommandCronJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimeCommandCronHealth{RuntimeCommandCronSummary: newRuntimeCommandCronSummary(), Items: []RuntimeCommandCronItem{}}
	inventoryUnknown := false
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected
		inventoryUnknown = inventoryUnknown || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeCommandCronItem(&out.RuntimeCommandCronSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = runtimeCommandCronIncomplete(out.Counts, inventoryUnknown)
	out.State, out.Reason = runtimeCommandCronState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeCommandCronItem) int {
		if p := cmp.Compare(runtimeCommandCronPriority(a), runtimeCommandCronPriority(b)); p != 0 {
			return p
		}
		return cmp.Compare(runtimeCommandCronOldest(b), runtimeCommandCronOldest(a))
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeCommandCronIncomplete(c RuntimeCommandCronCounts, inventoryUnknown bool) bool {
	return inventoryUnknown || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
}

func runtimeCommandCronState(c RuntimeCommandCronCounts, incomplete bool) (string, string) {
	switch {
	case c.NonemptyScopes > 0:
		return "nonempty", "command_cron_evidence_observed"
	case incomplete:
		return "incomplete", "incomplete_command_cron_evidence"
	case c.Complete > 0:
		return "empty", ""
	default:
		return "unknown", "command_cron_evidence_unavailable"
	}
}

func runtimeCommandCronItemState(item RuntimeCommandCronItem) string {
	states := []string{item.Collection.State, item.Observation.State, item.Sweep.State}
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

func assessRuntimeCommandCronItem(job string, target api.ObsRuntimeBuildItem, key string, samples runtimeGaugeSamples, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimeCommandCronItem {
	bad, at := runtimeTargetSourceStatus(target, key, builds, selected, now)
	out := RuntimeCommandCronItem{Job: job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Classes: []RuntimeCommandCronClass{}}
	component := func(names []string) (RuntimeBuilderSignal, map[string]float64) {
		if bad.State != "" {
			return bad, nil
		}
		return samples.coherentComponent(key, runtimeCommandCronPrefix, names, selected, now)
	}
	s, v := component([]string{"collection_success", "collection_last_success_timestamp_seconds"})
	out.Collection = runtimeHandoffCollection(s, v, now)
	s, v = component([]string{"observation_enabled"})
	out.Observation = RuntimeCommandCronObservation{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State == "available" {
		if v["observation_enabled"] != 0 && v["observation_enabled"] != 1 {
			out.Observation.RuntimeHandoffSignal = RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}
		} else {
			out.Observation.Values = &RuntimeCommandCronObservationValues{Enabled: v["observation_enabled"] == 1}
		}
	}
	s, v = component([]string{"sweep_running", "sweep_last_start_timestamp_seconds", "sweep_last_success_timestamp_seconds"})
	out.Sweep = runtimeCommandCronSweep(s, v, now)
	for _, name := range runtimeCommandCronClasses() {
		class := RuntimeCommandCronClass{Class: name, RuntimeHandoffSignal: runtimeCommandCronPrerequisite(out.Collection, selected.after, now)}
		if class.State == "" {
			s, v := samples.component(key, runtimeCommandCronPrefix, name, []string{"schedules", "age_unknown", "oldest_due_timestamp_seconds"}, selected, now)
			if s.State == "available" {
				for _, metric := range []string{"schedules", "age_unknown", "oldest_due_timestamp_seconds"} {
					if samples.rows[key][runtimeCommandCronPrefix+metric+"/"+name].at != samples.rows[key][runtimeCommandCronPrefix+"collection_success/"].at {
						s = RuntimeBuilderSignal{State: "conflicting", Reason: "conflicting_sample_times"}
						break
					}
				}
			}
			class = runtimeCommandCronClass(name, s, v, now)
		}
		out.Classes = append(out.Classes, class)
	}
	out.State = runtimeCommandCronItemState(out)
	return out
}

func accountRuntimeCommandCronItem(h *RuntimeCommandCronSummary, item RuntimeCommandCronItem) {
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
	nonempty, empty, unknown, due, inactive := false, true, false, false, false
	for i, class := range item.Classes {
		if class.State == "nonempty" {
			h.Classes[i].NonemptyScopes++
			due = due || runtimeCommandCronDueClass(class.Class)
			inactive = inactive || runtimeCommandCronInactiveClass(class.Class)
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
	if due {
		c.DueScopes++
	}
	if inactive {
		c.InactiveScopes++
	}
	if v := item.Sweep.Values; v != nil {
		if v.Running {
			c.SweepRunningScopes++
		} else {
			c.SweepIdleScopes++
		}
		if v.LastStartedAt == nil {
			c.SweepNeverStartedScopes++
		}
		if v.LastSuccessAt == nil {
			c.SweepNeverSucceededScopes++
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

func runtimeCommandCronPriority(item RuntimeCommandCronItem) int {
	nonempty := false
	for _, c := range item.Classes {
		if c.State == "nonempty" {
			if runtimeCommandCronDueClass(c.Class) {
				return 0
			}
			nonempty = true
		}
	}
	if nonempty {
		return 1
	}
	if item.State != "available" {
		return 2
	}
	return 3
}
func runtimeCommandCronOldest(item RuntimeCommandCronItem) float64 {
	age := float64(0)
	for _, c := range item.Classes {
		if c.Values != nil && c.Values.OldestKnownDueAgeSeconds != nil {
			age = max(age, *c.Values.OldestKnownDueAgeSeconds)
		}
	}
	return age
}
