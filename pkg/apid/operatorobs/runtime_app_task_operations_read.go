package operatorobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"slices"
	"strings"
	"time"
)

const runtimeAppTaskOperationMetric = "schedd_app_task_operations_total"

func runtimeAppTaskOperationQuery(job string) string {
	return fmt.Sprintf(`label_replace({job=%q,__name__=%q,operation=~%q}, "obs_metric", "$1", "__name__", "(.+)")`, job, runtimeAppTaskOperationMetric, strings.Join(runtimeAppTaskOperationNames(), "|"))
}
func readRuntimeAppTaskOperationSamples(ctx context.Context, client *promql.Client, result *runtimeBuildJobResult, now time.Time) runtimeGaugeSamples {
	if result == nil || result.samples == nil || len(result.samples.up) == 0 {
		return runtimeGaugeSamples{reason: "source_unavailable"}
	}
	known := map[string]bool{}
	for _, op := range runtimeAppTaskOperationNames() {
		for _, outcome := range runtimeAppTaskOperationOutcomes() {
			known[runtimeAppTaskOperationMetric+"/"+op+"/"+outcome] = true
		}
	}
	return readRuntimeGaugeSamplesDimensions(ctx, client, *result.samples, result.job.Job, runtimeAppTaskOperationQuery(result.job.Job), known, []string{"operation", "outcome"}, now)
}
func runtimeAppTaskOperationPoint(samples runtimeGaugeSamples, key, op, outcome string, selected RuntimeBuildSelection, now time.Time) (*RuntimeAppTaskOperationPoint, RuntimeBuilderSignal) {
	return runtimeAppTaskCounterPoint(samples, key, runtimeAppTaskOperationMetric, op+"/"+outcome, selected, now)
}
func runtimeAppTaskCounterPoint(samples runtimeGaugeSamples, key, metric, dimensions string, selected RuntimeBuildSelection, now time.Time) (*RuntimeAppTaskOperationPoint, RuntimeBuilderSignal) {
	s, v := samples.component(key, metric, dimensions, []string{""}, selected, now)
	if s.State != "available" {
		return nil, s
	}
	n := v[""]
	if !runtimeBuildRecoveryNumberValid(n) {
		return nil, RuntimeBuilderSignal{State: "unavailable", Reason: "invalid_value"}
	}
	return &RuntimeAppTaskOperationPoint{Value: int64(n), ObservedAt: *s.ObservedAt}, s
}
func readRuntimeAppTaskOperationsJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeAppTaskOperationItem {
	current := readRuntimeAppTaskOperationSamples(ctx, client, &result, now)
	prior := runtimeGaugeSamples{reason: "source_unavailable"}
	if !selected.compareAt.IsZero() {
		prior = readRuntimeAppTaskOperationSamples(ctx, client, result.previous, selected.compareAt)
	}
	keys, previousKeys := map[string]string{}, map[string]string{}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
	}
	if result.previous != nil && result.previous.samples != nil {
		for key := range result.previous.samples.up {
			previousKeys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
	}
	items := []RuntimeAppTaskOperationItem{}
	for _, target := range result.items {
		bad, at := runtimeTargetSourceStatus(target, keys[target.TargetID], result.samples, selected, now)
		item := RuntimeAppTaskOperationItem{Job: result.job.Job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Process: target.Process, Signals: []RuntimeAppTaskOperationSignal{}}
		for _, op := range runtimeAppTaskOperationNames() {
			for _, outcome := range runtimeAppTaskOperationOutcomes() {
				s := RuntimeAppTaskOperationSignal{Operation: op, Outcome: outcome, State: bad.State, Reason: bad.Reason}
				if bad.State == "" {
					point, status := runtimeAppTaskOperationPoint(current, keys[target.TargetID], op, outcome, selected, now)
					s.Current = point
					s.State, s.Reason = status.State, status.Reason
					if point != nil {
						if selected.compareAt.IsZero() {
							s.State, s.Reason = "baseline", ""
						} else {
							previous, status := runtimeAppTaskOperationPoint(prior, previousKeys[target.TargetID], op, outcome, selected, selected.compareAt)
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
		}
		items = append(items, item)
	}
	return items
}
func aggregateRuntimeAppTaskOperations(base api.ObsRuntimeBuilds, rows [][]RuntimeAppTaskOperationItem) *RuntimeAppTaskOperations {
	jobs := runtimeAppTaskJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimeAppTaskOperations{RuntimeAppTaskOperationsSummary: newRuntimeAppTaskOperationsSummary(), Items: []RuntimeAppTaskOperationItem{}}
	if base.Process != nil {
		out.ComparisonAt = base.Process.ComparisonAt
	}
	inventoryUnknown := false
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected * runtimeAppTaskOperationPairs()
		inventoryUnknown = inventoryUnknown || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeAppTaskOperation(&out.RuntimeAppTaskOperationsSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = inventoryUnknown || runtimeAppTaskOperationUnknown(out.Counts) > 0 || out.Counts.Unexpected > 0
	out.State, out.Reason = runtimeAppTaskOperationsState(out.Counts, out.Incomplete, out.ComparisonAt != nil)
	slices.SortStableFunc(out.Items, func(a, b RuntimeAppTaskOperationItem) int {
		return runtimeAppTaskOperationPriority(a) - runtimeAppTaskOperationPriority(b)
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}
func runtimeAppTaskOperationPriority(item RuntimeAppTaskOperationItem) int {
	priority := 3
	for _, s := range item.Signals {
		if s.State == "increased" {
			if s.Outcome == "error" {
				return 0
			}
			priority = min(priority, 1)
		} else if s.State != "unchanged" && s.State != "baseline" {
			priority = min(priority, 2)
		}
	}
	return priority
}
