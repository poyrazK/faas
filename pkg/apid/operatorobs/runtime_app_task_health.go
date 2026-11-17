package operatorobs

import (
	"cmp"
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const runtimeAppTaskPrefix = "schedd_app_task_"

func runtimeAppTaskMetricNames() []string {
	return []string{"pending", "age_unknown", "oldest_boundary_timestamp_seconds", "collection_success", "collection_last_success_timestamp_seconds", "observation_enabled", "dispatch_enabled", "runner_running", "slots_configured", "slots_in_use", "claim_last_success_timestamp_seconds", "sweep_last_success_timestamp_seconds"}
}

func runtimeAppTaskQuery(job string) string {
	names := runtimeAppTaskMetricNames()
	for i := range names {
		names[i] = runtimeAppTaskPrefix + names[i]
	}
	return runtimeGaugeQuery(job, names)
}

func readRuntimeAppTaskJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeAppTaskItem {
	items := []RuntimeAppTaskItem{}
	keys, known := map[string]string{}, map[string]bool{}
	for i, name := range runtimeAppTaskMetricNames() {
		classes := []string{"/"}
		if i < 3 {
			classes = runtimeAppTaskClasses()
		}
		for _, class := range classes {
			known[runtimeAppTaskPrefix+name+"/"+class] = true
		}
	}
	samples := runtimeGaugeSamples{reason: "source_unavailable"}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
		if len(keys) > 0 {
			samples = readRuntimeGaugeSamplesDimensions(ctx, client, *result.samples, result.job.Job, runtimeAppTaskQuery(result.job.Job), known, []string{"kind", "class"}, now)
		}
	}
	for _, row := range samples.rows {
		for _, name := range runtimeAppTaskMetricNames()[3:] {
			key := runtimeAppTaskPrefix + name
			if value, ok := row[key+"//"]; ok {
				row[key+"/"] = value
				delete(row, key+"//")
			}
		}
	}
	for _, target := range result.items {
		items = append(items, assessRuntimeAppTaskItem(result.job.Job, target, keys[target.TargetID], samples, result.samples, selected, now))
	}
	return items
}

