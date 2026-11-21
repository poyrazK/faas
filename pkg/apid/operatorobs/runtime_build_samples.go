package operatorobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeBuildJobResult struct {
	previous          *runtimeBuildJobResult
	job               api.ObsRuntimeBuildJob
	items             []api.ObsRuntimeBuildItem
	samples           *runtimeBuildSamples
	processCounts     api.ObsRuntimeProcessCounts
	processIncomplete bool
}

type runtimeBuildSamples struct {
	up         map[string]promql.VectorSample
	upTimes    map[string]float64
	builds     map[string][]promql.VectorSample
	buildTimes map[string]float64
}

func runtimeBuildTargetKey(labels map[string]string) (string, bool) {
	clean := make(map[string]string, len(labels))
	for k, v := range labels {
		if k != "daemon" && k != "version" && k != "git_sha" && k != "build_time" {
			clean[k] = v
		}
	}
	return inputSeriesKey(clean, false)
}

func readRuntimeBuildIdentityJob(ctx context.Context, client *promql.Client, spec runtimeBuildJobSpec, nodes []state.ComputeNode, inventoryOK bool, selected RuntimeBuildSelection, now time.Time) runtimeBuildJobResult {
	ctx = runtimeQueryContext(ctx, "identity")
	job := api.ObsRuntimeBuildJob{Job: spec.job, Daemon: spec.daemon, InventorySource: "scrape_targets_minimum", Expected: 1, State: "available"}
	if spec.discovery {
		job.InventorySource, job.Expected = "active_configured_compute_registry", len(nodes)
		if !inventoryOK {
			job.State, job.Reason = "unknown", "inventory_unavailable"
			return runtimeBuildJobResult{job: job}
		}
	}
	up := fmt.Sprintf(`up{job=%q}`, spec.job)
	build := fmt.Sprintf(`%s{job=%q,daemon=%q}`, spec.metric, spec.job, spec.daemon)
	queries := []string{up, "timestamp(" + up + ")", build, "timestamp(" + build + ")"}
	rows := make([][]promql.VectorSample, len(queries))
	for i, query := range queries {
		var err error
		rows[i], err = inputVector(ctx, client, query, now)
		if err != nil {
			return unavailableRuntimeBuildJob(job, nodes, spec.discovery, "source_unavailable")
		}
		if len(rows[i]) > api.ObsRuntimeBuildSamplesMax {
			return unavailableRuntimeBuildJob(job, nodes, spec.discovery, "invalid_evidence")
		}
	}
	samples, valid := parseRuntimeBuildSamples(rows, spec)
	if !valid {
		return unavailableRuntimeBuildJob(job, nodes, spec.discovery, "invalid_evidence")
	}
	result := assessRuntimeBuildJob(job, nodes, spec.discovery, samples, selected, now)
	result.samples = &samples
	return result
}

func unavailableRuntimeBuildJob(job api.ObsRuntimeBuildJob, nodes []state.ComputeNode, discovery bool, reason string) runtimeBuildJobResult {
	job.State, job.Reason = "unknown", reason
	out := runtimeBuildJobResult{job: job}
	if discovery {
		for _, node := range nodes {
			out.items = append(out.items, api.ObsRuntimeBuildItem{Job: job.Job, Daemon: job.Daemon, Node: node.Name, State: "unavailable", Reason: reason})
		}
	} else {
		out.items = []api.ObsRuntimeBuildItem{{Job: job.Job, Daemon: job.Daemon, State: "unavailable", Reason: reason}}
	}
	return out
}

func parseRuntimeBuildSamples(rows [][]promql.VectorSample, spec runtimeBuildJobSpec) (runtimeBuildSamples, bool) {
	out := runtimeBuildSamples{up: map[string]promql.VectorSample{}, upTimes: map[string]float64{}, builds: map[string][]promql.VectorSample{}, buildTimes: map[string]float64{}}
	for i, set := range rows {
		seen := map[string]bool{}
		for _, row := range set {
			fullKey, ok := inputSeriesKey(row.Labels, false)
			key, targetOK := runtimeBuildTargetKey(row.Labels)
			if !ok || !targetOK || row.Labels["job"] != spec.job || seen[fullKey] || i >= 2 && row.Labels["daemon"] != spec.daemon {
				return out, false
			}
			seen[fullKey] = true
			switch i {
			case 0:
				if _, exists := out.up[key]; exists || row.Value != 0 && row.Value != 1 {
					return out, false
				}
				out.up[key] = row
			case 1:
				if _, exists := out.upTimes[key]; exists {
					return out, false
				}
				out.upTimes[key] = row.Value
			case 2:
				if row.Value != 1 {
					return out, false
				}
				out.builds[key] = append(out.builds[key], row)
			case 3:
				out.buildTimes[fullKey] = row.Value
			}
		}
	}
	if len(out.up) != len(out.upTimes) || len(rows[2]) != len(out.buildTimes) {
		return out, false
	}
	for key := range out.up {
		if _, ok := out.upTimes[key]; !ok {
			return out, false
		}
	}
	for key, builds := range out.builds {
		if _, exists := out.up[key]; !exists {
			return out, false
		}
		for _, row := range builds {
			fullKey, _ := inputSeriesKey(row.Labels, false)
			if _, exists := out.buildTimes[fullKey]; !exists {
				return out, false
			}
		}
	}
	return out, true
}

