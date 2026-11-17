package operatorobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func runtimeAppTaskResultMetric(d RuntimeAppTaskResultDimension) (string, string) {
	if d.Source == "completion" {
		return "schedd_app_task_results_total", d.Kind + "/" + d.Outcome + "/"
	}
	return "schedd_app_task_recovery_total", "//" + d.Outcome
}

func runtimeAppTaskResultQuery(job string) string {
	return runtimeGaugeQuery(job, []string{"schedd_app_task_results_total", "schedd_app_task_recovery_total"})
}

func readRuntimeAppTaskResultSamples(ctx context.Context, client *promql.Client, result *runtimeBuildJobResult, now time.Time) runtimeGaugeSamples {
	if result == nil || result.samples == nil || len(result.samples.up) == 0 {
		return runtimeGaugeSamples{reason: "source_unavailable"}
	}
	known := map[string]bool{}
	for _, d := range runtimeAppTaskResultDimensions() {
		m, dims := runtimeAppTaskResultMetric(d)
		known[m+"/"+dims] = true
	}
	return readRuntimeGaugeSamplesDimensions(ctx, client, *result.samples, result.job.Job, runtimeAppTaskResultQuery(result.job.Job), known, []string{"kind", "status", "outcome"}, now)
}

func readRuntimeAppTaskResultsJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeAppTaskResultItem {
	current := readRuntimeAppTaskResultSamples(ctx, client, &result, now)
	prior := runtimeGaugeSamples{reason: "source_unavailable"}
	if !selected.compareAt.IsZero() {
		prior = readRuntimeAppTaskResultSamples(ctx, client, result.previous, selected.compareAt)
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
	items := []RuntimeAppTaskResultItem{}
	for _, target := range result.items {
		bad, at := runtimeTargetSourceStatus(target, keys[target.TargetID], result.samples, selected, now)
		item := RuntimeAppTaskResultItem{Job: result.job.Job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Process: target.Process, Signals: []RuntimeAppTaskResultSignal{}}
		for _, d := range runtimeAppTaskResultDimensions() {
			s := RuntimeAppTaskResultSignal{RuntimeAppTaskResultDimension: d, State: bad.State, Reason: bad.Reason}
			if bad.State == "" {
				m, dims := runtimeAppTaskResultMetric(d)
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

func aggregateRuntimeAppTaskResults(base api.ObsRuntimeBuilds, rows [][]RuntimeAppTaskResultItem) *RuntimeAppTaskResults {
	jobs := runtimeAppTaskJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimeAppTaskResults{RuntimeAppTaskResultsSummary: newRuntimeAppTaskResultsSummary(), Items: []RuntimeAppTaskResultItem{}}
	if base.Process != nil {
		out.ComparisonAt = base.Process.ComparisonAt
	}
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected * len(runtimeAppTaskResultDimensions())
		out.Incomplete = out.Incomplete || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeAppTaskResults(&out.RuntimeAppTaskResultsSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = out.Incomplete || runtimeAppTaskOperationUnknown(out.Counts) > 0 || out.Counts.Unexpected > 0
	out.State, out.Reason = runtimeAppTaskResultsState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeAppTaskResultItem) int {
		return runtimeAppTaskResultPriority(a) - runtimeAppTaskResultPriority(b)
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeAppTaskResultPriority(item RuntimeAppTaskResultItem) int {
	priority := 3
	for _, s := range item.Signals {
		if s.State == "increased" {
			if strings.Contains(s.Outcome, "failed") || s.Outcome == "timed_out" {
				return 0
			}
			priority = min(priority, 1)
		} else if s.State != "unchanged" && s.State != "baseline" {
			priority = min(priority, 2)
		}
	}
	return priority
}