func aggregateRuntimeAppTask(base api.ObsRuntimeBuilds, rows [][]RuntimeAppTaskItem) *RuntimeAppTaskHealth {
	jobs := runtimeAppTaskJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimeAppTaskHealth{RuntimeAppTaskSummary: newRuntimeAppTaskSummary(), Items: []RuntimeAppTaskItem{}}
	inventoryUnknown := false
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected
		inventoryUnknown = inventoryUnknown || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeAppTaskItem(&out.RuntimeAppTaskSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = runtimeAppTaskIncomplete(out.Counts, inventoryUnknown)
	out.State, out.Reason = runtimeAppTaskState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeAppTaskItem) int {
		if p := cmp.Compare(runtimeAppTaskPriority(a), runtimeAppTaskPriority(b)); p != 0 {
			return p
		}
		return cmp.Compare(runtimeAppTaskOldest(b), runtimeAppTaskOldest(a))
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeAppTaskIncomplete(c RuntimeAppTaskCounts, inventoryUnknown bool) bool {
	return inventoryUnknown || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
}

func runtimeAppTaskState(c RuntimeAppTaskCounts, incomplete bool) (string, string) {
	switch {
	case c.NonemptyViews > 0:
		return "nonempty", "app_task_evidence_observed"
	case incomplete:
		return "incomplete", "incomplete_app_task_evidence"
	case c.Complete > 0:
		return "empty", ""
	default:
		return "unknown", "app_task_evidence_unavailable"
	}
}

func runtimeAppTaskItemState(item RuntimeAppTaskItem) string {
	states := []string{item.Collection.State, item.Observation.State, item.Dispatch.State}
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

func assessRuntimeAppTaskItem(job string, target api.ObsRuntimeBuildItem, key string, samples runtimeGaugeSamples, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimeAppTaskItem {
	bad, at := runtimeTargetSourceStatus(target, key, builds, selected, now)
	out := RuntimeAppTaskItem{Job: job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Classes: []RuntimeAppTaskClass{}}
	component := func(names []string) (RuntimeBuilderSignal, map[string]float64) {
		if bad.State != "" {
			return bad, nil
		}
		return samples.coherentComponent(key, runtimeAppTaskPrefix, names, selected, now)
	}
	s, v := component([]string{"collection_success", "collection_last_success_timestamp_seconds"})
	out.Collection = runtimeHandoffCollection(s, v, now)
	s, v = component([]string{"observation_enabled"})
	out.Observation = RuntimeAppTaskObservation{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State == "available" {
		if v["observation_enabled"] != 0 && v["observation_enabled"] != 1 {
			out.Observation.RuntimeHandoffSignal = RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}
		} else {
			out.Observation.Values = &RuntimeAppTaskObservationValues{Enabled: v["observation_enabled"] == 1}
		}
	}
	// Dispatch is live process evidence, independent of durable collection success.
	s, v = component([]string{"dispatch_enabled", "runner_running", "slots_configured", "slots_in_use", "claim_last_success_timestamp_seconds", "sweep_last_success_timestamp_seconds"})
	out.Dispatch = runtimeAppTaskDispatch(s, v, now)
	for _, name := range runtimeAppTaskClasses() {
		kind, className, _ := strings.Cut(name, "/")
		class := RuntimeAppTaskClass{Kind: kind, Class: className, RuntimeHandoffSignal: runtimeAppTaskPrerequisite(out.Collection, selected.after, now)}
		if class.State == "" {
			s, v := samples.component(key, runtimeAppTaskPrefix, name, []string{"pending", "age_unknown", "oldest_boundary_timestamp_seconds"}, selected, now)
			if s.State == "available" {
				for _, metric := range []string{"pending", "age_unknown", "oldest_boundary_timestamp_seconds"} {
					if samples.rows[key][runtimeAppTaskPrefix+metric+"/"+name].at != samples.rows[key][runtimeAppTaskPrefix+"collection_success/"].at {
						s = RuntimeBuilderSignal{State: "conflicting", Reason: "conflicting_sample_times"}
						break
					}
				}
			}
			class = runtimeAppTaskClass(name, s, v, now)
		}
		class.Kind, class.Class = kind, className
		out.Classes = append(out.Classes, class)
	}
	out.State = runtimeAppTaskItemState(out)
	return out
}

func accountRuntimeAppTaskItem(h *RuntimeAppTaskSummary, item RuntimeAppTaskItem) {
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
			h.Classes[i].NonemptyViews++
			nonempty = true
		}
		if class.State == "empty" {
			h.Classes[i].EmptyViews++
		} else {
			empty = false
		}
		if class.Values != nil && class.Values.UnknownAge > 0 {
			h.Classes[i].UnknownAgeViews++
			unknown = true
		}
	}
	if nonempty {
		c.NonemptyViews++
	}
	if empty {
		c.EmptyViews++
	}
	if unknown {
		c.UnknownAgeViews++
	}
	if item.Collection.Values != nil && !item.Collection.Values.Success {
		c.UnsuccessfulCollections++
	}
	if v := item.Dispatch.Values; v != nil {
		if v.Enabled {
			c.DispatchEnabledViews++
			if v.Running {
				c.RunnerRunningViews++
			} else {
				c.RunnerStoppedViews++
			}
		} else {
			c.DispatchDisabledViews++
		}
		if v.SlotsInUse > 0 {
			c.OccupiedViews++
		}
		if v.SlotsInUse == v.SlotsConfigured {
			c.SaturatedViews++
		}
	}
	if v := item.Observation.Values; v != nil {
		if v.Enabled {
			c.EnabledViews++
		} else {
			c.DisabledViews++
		}
	}
}

func runtimeAppTaskPriority(item RuntimeAppTaskItem) int {
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
func runtimeAppTaskOldest(item RuntimeAppTaskItem) float64 {
	age := float64(0)
	for _, c := range item.Classes {
		if c.Values != nil && c.Values.OldestKnownAgeSeconds != nil {
			age = max(age, *c.Values.OldestKnownAgeSeconds)
		}
	}
	return age
}
