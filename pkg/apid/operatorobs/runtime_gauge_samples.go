package operatorobs

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"math"
	"slices"
	"strings"
	"time"
)

func runtimeGaugeQuery(job string, names []string) string {
	return fmt.Sprintf(`label_replace({job=%q,__name__=~%q}, "obs_metric", "$1", "__name__", "(.+)")`, job, strings.Join(names, "|"))
}

type runtimeGaugeSample struct {
	value, at float64
	conflict  bool
}

type runtimeGaugeSamples struct {
	rows   map[string]map[string]runtimeGaugeSample
	reason string
}

func readRuntimeGaugeSamples(ctx context.Context, client *promql.Client, builds runtimeBuildSamples, job, query string, known map[string]bool, now time.Time) runtimeGaugeSamples {
	return readRuntimeGaugeSamplesDimension(ctx, client, builds, job, query, known, "queue", now)
}

func readRuntimeGaugeSamplesDimension(ctx context.Context, client *promql.Client, builds runtimeBuildSamples, job, query string, known map[string]bool, dimension string, now time.Time) runtimeGaugeSamples {
	return readRuntimeGaugeSamplesDimensions(ctx, client, builds, job, query, known, []string{dimension}, now)
}

func readRuntimeGaugeSamplesDimensions(ctx context.Context, client *promql.Client, builds runtimeBuildSamples, job, query string, known map[string]bool, dimensions []string, now time.Time) runtimeGaugeSamples {
	sets := make([][]promql.VectorSample, 2)
	for i, q := range []string{query, "timestamp(" + query + ")"} {
		rows, err := inputVector(ctx, client, q, now)
		if err != nil {
			return runtimeGaugeSamples{reason: "source_unavailable"}
		}
		if len(rows) > api.ObsRuntimeBuildSamplesMax*len(known) {
			return runtimeGaugeSamples{reason: "invalid_evidence"}
		}
		sets[i] = rows
	}
	out := runtimeGaugeSamples{rows: map[string]map[string]runtimeGaugeSample{}}
	frames := [2]map[string]bool{{}, {}}
	for i, rows := range sets {
		seen, counts := map[string]bool{}, map[string]int{}
		for _, row := range rows {
			name := row.Labels["obs_metric"]
			parts := make([]string, len(dimensions))
			for d, dimension := range dimensions {
				parts[d] = row.Labels[dimension]
			}
			queue := strings.Join(parts, "/")
			signal := name + "/" + queue
			full, valid := inputSeriesKey(row.Labels, false)
			clean := make(map[string]string, len(row.Labels))
			for k, v := range row.Labels {
				if k != "obs_metric" && !slices.Contains(dimensions, k) {
					clean[k] = v
				}
			}
			key, targetOK := runtimeBuildTargetKey(clean)
			if !valid || !targetOK || row.Labels["job"] != job || !known[signal] || row.Labels["__name__"] != "" && row.Labels["__name__"] != name {
				return runtimeGaugeSamples{reason: "invalid_evidence"}
			}
			if _, exists := builds.up[key]; !exists {
				return runtimeGaugeSamples{reason: "invalid_evidence"}
			}
			counts[signal]++
			if counts[signal] > api.ObsRuntimeBuildSamplesMax {
				return runtimeGaugeSamples{reason: "invalid_evidence"}
			}
			if out.rows[key] == nil {
				out.rows[key] = map[string]runtimeGaugeSample{}
			}
			sample := out.rows[key][signal]
			sample.conflict = sample.conflict || seen[key+"\x00"+signal]
			seen[key+"\x00"+signal], frames[i][full] = true, true
			if i == 0 {
				sample.value = row.Value
			} else {
				sample.at = row.Value
			}
			out.rows[key][signal] = sample
		}
	}
	if len(frames[0]) != len(frames[1]) {
		return runtimeGaugeSamples{reason: "invalid_evidence"}
	}
	for full := range frames[0] {
		if !frames[1][full] {
			return runtimeGaugeSamples{reason: "invalid_evidence"}
		}
	}
	return out
}

func (s runtimeGaugeSamples) component(key, prefix, queue string, suffixes []string, selected RuntimeBuildSelection, now time.Time) (RuntimeBuilderSignal, map[string]float64) {
	bad := func(state, reason string) (RuntimeBuilderSignal, map[string]float64) {
		return RuntimeBuilderSignal{State: state, Reason: reason}, nil
	}
	if s.reason != "" {
		return bad("unavailable", s.reason)
	}
	values := map[string]float64{}
	var oldest time.Time
	for _, suffix := range suffixes {
		sample, exists := s.rows[key][prefix+suffix+"/"+queue]
		if !exists {
			return bad("missing", "metric_missing")
		}
		if sample.conflict {
			return bad("conflicting", "conflicting_series")
		}
		at, ok := inputSampleTime(sample.at, now)
		if !ok {
			return bad("unavailable", "invalid_sample_time")
		}
		if at.Before(now.Add(-api.ObsInputHealthMaxAge)) {
			return bad("stale", "metric_stale")
		}
		if at.Before(selected.after.Add(-api.ObsHealthClockSkewTolerance)) {
			return bad("unavailable", "metric_before_boundary")
		}
		if math.IsNaN(sample.value) || math.IsInf(sample.value, 0) {
			return bad("unavailable", "invalid_value")
		}
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
		values[suffix] = sample.value
	}
	return RuntimeBuilderSignal{State: "available", ObservedAt: &oldest}, values
}

// Gauges published atomically by one collector must come from the same scrape.
func (s runtimeGaugeSamples) coherentComponent(key, prefix string, names []string, selected RuntimeBuildSelection, now time.Time) (RuntimeBuilderSignal, map[string]float64) {
	status, values := s.component(key, prefix, "", names, selected, now)
	if status.State != "available" {
		return status, values
	}
	first := s.rows[key][prefix+names[0]+"/"].at
	for _, name := range names[1:] {
		if s.rows[key][prefix+name+"/"].at != first {
			return RuntimeBuilderSignal{State: "conflicting", Reason: "conflicting_sample_times"}, nil
		}
	}
	return status, values
}

func runtimeTargetSourceStatus(target api.ObsRuntimeBuildItem, key string, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) (RuntimeBuilderSignal, *time.Time) {
	var observedAt *time.Time
	bad := RuntimeBuilderSignal{State: "unavailable", Reason: "source_unavailable"}
	switch {
	case target.Reason == "expected_target_missing":
		bad.State, bad.Reason = "missing", "expected_target_missing"
	case target.Reason == "conflicting_targets":
		bad.State, bad.Reason = "conflicting", "conflicting_targets"
	case builds != nil && key != "":
		at, ok := inputSampleTime(builds.upTimes[key], now)
		if ok {
			observedAt = &at
		}
		switch {
		case !ok:
			bad.Reason = "invalid_sample_time"
		case at.Before(now.Add(-api.ObsInputHealthMaxAge)):
			bad.State, bad.Reason = "stale", "scrape_stale"
		case at.Before(selected.after.Add(-api.ObsHealthClockSkewTolerance)):
			bad.Reason = "scrape_before_boundary"
		case builds.up[key].Value != 1:
			bad.Reason = "scrape_down"
		default:
			bad = RuntimeBuilderSignal{}
		}
	}
	return bad, observedAt
}
