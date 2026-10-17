package operatorobs

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func inputSampleTime(value float64, now time.Time) (time.Time, bool) {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) || value > float64(now.Add(api.ObsHealthClockSkewTolerance).UnixMilli())/1000 {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(math.Round(value * 1000))).UTC(), true
}

// Target labels must match exactly except the selected gauge's daemon label.
// Collapse neither duplicate sources nor multiple series for the same target.
func inputSeriesKey(labels map[string]string, gauge bool) (string, bool) {
	if labels["instance"] == "" || labels["job"] == "" {
		return "", false
	}
	copyLabels := make(map[string]string, len(labels))
	for k, v := range labels {
		if k != "__name__" && (!gauge || k != "daemon") {
			copyLabels[k] = v
		}
	}
	data, err := json.Marshal(copyLabels)
	return string(data), err == nil
}

func inputSamples(rows []promql.VectorSample, job string, gauge bool) (map[string]float64, bool) {
	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		key, valid := inputSeriesKey(row.Labels, gauge)
		if !valid || row.Labels["job"] != job {
			return nil, false
		}
		if _, exists := out[key]; exists {
			return nil, false
		}
		out[key] = row.Value
	}
	return out, true
}

func readInputTargets(ctx context.Context, client *promql.Client, job string, expect api.ObsInputExpectation, now time.Time) (api.ObsInputTargets, map[string]bool) {
	out := api.ObsInputTargets{State: "unknown", Reason: "source_unavailable"}
	selector := fmt.Sprintf(`up{job=%q}`, job)
	values, err := inputVector(ctx, client, selector, now)
	if err != nil {
		return out, nil
	}
	times, err := inputVector(ctx, client, "timestamp("+selector+")", now)
	if err != nil {
		return out, nil
	}
	vs, vok := inputSamples(values, job, false)
	ts, tok := inputSamples(times, job, false)
	out.Reason = "invalid_evidence"
	if !vok || !tok || len(vs) != len(ts) {
		return out, nil
	}
	counts := &api.ObsInputTargetCounts{Observed: len(vs)}
	eligible := map[string]bool{}
	for key, value := range vs {
		timestamp, exists := ts[key]
		at, valid := inputSampleTime(timestamp, now)
		if !exists || !valid || (value != 0 && value != 1) {
			return out, nil
		}
		switch {
		case at.Before(now.Add(-api.ObsInputHealthMaxAge)):
			counts.Stale++
		case value == 0:
			counts.Down++
		default:
			counts.FreshUp++
			eligible[key] = true
		}
	}
	if expect.MinimumTargets != nil {
		counts.MissingMinimum = inputInt(max(0, *expect.MinimumTargets-counts.Observed))
		if expect.Source == "compute_discovery" {
			counts.Extra = inputInt(max(0, counts.Observed-*expect.MinimumTargets))
		}
	}
	out.Counts, out.State, out.Reason = counts, "healthy", ""
	switch {
	case counts.Down > 0 || counts.Stale > 0 || (counts.MissingMinimum != nil && *counts.MissingMinimum > 0):
		out.State, out.Reason = "degraded", "scrape_coverage_gap"
	case counts.Extra != nil && *counts.Extra > 0:
		out.State, out.Reason = "partial", "inventory_count_mismatch"
	case expect.MinimumTargets == nil:
		out.State, out.Reason = "unknown", "expectation_unavailable"
	}
	return out, eligible
}

