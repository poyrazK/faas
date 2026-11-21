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

// Keep in wire's one-hot order. No raw readiness text leaves this reader.
func runtimeReadinessReasons() []string {
	return []string{"ready", "draining", "database", "vmmd", "grpc", "storage", "credentials", "stale", "process", "other"}
}

func runtimeReadinessKey(labels map[string]string) (string, bool) {
	clean := make(map[string]string, len(labels))
	for k, v := range labels {
		if k != "reason" {
			clean[k] = v
		}
	}
	return runtimeBuildTargetKey(clean)
}

type runtimeReadinessSamples struct {
	values      map[string][]promql.VectorSample
	reasons     map[string][]promql.VectorSample
	times       map[string]float64
	reasonTimes map[string]float64
}

func readRuntimeReadiness(ctx context.Context, client *promql.Client, spec runtimeBuildJobSpec, result *runtimeBuildJobResult, builds runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) {
	ctx = runtimeQueryContext(ctx, "readiness")
	prefix := strings.TrimSuffix(spec.metric, "_build_info")
	ready := fmt.Sprintf(`%s_ready{job=%q,daemon=%q}`, prefix, spec.job, spec.daemon)
	reason := fmt.Sprintf(`%s_ready_reason{job=%q,daemon=%q}`, prefix, spec.job, spec.daemon)
	rows := make([][]promql.VectorSample, 4)
	for i, query := range []string{ready, "timestamp(" + ready + ")", reason, "timestamp(" + reason + ")"} {
		set, err := inputVector(ctx, client, query, now)
		if err != nil {
			setRuntimeReadinessUnavailable(result, "source_unavailable")
			return
		}
		limit := api.ObsRuntimeBuildSamplesMax
		if i >= 2 {
			limit = api.ObsRuntimeReadinessReasonSamplesMax
		}
		if len(set) > limit {
			setRuntimeReadinessUnavailable(result, "invalid_evidence")
			return
		}
		rows[i] = set
	}
	samples, ok := parseRuntimeReadiness(rows, spec, builds)
	if !ok {
		setRuntimeReadinessUnavailable(result, "invalid_evidence")
		return
	}
	byID := map[string]string{}
	for key := range builds.up {
		byID[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
	}
	for i := range result.items {
		item := &result.items[i]
		key, exists := byID[item.TargetID]
		switch {
		case item.Reason == "conflicting_targets":
			item.Readiness = &api.ObsRuntimeReadinessItem{State: "conflicting", Reason: "conflicting_targets"}
		case !exists:
			item.Readiness = &api.ObsRuntimeReadinessItem{State: "missing", Reason: "expected_target_missing"}
		default:
			item.Readiness = assessRuntimeReadiness(key, samples, builds, selected, now)
		}
	}
}

func setRuntimeReadinessUnavailable(result *runtimeBuildJobResult, reason string) {
	for i := range result.items {
		result.items[i].Readiness = &api.ObsRuntimeReadinessItem{State: "unavailable", Reason: reason}
	}
}

func parseRuntimeReadiness(rows [][]promql.VectorSample, spec runtimeBuildJobSpec, builds runtimeBuildSamples) (runtimeReadinessSamples, bool) {
	out := runtimeReadinessSamples{values: map[string][]promql.VectorSample{}, reasons: map[string][]promql.VectorSample{}, times: map[string]float64{}, reasonTimes: map[string]float64{}}
	for i, set := range rows {
		seen := map[string]bool{}
		for _, row := range set {
			full, ok := inputSeriesKey(row.Labels, false)
			key, targetOK := runtimeReadinessKey(row.Labels)
			if !ok || !targetOK || seen[full] || row.Labels["job"] != spec.job || row.Labels["daemon"] != spec.daemon {
				return out, false
			}
			if _, exists := builds.up[key]; !exists {
				return out, false
			}
			seen[full] = true
			if i >= 2 && !slices.Contains(runtimeReadinessReasons(), row.Labels["reason"]) {
				return out, false
			}
			switch i {
			case 0:
				out.values[key] = append(out.values[key], row)
			case 1:
				out.times[full] = row.Value
			case 2:
				out.reasons[key] = append(out.reasons[key], row)
			case 3:
				out.reasonTimes[full] = row.Value
			}
		}
	}
	for i, values := range []map[string][]promql.VectorSample{out.values, out.reasons} {
		times := out.times
		if i == 1 {
			times = out.reasonTimes
		}
		n := 0
		for _, set := range values {
			for _, row := range set {
				full, _ := inputSeriesKey(row.Labels, false)
				if _, exists := times[full]; !exists {
					return out, false
				}
				n++
			}
		}
		if n != len(times) {
			return out, false
		}
	}
	return out, true
}

func assessRuntimeReadiness(key string, samples runtimeReadinessSamples, builds runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) *api.ObsRuntimeReadinessItem {
	out := &api.ObsRuntimeReadinessItem{State: "unavailable", Reason: "invalid_sample_time"}
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
	values, reasons := samples.values[key], samples.reasons[key]
	if len(values) == 0 || len(reasons) == 0 {
		out.State, out.Reason = "missing", "readiness_missing"
		return out
	}
	if len(values) != 1 {
		out.State, out.Reason = "conflicting", "conflicting_readiness"
		return out
	}
	active, classes := "", map[string]bool{}
	for i, set := range [][]promql.VectorSample{values, reasons} {
		times := samples.times
		if i == 1 {
			times = samples.reasonTimes
		}
		for _, row := range set {
			full, _ := inputSeriesKey(row.Labels, false)
			at, valid := inputSampleTime(times[full], now)
			if !valid {
				return out
			}
			if at.Before(now.Add(-api.ObsInputHealthMaxAge)) {
				out.State, out.Reason = "stale", "readiness_stale"
				return out
			}
			if at.Before(selected.after.Add(-api.ObsHealthClockSkewTolerance)) {
				out.Reason = "readiness_before_boundary"
				return out
			}
			if row.Value != 0 && row.Value != 1 {
				out.State, out.Reason = "conflicting", "conflicting_readiness"
				return out
			}
			if i == 0 {
				out.ObservedAt = &at
				continue
			}
			class := row.Labels["reason"]
			if classes[class] {
				out.State, out.Reason = "conflicting", "conflicting_readiness"
				return out
			}
			classes[class] = true
			if row.Value == 1 {
				if active != "" {
					out.State, out.Reason = "conflicting", "conflicting_readiness"
					return out
				}
				active = class
				out.ReasonObservedAt = &at
			}
		}
	}
	if len(classes) != api.ObsRuntimeReadinessReasonsMax {
		out.State, out.Reason = "missing", "readiness_missing"
		return out
	}
	if active == "" || (values[0].Value == 1) != (active == "ready") {
		out.State, out.Reason = "conflicting", "conflicting_readiness"
		return out
	}
	out.State, out.Reason = "not_ready", active
	if active == "ready" {
		out.State = "ready"
	}
	return out
}

func runtimeReadinessState(c api.ObsRuntimeReadinessCounts, incomplete bool) (string, string) {
	switch {
	case c.NotReady > 0:
		return "not_ready", "fresh_not_ready"
	case incomplete || c.Missing+c.Stale+c.Conflicting+c.Unavailable > 0:
		return "incomplete", "incomplete_readiness_evidence"
	case c.Ready > 0:
		return "ready", ""
	default:
		return "unknown", "readiness_unavailable"
	}
}

func aggregateRuntimeReadiness(out *api.ObsRuntimeBuilds, results []runtimeBuildJobResult) {
	summary := &api.ObsRuntimeReadiness{Reasons: []api.ObsRuntimeReadinessReason{}}
	reasons := map[string]int{}
	incomplete := false
	for _, result := range results {
		incomplete = incomplete || result.job.State != "available"
		for i := range result.items {
			item := &result.items[i]
			if item.Readiness == nil {
				item.Readiness = &api.ObsRuntimeReadinessItem{State: "unavailable", Reason: "source_unavailable"}
			}
			incomplete = incomplete || item.Unexpected
			r := item.Readiness
			switch r.State {
			case "ready":
				summary.Counts.Ready++
				reasons[r.Reason]++
			case "not_ready":
				summary.Counts.NotReady++
				reasons[r.Reason]++
			case "missing":
				summary.Counts.Missing++
			case "stale":
				summary.Counts.Stale++
			case "conflicting":
				summary.Counts.Conflicting++
			default:
				summary.Counts.Unavailable++
			}
		}
	}
	for _, class := range runtimeReadinessReasons() {
		if reasons[class] > 0 {
			summary.Reasons = append(summary.Reasons, api.ObsRuntimeReadinessReason{Reason: class, Count: reasons[class]})
		}
	}
	summary.State, summary.Reason = runtimeReadinessState(summary.Counts, incomplete)
	out.Readiness = summary
}
