package operatorobs

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func addRuntimeCommandCronTimingFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for _, d := range runtimeCommandCronTimingDimensions() {
				metric, le := runtimeCommandCronTimingMetric(d)
				for _, q := range []string{runtimeCommandCronTimingQuery(job), "timestamp(" + runtimeCommandCronTimingQuery(job) + ")"} {
					n := float64(0)
					if strings.HasPrefix(q, "timestamp(") {
						n = float64(now.Unix())
					}
					row := inputRow(job, up.Metric["instance"], "", n)
					for k, v := range up.Metric {
						row.Metric[k] = v
					}
					row.Metric["obs_metric"] = metric
					if le != "" {
						row.Metric["le"] = le
					}
					if !strings.HasPrefix(q, "timestamp(") {
						row.Metric["__name__"] = metric
					}
					f[q] = append(f[q], row)
				}
			}
		}
	}
}
func changeRuntimeCronTiming(f inputFixture, signal string, value string) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		q := runtimeCommandCronTimingQuery(job)
		metric, le := runtimeCommandCronTimingMetric(RuntimeCommandCronTimingDimension{Signal: signal})
		for i, row := range f[q] {
			if row.Metric["obs_metric"] == metric && row.Metric["le"] == le {
				f[q][i].Value[1] = value
			}
		}
	}
}
func TestRuntimeCommandCronTimingEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	for _, fault := range []string{"baseline", "quiet", "timed", "unknown", "partial", "prior_partial", "unknown_missing", "hist_stale", "fractional", "nonmonotonic", "count_mismatch", "mixed_clocks", "impossible_delta", "reset", "process_changed", "uncertain_window", "repeated", "max_exact"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			prior, _, _ := runtimeFixture(s.role, at, 1)
			addLoopFixture(prior, s.role, at)
			s.compareAt = at
			for _, spec := range runtimeBuildJobSpecs(s.role) {
				if spec.daemon == "schedd" {
					for i := range f[runtimeProcessQuery(spec)] {
						f[runtimeProcessQuery(spec)][i].Value[1] = "160"
					}
				}
			}
			for _, d := range runtimeCommandCronTimingDimensions() {
				if d.Signal != "unknown_timestamps" {
					changeRuntimeCronTiming(f, d.Signal, "3")
				}
			}
			changeRuntimeCronTiming(f, "unknown_timestamps", "2")
			q := runtimeCommandCronTimingQuery("schedd")
			switch fault {
			case "baseline":
				s.compareAt = time.Time{}
			case "quiet":
				for _, d := range runtimeCommandCronTimingDimensions() {
					changeRuntimeCronTiming(f, d.Signal, "0")
				}
			case "unknown":
				for _, d := range runtimeCommandCronTimingDimensions() {
					if d.Signal != "unknown_timestamps" {
						changeRuntimeCronTiming(f, d.Signal, "0")
					}
				}
			case "partial":
				f[q] = f[q][1:]
				f["timestamp("+q+")"] = f["timestamp("+q+")"][1:]
			case "prior_partial":
				prior[q] = prior[q][1:]
				prior["timestamp("+q+")"] = prior["timestamp("+q+")"][1:]
			case "unknown_missing":
				for _, job := range []string{"schedd", "schedd-compute"} {
					for _, query := range []string{runtimeCommandCronTimingQuery(job), "timestamp(" + runtimeCommandCronTimingQuery(job) + ")"} {
						f[query] = f[query][:len(f[query])-1]
					}
				}
			case "hist_stale":
				for i := 0; i < 12; i++ {
					f["timestamp("+q+")"][i].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
				}
			case "fractional":
				changeRuntimeCronTiming(f, "le_1_seconds", "0.5")
			case "nonmonotonic":
				changeRuntimeCronTiming(f, "le_1_seconds", "4")
			case "count_mismatch":
				changeRuntimeCronTiming(f, "timed", "4")
			case "mixed_clocks":
				f["timestamp("+q+")"][0].Value[1] = fmt.Sprint(now.Add(-time.Second).Unix())
			case "impossible_delta":
				changeRuntimeCronTiming(prior, "le_5_seconds", "2")
				for _, d := range runtimeCommandCronTimingDimensions()[2:12] {
					changeRuntimeCronTiming(prior, d.Signal, "2")
				}
			case "reset":
				changeRuntimeCronTiming(prior, "le_1_seconds", "4")
				for _, d := range runtimeCommandCronTimingDimensions()[1:12] {
					changeRuntimeCronTiming(prior, d.Signal, "4")
				}
			case "process_changed", "uncertain_window", "repeated":
				for _, spec := range runtimeBuildJobSpecs(s.role) {
					if spec.daemon != "schedd" {
						continue
					}
					p := runtimeProcessQuery(spec)
					for i := range f[p] {
						f[p][i].Value[1] = "130"
						if fault == "process_changed" {
							f[p][i].Value[1] = "5"
						}
					}
					if fault == "repeated" {
						for i := range f["timestamp("+p+")"] {
							f["timestamp("+p+")"][i].Value[1] = fmt.Sprint(at.Unix())
						}
					}
				}
			case "max_exact":
				for _, d := range runtimeCommandCronTimingDimensions() {
					changeRuntimeCronTiming(f, d.Signal, "9007199254740991")
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), runtimeProcessClient(t, f, prior, now, at, ""), r, s, now)
			h := out.CommandCronTiming
			if !RuntimeCommandCronTimingValid(out.ObsRuntimeBuilds, h) {
				t.Fatalf("invalid timing: %+v", h)
			}
			if out.State != "matching" {
				t.Fatal("advisory evidence changed core gate")
			}
			switch fault {
			case "baseline":
				if h.Counts.Baseline != 26 || h.Counts.Increased != 0 {
					t.Fatal("baseline counted as activity")
				}
			case "quiet":
				if h.Counts.Unchanged != 26 {
					t.Fatal("quiet timing fabricated activity")
				}
			case "timed", "max_exact":
				if h.Counts.Increased != 26 || h.Incomplete {
					t.Fatal("lost timing observations")
				}
			case "unknown":
				if h.Signals[12].Increased != 2 || h.Signals[11].Unchanged != 2 {
					t.Fatal("unknown timestamps fabricated zero-lag observation")
				}
			case "unknown_missing":
				if h.Signals[11].Increased != 2 || h.Signals[12].Unknown != 2 || !h.Incomplete {
					t.Fatal("unknown-timestamp gap lost valid histogram")
				}
			case "process_changed", "uncertain_window", "repeated":
				if h.Counts.Increased != 0 || !h.Incomplete {
					t.Fatal("unsafe process comparison")
				}
			default:
				if !h.Incomplete || h.Signals[12].Increased != 2 {
					t.Fatal("histogram fault lost independent unknown timestamps")
				}
				for _, signal := range h.Signals[:12] {
					if signal.Unknown == 0 {
						t.Fatal("incomplete histogram established a timing comparison")
					}
				}
			}
			body, _ := json.Marshal(h)
			var decoded RuntimeCommandCronTiming
			if json.Unmarshal(body, &decoded) != nil || !RuntimeCommandCronTimingValid(out.ObsRuntimeBuilds, &decoded) || CloneRuntimeCommandCronTiming(h) == nil {
				t.Fatal("timing wire lost evidence")
			}
		})
	}
}
func TestRuntimeCommandCronTimingProfilesAndBounds(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, r := runtimeFixture(role, now, 1)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.CommandCronTiming != nil {
				t.Fatal("legacy profile gained timing")
			}
		} else if !RuntimeCommandCronTimingValid(out.ObsRuntimeBuilds, out.CommandCronTiming) {
			t.Fatal("invalid timing profile")
		}
	}
	f, s, r := runtimeFixture("control-plane-v2", now, 40)
	addLoopFixture(f, s.role, now)
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	if !RuntimeCommandCronTimingValid(out.ObsRuntimeBuilds, out.CommandCronTiming) || out.CommandCronTiming.Counts.Baseline != 41*13 || out.CommandCronTiming.OmittedItems < 9 {
		t.Fatal("lost full-set coverage")
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("response exceeds limit")
	}
}

