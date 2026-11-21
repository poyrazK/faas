package operatorobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

func readRuntimeBuildJob(ctx context.Context, client *promql.Client, spec runtimeBuildJobSpec, nodes []state.ComputeNode, inventoryOK bool, selected RuntimeBuildSelection, now time.Time) runtimeBuildJobResult {
	result := readRuntimeBuildIdentityJob(ctx, client, spec, nodes, inventoryOK, selected, now)
	readRuntimeProcessUptime(ctx, client, spec, &result, selected, now)
	var previous *runtimeBuildJobResult
	if !selected.compareAt.IsZero() && result.samples != nil {
		prior := readRuntimeBuildIdentityJob(ctx, client, spec, nodes, inventoryOK, selected, selected.compareAt)
		readRuntimeProcessUptime(ctx, client, spec, &prior, selected, selected.compareAt)
		previous = &prior
	}
	if slices.Contains(runtimeAppTaskJobs(selected.role), spec.job) {
		result.previous = previous
	}
	compareRuntimeProcessJob(&result, previous, selected)
	// Optional readers cannot invalidate already collected identity or uptime.
	if result.samples != nil {
		readRuntimeReadiness(ctx, client, spec, &result, *result.samples, selected, now)
	}
	return result
}

func readRuntimeProcessUptime(ctx context.Context, client *promql.Client, spec runtimeBuildJobSpec, result *runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) {
	ctx = runtimeQueryContext(ctx, "process")
	if result.samples == nil {
		setRuntimeProcessUnavailable(result, "source_unavailable")
		return
	}
	query := fmt.Sprintf(`%s_uptime_seconds{job=%q,daemon=%q}`, strings.TrimSuffix(spec.metric, "_build_info"), spec.job, spec.daemon)
	rows := make([][]promql.VectorSample, 2)
	for i, q := range []string{query, "timestamp(" + query + ")"} {
		set, err := inputVector(ctx, client, q, now)
		if err != nil {
			setRuntimeProcessUnavailable(result, "source_unavailable")
			return
		}
		if len(set) > api.ObsRuntimeBuildSamplesMax {
			setRuntimeProcessUnavailable(result, "invalid_evidence")
			return
		}
		rows[i] = set
	}
	samples, ok := parseRuntimeProcessSamples(rows, spec, *result.samples)
	if !ok {
		setRuntimeProcessUnavailable(result, "invalid_evidence")
		return
	}
	for i := range result.items {
		item := &result.items[i]
		if item.Reason == "conflicting_targets" {
			item.Process = &api.ObsRuntimeProcessItem{State: "conflicting", Reason: "conflicting_targets"}
			continue
		}
		key, exists := samples.keys[item.TargetID]
		if !exists {
			item.Process = &api.ObsRuntimeProcessItem{State: "missing", Reason: "expected_target_missing"}
			continue
		}
		item.Process = assessRuntimeProcessUptime(key, samples, *result.samples, selected, now)
	}
}

type runtimeProcessSamples struct {
	values map[string][]promql.VectorSample
	times  map[string]float64
	keys   map[string]string
}

func parseRuntimeProcessSamples(rows [][]promql.VectorSample, spec runtimeBuildJobSpec, builds runtimeBuildSamples) (runtimeProcessSamples, bool) {
	out := runtimeProcessSamples{values: map[string][]promql.VectorSample{}, times: map[string]float64{}, keys: map[string]string{}}
	for key := range builds.up {
		out.keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
	}
	for i, set := range rows {
		seen := map[string]bool{}
		for _, row := range set {
			full, ok := inputSeriesKey(row.Labels, false)
			key, targetOK := runtimeBuildTargetKey(row.Labels)
			if !ok || !targetOK || seen[full] || row.Labels["job"] != spec.job || row.Labels["daemon"] != spec.daemon {
				return out, false
			}
			if _, exists := builds.up[key]; !exists {
				return out, false
			}
			seen[full] = true
			if i == 0 {
				out.values[key] = append(out.values[key], row)
			} else {
				out.times[full] = row.Value
			}
		}
	}
	n := 0
	for _, set := range out.values {
		for _, row := range set {
			full, _ := inputSeriesKey(row.Labels, false)
			if _, ok := out.times[full]; !ok {
				return out, false
			}
			n++
		}
	}
	return out, n == len(out.times)
}

func setRuntimeProcessUnavailable(result *runtimeBuildJobResult, reason string) {
	for i := range result.items {
		result.items[i].Process = &api.ObsRuntimeProcessItem{State: "unavailable", Reason: reason}
	}
}

func assessRuntimeProcessUptime(key string, samples runtimeProcessSamples, builds runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) *api.ObsRuntimeProcessItem {
	out := &api.ObsRuntimeProcessItem{State: "unavailable", Reason: "invalid_sample_time"}
	upAt, ok := inputSampleTime(builds.upTimes[key], now)
	if !ok {
		return out
	}
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
	rows := samples.values[key]
	if len(rows) == 0 {
		out.State, out.Reason = "missing", "uptime_missing"
		return out
	}
	if len(rows) != 1 {
		out.State, out.Reason = "conflicting", "conflicting_uptime"
		return out
	}
	row := rows[0]
	full, _ := inputSeriesKey(row.Labels, false)
	at, ok := inputSampleTime(samples.times[full], now)
	if !ok {
		return out
	}
	if at.Before(now.Add(-api.ObsInputHealthMaxAge)) {
		out.State, out.Reason = "stale", "uptime_stale"
		return out
	}
	if at.Before(selected.after.Add(-api.ObsHealthClockSkewTolerance)) {
		out.Reason = "uptime_before_boundary"
		return out
	}
	if !runtimeUptimeValid(row.Value) {
		out.Reason = "invalid_uptime"
		return out
	}
	out.State, out.Reason, out.ObservedAt, out.UptimeSeconds = "fresh", "", &at, &row.Value
	return out
}

func runtimeUptimeValid(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= api.ObsRuntimeProcessUptimeMax.Seconds()
}
