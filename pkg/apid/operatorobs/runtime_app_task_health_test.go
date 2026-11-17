package operatorobs

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"strconv"
	"strings"
	"testing"
	"time"
)

func addRuntimeAppTaskFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for i, metric := range runtimeAppTaskMetricNames() {
				classes := []string{"/"}
				if i < 3 {
					classes = runtimeAppTaskClasses()
				}
				for _, cls := range classes {
					kind, class, _ := strings.Cut(cls, "/")
					value := float64(0)
					if metric == "collection_success" || metric == "observation_enabled" || metric == "dispatch_enabled" || metric == "runner_running" {
						value = 1
					}
					if metric == "slots_configured" {
						value = 4
					}
					if metric == "collection_last_success_timestamp_seconds" || metric == "claim_last_success_timestamp_seconds" || metric == "sweep_last_success_timestamp_seconds" {
						value = float64(now.Unix())
					}
					for _, query := range []string{runtimeAppTaskQuery(job), "timestamp(" + runtimeAppTaskQuery(job) + ")"} {
						v := value
						if strings.HasPrefix(query, "timestamp(") {
							v = float64(now.Unix())
						}
						row := inputRow(job, up.Metric["instance"], "", v)
						for k, v := range up.Metric {
							row.Metric[k] = v
						}
						row.Metric["obs_metric"] = runtimeAppTaskPrefix + metric
						if i < 3 {
							row.Metric["kind"], row.Metric["class"] = kind, class
						}
						if !strings.HasPrefix(query, "timestamp(") {
							row.Metric["__name__"] = row.Metric["obs_metric"]
						}
						f[query] = append(f[query], row)
					}
				}
			}
		}
	}
}
func changeRuntimeAppTaskFixture(f inputFixture, name, cohort string, value float64) {
	kind, class, _ := strings.Cut(cohort, "/")
	for _, job := range []string{"schedd", "schedd-compute"} {
		q := runtimeAppTaskQuery(job)
		for i, row := range f[q] {
			if row.Metric["obs_metric"] == runtimeAppTaskPrefix+name && row.Metric["kind"] == kind && row.Metric["class"] == class {
				f[q][i].Value[1] = strconv.FormatFloat(value, 'f', -1, 64)
			}
		}
	}
}
func TestRuntimeAppTaskKindsClassesAndLocalDispatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, key := range runtimeAppTaskClasses() {
		t.Run(key, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			_, cls, _ := strings.Cut(key, "/")
			changeRuntimeAppTaskFixture(f, "pending", key, 3)
			if runtimeAppTaskUnknownAgeClass(cls) {
				changeRuntimeAppTaskFixture(f, "age_unknown", key, 3)
			} else {
				changeRuntimeAppTaskFixture(f, "oldest_boundary_timestamp_seconds", key, float64(now.Add(-time.Hour).Unix()))
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			h := out.AppTaskHealth
			if !RuntimeAppTaskHealthValid(out.ObsRuntimeBuilds, h) || h.State != "nonempty" || h.Incomplete || h.Counts.NonemptyViews != 2 || h.Counts.RunnerRunningViews != 2 {
				t.Fatalf("invalid task evidence: %+v", h)
			}
			if out.State != "matching" {
				t.Fatal("advisory changed identity")
			}
		})
	}
	for _, fault := range []string{"empty", "disabled", "stopped", "saturated", "failed", "never", "stale", "before_boundary", "partial", "missing_dispatch", "invalid_slots", "fractional", "unknown_known_age", "max_exact"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, s.role, now)
			switch fault {
			case "disabled":
				changeRuntimeAppTaskFixture(f, "dispatch_enabled", "/", 0)
				changeRuntimeAppTaskFixture(f, "runner_running", "/", 0)
			case "stopped":
				changeRuntimeAppTaskFixture(f, "runner_running", "/", 0)
			case "saturated":
				changeRuntimeAppTaskFixture(f, "slots_in_use", "/", 4)
			case "failed":
				changeRuntimeAppTaskFixture(f, "collection_success", "/", 0)
			case "never":
				changeRuntimeAppTaskFixture(f, "collection_success", "/", 0)
				changeRuntimeAppTaskFixture(f, "collection_last_success_timestamp_seconds", "/", 0)
			case "stale":
				changeRuntimeAppTaskFixture(f, "collection_last_success_timestamp_seconds", "/", float64(now.Add(-3*time.Minute).Unix()))
			case "before_boundary":
				s.after = now
				changeRuntimeAppTaskFixture(f, "collection_last_success_timestamp_seconds", "/", float64(now.Add(-40*time.Second).Unix()))
			case "invalid_slots":
				changeRuntimeAppTaskFixture(f, "slots_in_use", "/", 5)
			case "fractional":
				changeRuntimeAppTaskFixture(f, "pending", "release/queued", 1.5)
			case "unknown_known_age":
				changeRuntimeAppTaskFixture(f, "pending", "release/restoring", 1)
				changeRuntimeAppTaskFixture(f, "oldest_boundary_timestamp_seconds", "release/restoring", float64(now.Add(-time.Hour).Unix()))
			case "max_exact":
				changeRuntimeAppTaskFixture(f, "pending", "release/held", float64(api.ObsRuntimeAppTaskPendingMax))
				changeRuntimeAppTaskFixture(f, "age_unknown", "release/held", float64(api.ObsRuntimeAppTaskPendingMax))
			case "partial", "missing_dispatch":
				changeRuntimeAppTaskFixture(f, "pending", "release/held", 1)
				changeRuntimeAppTaskFixture(f, "age_unknown", "release/held", 1)
				for _, q := range []string{runtimeAppTaskQuery("schedd"), "timestamp(" + runtimeAppTaskQuery("schedd") + ")"} {
					rows := []inputFixtureRow{}
					for _, row := range f[q] {
						drop := fault == "partial" && row.Metric["kind"] == "manual" && row.Metric["class"] == "running" && row.Metric["obs_metric"] == runtimeAppTaskPrefix+"pending" || fault == "missing_dispatch" && row.Metric["obs_metric"] == runtimeAppTaskPrefix+"dispatch_enabled"
						if !drop {
							rows = append(rows, row)
						}
					}
					f[q] = rows
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			h := out.AppTaskHealth
			if !RuntimeAppTaskHealthValid(out.ObsRuntimeBuilds, h) {
				t.Fatalf("invalid %s: %+v", fault, h)
			}
			positive := fault == "partial" || fault == "missing_dispatch" || fault == "max_exact"
			incomplete := fault == "failed" || fault == "never" || fault == "stale" || fault == "before_boundary" || fault == "partial" || fault == "missing_dispatch" || fault == "invalid_slots" || fault == "fractional" || fault == "unknown_known_age"
			if (h.State == "nonempty") != positive || h.Incomplete != incomplete {
				t.Fatalf("lost evidence %s: %+v", fault, h)
			}
			if (fault == "failed" || fault == "stale") && h.Counts.RunnerRunningViews != 1 {
				t.Fatal("collection poisoned dispatch")
			}
			if fault == "disabled" && (h.Counts.DispatchDisabledViews != 1 || h.Items[0].Dispatch.State != "disabled") {
				t.Fatal("disabled confused with stopped")
			}
			if fault == "stopped" && h.Counts.RunnerStoppedViews != 1 {
				t.Fatal("stopped runner lost")
			}
			if fault == "saturated" && h.Counts.SaturatedViews != 1 {
				t.Fatal("local slot pressure lost")
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "private-host") {
				t.Fatal("raw label escaped")
			}
		})
	}
}