func TestRuntimeCommandCronTimingEveryBucket(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	for bucket := 0; bucket < 11; bucket++ {
		t.Run(runtimeCommandCronTimingDimensions()[bucket].Signal, func(t *testing.T) {
			f, s, r := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, s.role, now)
			prior, _, _ := runtimeFixture(s.role, at, 1)
			addLoopFixture(prior, s.role, at)
			s.compareAt = at
			for _, spec := range runtimeBuildJobSpecs(s.role) {
				if spec.daemon == "schedd" {
					for i := range f[runtimeProcessQuery(spec)] {
						f[runtimeProcessQuery(spec)][i].Value[1] = "160"
					}
				}
			}
			for _, d := range runtimeCommandCronTimingDimensions()[bucket:12] {
				changeRuntimeCronTiming(f, d.Signal, "3")
			}
			client := runtimeProcessClient(t, f, prior, now, at, "")
			base, results := collectRuntimeBuilds(t.Context(), client, r, s, now)
			rows := make([][]RuntimeCommandCronTimingItem, len(results))
			for i, result := range results {
				if result.job.Daemon == "schedd" {
					rows[i] = readRuntimeCommandCronTimingJob(t.Context(), client, result, s, now)
				}
			}
			h := aggregateRuntimeCommandCronTiming(base, rows)
			if !RuntimeCommandCronTimingValid(base, h) || h.Incomplete || h.Counts.Increased != 12-bucket || len(h.Items) != 1 {
				t.Fatal("lost cumulative bucket distribution")
			}
			for i, signal := range h.Items[0].Signals {
				want := int64(0)
				if i >= bucket && i < 12 {
					want = 3
				}
				if signal.Delta == nil || *signal.Delta != want {
					t.Fatalf("wrong cumulative interval for %s", signal.Signal)
				}
			}
		})
	}
}