func assessRuntimeBuildJob(job api.ObsRuntimeBuildJob, nodes []state.ComputeNode, discovery bool, samples runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) runtimeBuildJobResult {
	out := runtimeBuildJobResult{job: job}
	expected := map[string]state.ComputeNode{}
	for _, node := range nodes {
		expected[node.ID] = node
	}
	seen := map[string]int{}
	keys := make([]string, 0, len(samples.up))
	for key := range samples.up {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Count membership before assessing, so duplicate targets never masquerade
	// as independent proof of a single expected node's revision.
	for _, row := range samples.up {
		if node, ok := expected[row.Labels["node_id"]]; ok && node.Name == row.Labels["node"] {
			seen[node.ID]++
		}
	}
	for _, key := range keys {
		row := samples.up[key]
		item := assessRuntimeBuildTarget(job, key, samples, selected, now)
		if discovery {
			node, ok := expected[row.Labels["node_id"]]
			item.Unexpected = !ok || node.Name != row.Labels["node"]
			if !item.Unexpected && seen[node.ID] > 1 {
				item.State, item.Reason, item.GitSHA, item.Version, item.ObservedAt = "unavailable", "conflicting_targets", "", "", nil
			}
		}
		out.items = append(out.items, item)
	}
	if discovery {
		for _, node := range nodes {
			if seen[node.ID] == 0 {
				out.items = append(out.items, api.ObsRuntimeBuildItem{Job: job.Job, Daemon: job.Daemon, Node: node.Name, State: "missing", Reason: "expected_target_missing"})
			}
		}
	} else if len(keys) == 0 {
		out.items = append(out.items, api.ObsRuntimeBuildItem{Job: job.Job, Daemon: job.Daemon, State: "missing", Reason: "expected_target_missing"})
	}
	return out
}

func assessRuntimeBuildTarget(job api.ObsRuntimeBuildJob, key string, samples runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) api.ObsRuntimeBuildItem {
	row := samples.up[key]
	item := api.ObsRuntimeBuildItem{Job: job.Job, Daemon: job.Daemon, TargetID: fmt.Sprintf("%x", sha256.Sum256([]byte(key))), State: "unavailable", Reason: "invalid_sample_time"}
	if ServingProbeNodeValid(row.Labels["node"]) {
		item.Node = row.Labels["node"]
	}
	at, valid := inputSampleTime(samples.upTimes[key], now)
	if !valid {
		return item
	}
	item.ScrapeObservedAt = &at
	if at.Before(now.Add(-api.ObsInputHealthMaxAge)) {
		item.State, item.Reason = "stale", "scrape_stale"
		return item
	}
	if at.Before(selected.after.Add(-api.ObsHealthClockSkewTolerance)) {
		item.Reason = "scrape_before_boundary"
		return item
	}
	if row.Value != 1 {
		item.Reason = "scrape_down"
		return item
	}
	builds := samples.builds[key]
	if len(builds) == 0 {
		item.State, item.Reason = "missing", "build_info_missing"
		return item
	}
	if len(builds) != 1 {
		item.Reason = "conflicting_build_info"
		return item
	}
	build := builds[0]
	fullKey, _ := inputSeriesKey(build.Labels, false)
	observed, valid := inputSampleTime(samples.buildTimes[fullKey], now)
	if !valid {
		return item
	}
	item.ObservedAt = &observed
	if observed.Before(now.Add(-api.ObsInputHealthMaxAge)) {
		item.State, item.Reason = "stale", "build_info_stale"
		return item
	}
	if observed.Before(selected.after.Add(-api.ObsHealthClockSkewTolerance)) {
		item.Reason = "build_info_before_boundary"
		return item
	}
	if !runtimeRevisionPattern.MatchString(build.Labels["git_sha"]) {
		item.Reason = "revision_unavailable"
		return item
	}
	item.GitSHA = build.Labels["git_sha"]
	if value := build.Labels["version"]; len(value) <= api.ObsRuntimeBuildVersionMaxBytes && runtimeVersionPattern.MatchString(value) {
		item.Version = value
	}
	item.State, item.Reason = "matching", ""
	if item.GitSHA != selected.release {
		item.State, item.Reason = "mismatched", "requested_revision_mismatch"
	}
	return item
}