func readInputMetric(ctx context.Context, client *promql.Client, spec inputJobSpec, eligible map[string]bool, expect api.ObsInputExpectation, now time.Time) api.ObsInputMetric {
	out := api.ObsInputMetric{Metric: spec.metric, State: "unknown", Reason: "no_fresh_up_targets"}
	if len(eligible) == 0 {
		if expect.State == "healthy" && expect.MinimumTargets != nil && *expect.MinimumTargets == 0 {
			out.State, out.Reason, out.Counts = "not_applicable", "zero_expected_targets", &api.ObsInputMetricCounts{}
		}
		return out
	}
	out.Reason = "source_unavailable"
	rows, err := inputVector(ctx, client, inputMetricQuery(spec), now)
	if err != nil {
		return out
	}
	for _, row := range rows {
		if row.Labels["daemon"] != spec.daemon {
			out.Reason = "invalid_evidence"
			return out
		}
	}
	observed, valid := inputSamples(rows, spec.job, true)
	out.Reason = "invalid_evidence"
	if !valid {
		return out
	}
	for _, value := range observed {
		if _, ok := inputSampleTime(value, now); !ok {
			return out
		}
	}
	counts := &api.ObsInputMetricCounts{Expected: len(eligible)}
	for key := range eligible {
		value, exists := observed[key]
		at, _ := inputSampleTime(value, now)
		switch {
		case !exists:
			counts.Missing++
		case at.Before(now.Add(-api.ObsInputHealthMaxAge)):
			counts.Stale++
		default:
			counts.Fresh++
		}
	}
	out.State, out.Reason, out.Counts = "healthy", "", counts
	if counts.Missing > 0 || counts.Stale > 0 {
		out.State, out.Reason = "degraded", "metric_sample_gap"
	}
	return out
}

func readInputExpectation(ctx context.Context, client *promql.Client, job string, now time.Time) api.ObsInputExpectation {
	out := api.ObsInputExpectation{Source: "compute_discovery", State: "unknown", Reason: "discovery_unavailable"}
	values := map[string]float64{}
	sourceKey := ""
	var oldest time.Time
	for _, name := range []string{"targets", "registry_nodes", "invalid_targets", "last_success_timestamp_seconds"} {
		out.Reason = "discovery_unavailable"
		selector := fmt.Sprintf(`apid_metrics_discovery_%s{job="apid",exported_job=%q}`, name, job)
		rows, err := inputVector(ctx, client, selector, now)
		if err != nil {
			return out
		}
		times, err := inputVector(ctx, client, "timestamp("+selector+")", now)
		if err != nil {
			return out
		}
		if len(rows) == 0 || len(times) == 0 {
			out.Reason = "discovery_missing"
			return out
		}
		out.Reason = "discovery_invalid"
		if len(rows) != 1 || len(times) != 1 || rows[0].Labels["exported_job"] != job || rows[0].Labels["job"] != "apid" {
			return out
		}
		key, valid := inputSeriesKey(rows[0].Labels, false)
		timeKey, timeValid := inputSeriesKey(times[0].Labels, false)
		if !valid || !timeValid || key != timeKey || (sourceKey != "" && key != sourceKey) {
			return out
		}
		sourceKey = key
		at, valid := inputSampleTime(times[0].Value, now)
		if !valid {
			return out
		}
		if at.Before(now.Add(-api.ObsInputHealthMaxAge)) {
			out.Reason = "discovery_stale"
			return out
		}
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
		value := rows[0].Value
		if name != "last_success_timestamp_seconds" && (value < 0 || value > api.ObsInputHealthTargetsMax || value != math.Trunc(value)) {
			return out
		}
		values[name] = value
	}
	success, valid := inputSampleTime(values["last_success_timestamp_seconds"], now)
	if !valid && values["last_success_timestamp_seconds"] != 0 {
		return out
	}
	if !valid || success.Before(now.Add(-api.ObsInputHealthMaxAge)) {
		out.Reason = "discovery_stale"
		return out
	}
	if success.Before(oldest) {
		oldest = success
	}
	if values["targets"]+values["invalid_targets"] != values["registry_nodes"] {
		return out
	}
	out.State, out.Reason, out.MinimumTargets, out.InvalidTargets, out.ObservedAt = "healthy", "", inputInt(int(values["targets"])), inputInt(int(values["invalid_targets"])), &oldest
	if *out.InvalidTargets > 0 {
		out.State, out.Reason = "degraded", "discovery_invalid_targets"
	}
	return out
}
