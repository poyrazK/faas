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

func runtimeCommandCronOutcomeMetric(d RuntimeCommandCronOutcomeDimension) (string, string) {
	switch d.Source {
	case "sweep":
		return "schedd_command_cron_sweeps_total", d.Outcome
	case "decision":
		return "schedd_command_cron_decisions_total", d.Outcome
	default:
		return "schedd_command_cron_admissions_total", d.Outcome
	}
}
func runtimeCommandCronOutcomeQuery(job string) string {
	return runtimeGaugeQuery(job, []string{"schedd_command_cron_sweeps_total", "schedd_command_cron_decisions_total", "schedd_command_cron_admissions_total"})
}

func readRuntimeCommandCronOutcomeSamples(ctx context.Context, client *promql.Client, result *runtimeBuildJobResult, now time.Time) runtimeGaugeSamples {
	if result == nil || result.samples == nil || len(result.samples.up) == 0 {
		return runtimeGaugeSamples{reason: "source_unavailable"}
	}
	known := map[string]bool{}
	for _, d := range runtimeCommandCronOutcomeDimensions() {
		m, dims := runtimeCommandCronOutcomeMetric(d)
		known[m+"/"+dims] = true
	}
	return readRuntimeGaugeSamplesDimensions(ctx, client, *result.samples, result.job.Job, runtimeCommandCronOutcomeQuery(result.job.Job), known, []string{"outcome"}, now)
}

func readRuntimeCommandCronOutcomesJob(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeCommandCronOutcomeItem {
	current := readRuntimeCommandCronOutcomeSamples(ctx, client, &result, now)
	prior := runtimeGaugeSamples{reason: "source_unavailable"}
	if !selected.compareAt.IsZero() {
		prior = readRuntimeCommandCronOutcomeSamples(ctx, client, result.previous, selected.compareAt)
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
	items := []RuntimeCommandCronOutcomeItem{}
	for _, target := range result.items {
		bad, at := runtimeTargetSourceStatus(target, keys[target.TargetID], result.samples, selected, now)
		item := RuntimeCommandCronOutcomeItem{Job: result.job.Job, Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at, Process: target.Process, Signals: []RuntimeCommandCronOutcomeSignal{}}
		for _, d := range runtimeCommandCronOutcomeDimensions() {
			s := RuntimeCommandCronOutcomeSignal{RuntimeCommandCronOutcomeDimension: d, State: bad.State, Reason: bad.Reason}
			if bad.State == "" {
				m, dims := runtimeCommandCronOutcomeMetric(d)
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

func aggregateRuntimeCommandCronOutcomes(base api.ObsRuntimeBuilds, rows [][]RuntimeCommandCronOutcomeItem) *RuntimeCommandCronOutcomes {
	jobs := runtimeCommandCronJobs(base.ExpectedRole)
	if len(jobs) == 0 {
		return nil
	}
	out := &RuntimeCommandCronOutcomes{RuntimeCommandCronOutcomesSummary: newRuntimeCommandCronOutcomesSummary(), Items: []RuntimeCommandCronOutcomeItem{}}
	if base.Process != nil {
		out.ComparisonAt = base.Process.ComparisonAt
	}
	for i, job := range base.Jobs {
		if !slices.Contains(jobs, job.Job) {
			continue
		}
		out.Counts.Expected += job.Expected * len(runtimeCommandCronOutcomeDimensions())
		out.Incomplete = out.Incomplete || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeCommandCronOutcomes(&out.RuntimeCommandCronOutcomesSummary, item)
		}
		out.Items = append(out.Items, rows[i]...)
	}
	out.Incomplete = out.Incomplete || runtimeAppTaskOperationUnknown(out.Counts) > 0 || out.Counts.Unexpected > 0
	out.State, out.Reason = runtimeCommandCronOutcomesState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeCommandCronOutcomeItem) int {
		return runtimeCommandCronOutcomePriority(a) - runtimeCommandCronOutcomePriority(b)
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeCommandCronOutcomePriority(item RuntimeCommandCronOutcomeItem) int {
	priority := 3
	for _, s := range item.Signals {
		if s.State == "increased" {
			if s.Outcome == "error" || s.Source == "decision" && (s.Outcome == "lookup_error" || s.Outcome == "store_unavailable" || s.Outcome == "legacy_error" || s.Outcome == "invalid_schedule") {
				return 0
			}
			priority = min(priority, 1)
		} else if s.State != "unchanged" && s.State != "baseline" {
			priority = min(priority, 2)
		}
	}
	return priority
}