func TestRuntimeAppTaskFramesProfilesAndPreview(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, r := runtimeFixture(role, now, 1)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.AppTaskHealth != nil {
				t.Fatal("v1 gained task evidence")
			}
		} else if !RuntimeAppTaskHealthValid(out.ObsRuntimeBuilds, out.AppTaskHealth) {
			t.Fatal("invalid selected evidence")
		}
	}
	for _, fault := range []string{"mixed_clocks", "duplicate", "extra_dimension", "missing_kind", "missing_metric", "down", "registry_unavailable"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			if fault == "down" {
				f[`up{job="schedd"}`][0].Value[1] = "0"
			} else if fault == "registry_unavailable" {
				r.err = fmt.Errorf("private-provider-error")
			} else {
				for _, q := range []string{runtimeAppTaskQuery("schedd"), "timestamp(" + runtimeAppTaskQuery("schedd") + ")"} {
					rows := []inputFixtureRow{}
					for _, row := range f[q] {
						if row.Metric["obs_metric"] == runtimeAppTaskPrefix+"pending" && row.Metric["kind"] == "release" && row.Metric["class"] == "queued" {
							if fault == "missing_metric" {
								continue
							}
							if fault == "duplicate" {
								rows = append(rows, row)
							}
							if fault == "extra_dimension" {
								row.Metric["secret"] = "private-provider-error"
							}
							if fault == "missing_kind" {
								delete(row.Metric, "kind")
							}
							if fault == "mixed_clocks" && strings.HasPrefix(q, "timestamp(") {
								row.Value[1] = strconv.FormatInt(now.Add(-time.Second).Unix(), 10)
							}
						}
						rows = append(rows, row)
					}
					f[q] = rows
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			if !RuntimeAppTaskHealthValid(out.ObsRuntimeBuilds, out.AppTaskHealth) || !out.AppTaskHealth.Incomplete || out.AppTaskHealth.State == "empty" {
				t.Fatalf("source gap fabricated empty: %+v", out.AppTaskHealth)
			}
		})
	}
	f, s, r := runtimeFixture("control-plane-v2", now, 1)
	addLoopFixture(f, s.role, now)
	for q, rows := range f {
		for i := 1; i < 40; i++ {
			for _, row := range rows {
				if row.Metric["job"] != "schedd" {
					continue
				}
				clone := inputRow("schedd", fmt.Sprintf("private-replica-%d:9100", i), "", 0)
				for k, v := range row.Metric {
					clone.Metric[k] = v
				}
				clone.Metric["instance"] = fmt.Sprintf("private-replica-%d:9100", i)
				clone.Value = row.Value
				f[q] = append(f[q], clone)
			}
		}
	}
	changeRuntimeAppTaskFixture(f, "pending", "release/held", 1)
	changeRuntimeAppTaskFixture(f, "age_unknown", "release/held", 1)
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.AppTaskHealth
	if !RuntimeAppTaskHealthValid(out.ObsRuntimeBuilds, h) || h.Counts.NonemptyViews != 41 || h.OmittedItems < 9 {
		t.Fatalf("lost full replica accounting: %+v", h)
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	if len(data) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("API budget exceeded")
	}
	if len(h.Items) > 0 && h.Items[0].Classes[2].Values.UnknownAge != 1 {
		t.Fatal("replicas were summed")
	}
}
