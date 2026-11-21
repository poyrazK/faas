package operatorobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

type runtimeLoopPair struct {
	rows      map[string]promql.VectorSample
	times     map[string]float64
	conflicts map[string]bool
	reason    string
}

func runtimeLoopTargetKey(labels map[string]string) (string, bool) {
	clean := make(map[string]string, len(labels))
	for k, v := range labels {
		if k != "loop" {
			clean[k] = v
		}
	}
	return runtimeBuildTargetKey(clean)
}

func readRuntimeLoopPair(ctx context.Context, client *promql.Client, spec runtimeBuildJobSpec, loop, metric string, builds runtimeBuildSamples, now time.Time) runtimeLoopPair {
	query := fmt.Sprintf(`%s_loop_%s{job=%q,loop=%q}`, strings.TrimSuffix(spec.metric, "_daemon_build_info"), metric, spec.job, loop)
	sets := make([][]promql.VectorSample, 2)
	for i, q := range []string{query, "timestamp(" + query + ")"} {
		rows, err := inputVector(ctx, client, q, now)
		if err != nil {
			return runtimeLoopPair{reason: "source_unavailable"}
		}
		if len(rows) > api.ObsRuntimeBuildSamplesMax {
			return runtimeLoopPair{reason: "invalid_evidence"}
		}
		sets[i] = rows
	}
	out := runtimeLoopPair{rows: map[string]promql.VectorSample{}, times: map[string]float64{}, conflicts: map[string]bool{}}
	fullRows, fullTimes := map[string]bool{}, map[string]bool{}
	for i, set := range sets {
		for _, row := range set {
			full, ok := inputSeriesKey(row.Labels, false)
			key, targetOK := runtimeLoopTargetKey(row.Labels)
			if !ok || !targetOK || row.Labels["job"] != spec.job || row.Labels["loop"] != loop {
				return runtimeLoopPair{reason: "invalid_evidence"}
			}
			if _, exists := builds.up[key]; !exists {
				return runtimeLoopPair{reason: "invalid_evidence"}
			}
			if i == 0 {
				if _, exists := out.rows[key]; exists {
					out.conflicts[key] = true
				}
				out.rows[key] = row
				fullRows[full] = true
			} else {
				if _, exists := out.times[key]; exists {
					out.conflicts[key] = true
				}
				out.times[key] = row.Value
				fullTimes[full] = true
			}
		}
	}
	if len(fullRows) != len(fullTimes) {
		return runtimeLoopPair{reason: "invalid_evidence"}
	}
	for key := range fullRows {
		if !fullTimes[key] {
			return runtimeLoopPair{reason: "invalid_evidence"}
		}
	}
	return out
}

type runtimeLoopSignal struct {
	state, reason string
	at            *time.Time
	value         *float64
}

func assessRuntimeLoopSignal(pair runtimeLoopPair, key, metric string, selected RuntimeBuildSelection, now time.Time) runtimeLoopSignal {
	out := runtimeLoopSignal{state: "unavailable", reason: pair.reason}
	if pair.reason != "" {
		return out
	}
	if pair.conflicts[key] {
		out.state, out.reason = "conflicting", "conflicting_loop_series"
		return out
	}
	row, exists := pair.rows[key]
	if !exists {
		out.state, out.reason = "missing", "loop_"+metric+"_missing"
		return out
	}
	at, ok := inputSampleTime(pair.times[key], now)
	if !ok {
		out.reason = "invalid_sample_time"
		return out
	}
	out.at = &at
	if at.Before(now.Add(-api.ObsInputHealthMaxAge)) {
		out.state, out.reason = "stale", "loop_sample_stale"
		return out
	}
	if at.Before(selected.after.Add(-api.ObsHealthClockSkewTolerance)) {
		out.reason = "loop_sample_before_boundary"
		return out
	}
	if metric == "stall" && row.Value != 0 && row.Value != 1 {
		out.reason = "invalid_stall_value"
		return out
	}
	if metric == "age" && !runtimeUptimeValid(row.Value) {
		out.reason = "invalid_loop_age"
		return out
	}
	out.state, out.reason, out.value = "fresh", "", &row.Value
	return out
}

func assessRuntimeLoopItem(item api.ObsRuntimeBuildItem, loop, key string, stall, age runtimeLoopPair, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimeLoopItem {
	out := RuntimeLoopItem{Job: item.Job, Daemon: item.Daemon, Node: item.Node, TargetID: item.TargetID, Unexpected: item.Unexpected, Loop: loop, State: "unavailable", Reason: "source_unavailable"}
	if builds == nil {
		return out
	}
	if item.TargetID == "" {
		out.State, out.Reason = "missing", "expected_target_missing"
		return out
	}
	if item.Reason == "conflicting_targets" {
		out.State, out.Reason = "conflicting", "conflicting_targets"
		return out
	}
	if key == "" {
		out.Reason = "invalid_evidence"
		return out
	}
	upAt, ok := inputSampleTime(builds.upTimes[key], now)
	if !ok {
		out.Reason = "invalid_sample_time"
		return out
	}
	out.ScrapeObservedAt = &upAt
	if upAt.Before(now.Add(-api.ObsInputHealthMaxAge)) {
		out.State, out.Reason = "stale", "scrape_stale"
		return out
	}
	if upAt.Before(selected.after.Add(-api.ObsHealthClockSkewTolerance)) {
		out.Reason = "scrape_before_boundary"
		return out
	}
	if builds.up[key].Value != 1 {
		out.Reason = "scrape_down"
		return out
	}
	reported := assessRuntimeLoopSignal(stall, key, "stall", selected, now)
	beat := assessRuntimeLoopSignal(age, key, "age", selected, now)
	out.StallObservedAt, out.AgeObservedAt = reported.at, beat.at
	if reported.value != nil {
		value := *reported.value == 1
		out.ReportedStalled = &value
	}
	out.AgeSeconds = beat.value
	switch {
	case reported.state != "fresh":
		out.State, out.Reason = reported.state, reported.reason
	case beat.state != "fresh":
		out.State, out.Reason = beat.state, beat.reason
	case *out.ReportedStalled:
		out.State, out.Reason = "stalled", ""
	default:
		out.State, out.Reason = "no_reported_stall", ""
	}
	return out
}
