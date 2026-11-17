package operatorobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func runtimeDelayedTaskDispatchMetric(d RuntimeDelayedTaskDispatchDimension) (string, string) {
	return "schedd_delayed_task_dispatch_total", d.Outcome
}
func runtimeDelayedTaskDispatchQuery(job string) string {
	return runtimeGaugeQuery(job, []string{"schedd_delayed_task_dispatch_total"})
}

func readRuntimeDelayedTaskDispatchSamples(ctx context.Context, client *promql.Client, result *runtimeBuildJobResult, now time.Time) runtimeGaugeSamples {
	if result == nil || result.samples == nil || len(result.samples.up) == 0 {
		return runtimeGaugeSamples{reason: "source_unavailable"}
	}
	known := map[string]bool{}
	for _, d := range runtimeDelayedTaskDispatchDimensions() {
		m, dims := runtimeDelayedTaskDispatchMetric(d)
		known[m+"/"+dims] = true
	}
	return readRuntimeGaugeSamplesDimensions(ctx, client, *result.samples, result.job.Job, runtimeDelayedTaskDispatchQuery(result.job.Job), known, []string{"outcome"}, now)
}

func readRuntimeDelayedTaskDispatchJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeDelayedTaskDispatchItem {
	current := readRuntimeDelayedTaskDispatchSamples(ctx, client, &result, now)
	prior := runtimeGaugeSamples{reason: "source_unavailable"}
	if !selected.compareAt.IsZero() {
		prior = readRuntimeDelayedTaskDispatchSamples(ctx, client, result.previous, selected.compareAt)
	}
	keys, previousKeys := map[string]string{}, map[string]string{}
	for _, pair := range []struct {
		result *runtimeBuildJobResult
		keys   map[string]string
	}{{&result, keys}, {result.previous, previousKeys}} {
		if pair.result != nil && pair.result.samples != nil {
			for key := range pair.result.samples.up {
				pair.keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
			}
		}
	}
	items := []RuntimeDelayedTaskDispatchItem{}
	for _, target := range result.items {
		bad, at := runtimeTargetSourceStatus(target, keys[target.TargetID], result.samples, selected, now)
		item := RuntimeDelayedTaskDispatchItem{Job: result.job.Job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Process: target.Process, Signals: []RuntimeDelayedTaskDispatchSignal{}}
		for _, d := range runtimeDelayedTaskDispatchDimensions() {
			s := RuntimeDelayedTaskDispatchSignal{RuntimeDelayedTaskDispatchDimension: d, State: bad.State, Reason: bad.Reason}
			if bad.State == "" {
				m, dims := runtimeDelayedTaskDispatchMetric(d)
				point, status := runtimeAppTaskCounterPoint(current, keys[target.TargetID], m, dims, selected, now)
				s.Current, s.State, s.Reason = point, status.State, status.Reason
				if point != nil {
					if selected.compareAt.IsZero() {
						s.State, s.Reason = "baseline", ""
					} else {
						previous, status := runtimeAppTaskCounterPoint(prior, previousKeys[target.TargetID], m, dims, selected, selected.compareAt)
						s.Previous = previous
						if previous == nil {
							s.State, s.Reason = status.State, "previous_"+status.Reason
						} else {
							s.State, s.Reason = runtimeAppTaskOperationComparison(point, previous, item.Process)
							if s.State == "increased" || s.State == "unchanged" {
								delta := point.Value - previous.Value
								s.Delta = &delta
							}
						}
					}
				}
			}
			item.Signals = append(item.Signals, s)
		}
		items = append(items, item)
	}
	return items
}

func aggregateRuntimeDelayedTaskDispatch(base api.ObsRuntimeBuilds, rows [][]RuntimeDelayedTaskDispatchItem) *RuntimeDelayedTaskDispatch {
	jobs := runtimeAppTaskJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimeDelayedTaskDispatch{RuntimeDelayedTaskDispatchSummary: newRuntimeDelayedTaskDispatchSummary(), Items: []RuntimeDelayedTaskDispatchItem{}}
	if base.Process != nil {
		out.ComparisonAt = base.Process.ComparisonAt
	}
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected * len(runtimeDelayedTaskDispatchDimensions())
		out.Incomplete = out.Incomplete || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeDelayedTaskDispatch(&out.RuntimeDelayedTaskDispatchSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = out.Incomplete || runtimeAppTaskOperationUnknown(out.Counts) > 0 || out.Counts.Unexpected > 0
	out.State, out.Reason = runtimeDelayedTaskDispatchState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeDelayedTaskDispatchItem) int {
		return runtimeDelayedTaskDispatchPriority(a) - runtimeDelayedTaskDispatchPriority(b)
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeDelayedTaskDispatchPriority(item RuntimeDelayedTaskDispatchItem) int {
	priority := 4
	for _, s := range item.Signals {
		if s.State == "increased" {
			switch s.Outcome {
			case "failed", "dead_letter":
				return 0
			case "retry":
				priority = min(priority, 1)
			default:
				priority = min(priority, 2)
			}
		} else if s.State != "unchanged" && s.State != "baseline" {
			priority = min(priority, 3)
		}
	}
	return priority
}
