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

const runtimePostCapturePrefix = "imaged_post_capture_"

func runtimePostCaptureMetricNames() []string {
	return []string{"pending", "age_unknown", "oldest_boundary_timestamp_seconds", "collection_success", "collection_last_success_timestamp_seconds", "observation_enabled"}
}

func runtimePostCaptureQuery(job string) string {
	names := runtimePostCaptureMetricNames()
	for i := range names {
		names[i] = runtimePostCapturePrefix + names[i]
	}
	return runtimeGaugeQuery(job, names)
}

func readRuntimePostCaptureJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimePostCaptureItem {
	items := []RuntimePostCaptureItem{}
	keys, known := map[string]string{}, map[string]bool{}
	for i, name := range runtimePostCaptureMetricNames() {
		classes := []string{""}
		if i < 3 {
			classes = runtimePostCaptureClasses()
		}
		for _, class := range classes {
			known[runtimePostCapturePrefix+name+"/"+class] = true
		}
	}
	samples := runtimeGaugeSamples{reason: "source_unavailable"}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
		if len(keys) > 0 {
			samples = readRuntimeGaugeSamplesDimension(ctx, client, *result.samples, result.job.Job, runtimePostCaptureQuery(result.job.Job), known, "class", now)
		}
	}
	for _, target := range result.items {
		items = append(items, assessRuntimePostCaptureItem(result.job.Job, target, keys[target.TargetID], samples, result.samples, selected, now))
	}
	return items
}

func aggregateRuntimePostCapture(base api.ObsRuntimeBuilds, rows [][]RuntimePostCaptureItem) *RuntimePostCaptureHealth {
	jobs := runtimePostCaptureJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimePostCaptureHealth{RuntimePostCaptureSummary: newRuntimePostCaptureSummary(), Items: []RuntimePostCaptureItem{}}
	inventoryUnknown := false
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected
		inventoryUnknown = inventoryUnknown || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimePostCaptureItem(&out.RuntimePostCaptureSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = runtimePostCaptureIncomplete(out.Counts, inventoryUnknown)
	out.State, out.Reason = runtimePostCaptureState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimePostCaptureItem) int {
		if p := cmp.Compare(runtimePostCapturePriority(a), runtimePostCapturePriority(b)); p != 0 {
			return p
		}
		return cmp.Compare(runtimePostCaptureOldest(b), runtimePostCaptureOldest(a))
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimePostCaptureIncomplete(c RuntimePostCaptureCounts, inventoryUnknown bool) bool {
	return inventoryUnknown || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
}

func runtimePostCaptureState(c RuntimePostCaptureCounts, incomplete bool) (string, string) {
	switch {
	case c.NonemptyViews > 0:
		return "nonempty", "post_capture_evidence_observed"
	case incomplete:
		return "incomplete", "incomplete_post_capture_evidence"
	case c.Complete > 0:
		return "empty", ""
	default:
		return "unknown", "post_capture_evidence_unavailable"
	}
}

func runtimePostCaptureItemState(item RuntimePostCaptureItem) string {
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

func assessRuntimePostCaptureItem(job string, target api.ObsRuntimeBuildItem, key string, samples runtimeGaugeSamples, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimePostCaptureItem {
	bad, at := runtimeTargetSourceStatus(target, key, builds, selected, now)
	out := RuntimePostCaptureItem{Job: job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Classes: []RuntimePostCaptureClass{}}
	component := func(names []string) (RuntimeBuilderSignal, map[string]float64) {
		if bad.State != "" {
			return bad, nil
		}
		return samples.coherentComponent(key, runtimePostCapturePrefix, names, selected, now)
	}
	s, v := component([]string{"collection_success", "collection_last_success_timestamp_seconds"})
	out.Collection = runtimeHandoffCollection(s, v, now)
	s, v = component([]string{"observation_enabled"})
	out.Observation = RuntimePostCaptureObservation{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State == "available" {
		if v["observation_enabled"] != 0 && v["observation_enabled"] != 1 {
			out.Observation.RuntimeHandoffSignal = RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}
		} else {
			out.Observation.Values = &RuntimePostCaptureObservationValues{Enabled: v["observation_enabled"] == 1}
		}
	}
	for _, name := range runtimePostCaptureClasses() {
		class := RuntimePostCaptureClass{Class: name, RuntimeHandoffSignal: runtimePostCapturePrerequisite(out.Collection, selected.after, now)}
		if class.State == "" {
			s, v := samples.component(key, runtimePostCapturePrefix, name, []string{"pending", "age_unknown", "oldest_boundary_timestamp_seconds"}, selected, now)
			if s.State == "available" {
				for _, metric := range []string{"pending", "age_unknown", "oldest_boundary_timestamp_seconds"} {
					if samples.rows[key][runtimePostCapturePrefix+metric+"/"+name].at != samples.rows[key][runtimePostCapturePrefix+"collection_success/"].at {
						s = RuntimeBuilderSignal{State: "conflicting", Reason: "conflicting_sample_times"}
						break
					}
				}
			}
			class = runtimePostCaptureClass(name, s, v, now)
		}
		out.Classes = append(out.Classes, class)
	}
	out.State = runtimePostCaptureItemState(out)
	return out
}

func accountRuntimePostCaptureItem(h *RuntimePostCaptureSummary, item RuntimePostCaptureItem) {
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
	if v := item.Observation.Values; v != nil {
		if v.Enabled {
			c.EnabledViews++
		} else {
			c.DisabledViews++
		}
	}
}

func runtimePostCapturePriority(item RuntimePostCaptureItem) int {
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
func runtimePostCaptureOldest(item RuntimePostCaptureItem) float64 {
	age := float64(0)
	for _, c := range item.Classes {
		if c.Values != nil && c.Values.OldestKnownAgeSeconds != nil {
			age = max(age, *c.Values.OldestKnownAgeSeconds)
		}
	}
	return age
}
