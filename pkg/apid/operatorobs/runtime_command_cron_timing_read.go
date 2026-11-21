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

func runtimeCommandCronTimingMetric(d RuntimeCommandCronTimingDimension) (string, string) {
	switch d.Signal {
	case "timed":
		return "schedd_command_cron_admission_lag_seconds_count", ""
	case "unknown_timestamps":
		return "schedd_command_cron_admission_lag_unknown_total", ""
	case "le_inf":
		return "schedd_command_cron_admission_lag_seconds_bucket", "+Inf"
	default:
		return "schedd_command_cron_admission_lag_seconds_bucket", strings.TrimSuffix(strings.TrimPrefix(d.Signal, "le_"), "_seconds")
	}
}
func runtimeCommandCronTimingQuery(job string) string {
	return runtimeGaugeQuery(job, []string{"schedd_command_cron_admission_lag_seconds_bucket", "schedd_command_cron_admission_lag_seconds_count", "schedd_command_cron_admission_lag_unknown_total"})
}

func readRuntimeCommandCronTimingSamples(ctx context.Context, client *promql.Client, result *runtimeBuildJobResult, now time.Time) runtimeGaugeSamples {
	if result == nil || result.samples == nil || len(result.samples.up) == 0 {
		return runtimeGaugeSamples{reason: "source_unavailable"}
	}
	known := map[string]bool{}
	for _, d := range runtimeCommandCronTimingDimensions() {
		m, dims := runtimeCommandCronTimingMetric(d)
		known[m+"/"+dims] = true
	}
	return readRuntimeGaugeSamplesDimensions(ctx, client, *result.samples, result.job.Job, runtimeCommandCronTimingQuery(result.job.Job), known, []string{"le"}, now)
}

func readRuntimeCommandCronTimingJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeCommandCronTimingItem {
	current := readRuntimeCommandCronTimingSamples(ctx, client, &result, now)
	prior := runtimeGaugeSamples{reason: "source_unavailable"}
	if !selected.compareAt.IsZero() {
		prior = readRuntimeCommandCronTimingSamples(ctx, client, result.previous, selected.compareAt)
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
	items := []RuntimeCommandCronTimingItem{}
	for _, target := range result.items {
		bad, at := runtimeTargetSourceStatus(target, keys[target.TargetID], result.samples, selected, now)
		item := RuntimeCommandCronTimingItem{Job: result.job.Job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Process: target.Process, Signals: []RuntimeCommandCronTimingSignal{}}
		for _, d := range runtimeCommandCronTimingDimensions() {
			s := RuntimeCommandCronTimingSignal{RuntimeCommandCronTimingDimension: d, State: bad.State, Reason: bad.Reason}
			if bad.State == "" {
				m, dims := runtimeCommandCronTimingMetric(d)
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
		normalizeRuntimeCommandCronTimingHistogram(&item, !selected.compareAt.IsZero())
		items = append(items, item)
	}
	return items
}

func aggregateRuntimeCommandCronTiming(base api.ObsRuntimeBuilds, rows [][]RuntimeCommandCronTimingItem) *RuntimeCommandCronTiming {
	jobs := runtimeCommandCronJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimeCommandCronTiming{RuntimeCommandCronTimingSummary: newRuntimeCommandCronTimingSummary(), Items: []RuntimeCommandCronTimingItem{}}
	if base.Process != nil {
		out.ComparisonAt = base.Process.ComparisonAt
	}
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected * len(runtimeCommandCronTimingDimensions())
		out.Incomplete = out.Incomplete || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeCommandCronTiming(&out.RuntimeCommandCronTimingSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = out.Incomplete || runtimeAppTaskOperationUnknown(out.Counts) > 0 || out.Counts.Unexpected > 0
	out.State, out.Reason = runtimeCommandCronTimingState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeCommandCronTimingItem) int {
		return runtimeCommandCronTimingPriority(a) - runtimeCommandCronTimingPriority(b)
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeCommandCronTimingPriority(item RuntimeCommandCronTimingItem) int {
	priority := 3
	for _, s := range item.Signals {
		if s.State == "increased" {
			if s.Signal == "unknown_timestamps" {
				return 0
			}
			priority = min(priority, 1)
		} else if s.State != "unchanged" && s.State != "baseline" {
			priority = min(priority, 2)
		}
	}
	return priority
}
