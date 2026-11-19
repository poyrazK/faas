package operatorobs

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func addRuntimeCommandCronFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for i, name := range runtimeCommandCronMetricNames() {
				classes := []string{""}
				if i < 3 {
					classes = runtimeCommandCronClasses()
				}
				for _, class := range classes {
					value := float64(0)
					if name == "sweep_last_start_timestamp_seconds" {
						value = float64(now.Add(-20 * time.Second).Unix())
					}
					if name == "sweep_last_success_timestamp_seconds" {
						value = float64(now.Add(-10 * time.Second).Unix())
					}
					if name == "collection_success" || name == "observation_enabled" {
						value = 1
					}
					if name == "collection_last_success_timestamp_seconds" {
						value = float64(now.Unix())
					}
					for _, q := range []string{runtimeCommandCronQuery(job), "timestamp(" + runtimeCommandCronQuery(job) + ")"} {
						v := value
						if strings.HasPrefix(q, "timestamp(") {
							v = float64(now.Unix())
						}
						row := inputRow(job, up.Metric["instance"], "", v)
						for k, v := range up.Metric {
							row.Metric[k] = v
						}
						row.Metric["obs_metric"] = runtimeCommandCronPrefix + name
						if class != "" {
							row.Metric["class"] = class
						}
						if !strings.HasPrefix(q, "timestamp(") {
							row.Metric["__name__"] = row.Metric["obs_metric"]
						}
						f[q] = append(f[q], row)
					}
				}
			}
		}
	}
}

func changeRuntimeCommandCronFixture(f inputFixture, name, class, instance string, value float64) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for i, row := range f[runtimeCommandCronQuery(job)] {
			if row.Metric["obs_metric"] == runtimeCommandCronPrefix+name && row.Metric["class"] == class && (instance == "" || row.Metric["instance"] == instance) {
				f[runtimeCommandCronQuery(job)][i].Value[1] = strconv.FormatFloat(value, 'f', -1, 64)
			}
		}
	}
}

func TestRuntimeCommandCronClassesScopesAndSweeps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, name := range runtimeCommandCronClasses() {
		t.Run(name, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			changeRuntimeCommandCronFixture(f, "schedules", name, "", 3)
			if runtimeCommandCronUnknownAgeClass(name) {
				changeRuntimeCommandCronFixture(f, "age_unknown", name, "", 3)
			} else {
				changeRuntimeCommandCronFixture(f, "oldest_due_timestamp_seconds", name, "", float64(now.Add(-time.Hour).Unix()))
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			h := out.CommandCronHealth
			if !RuntimeCommandCronHealthValid(out.ObsRuntimeBuilds, h) || h.State != "nonempty" || h.Incomplete || h.Counts.NonemptyScopes != 2 || h.Counts.SweepIdleScopes != 2 {
				t.Fatalf("invalid cron evidence: %+v", h)
			}
			if (h.Counts.DueScopes == 2) != runtimeCommandCronDueClass(name) || (h.Counts.InactiveScopes == 2) != runtimeCommandCronInactiveClass(name) {
				t.Fatal("due and inactive intent confused")
			}
			if out.State != "matching" {
				t.Fatal("cron changed identity")
			}
		})
	}
	for _, fault := range []string{"empty", "mixed", "partial", "disabled_observer", "failed", "never", "stale", "before_boundary", "running", "not_started", "never_succeeded", "missing_sweep", "fractional", "invalid_age", "unknown_known_age", "max_exact", "running_without_start", "success_without_start", "running_future_success"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, s.role, now)
			switch fault {
			case "mixed", "partial":
				changeRuntimeCommandCronFixture(f, "schedules", "due", "", 3)
				changeRuntimeCommandCronFixture(f, "age_unknown", "due", "", 1)
				changeRuntimeCommandCronFixture(f, "oldest_due_timestamp_seconds", "due", "", float64(now.Add(-time.Hour).Unix()))
				changeRuntimeCommandCronFixture(f, "schedules", "disabled", "", 2)
				changeRuntimeCommandCronFixture(f, "age_unknown", "disabled", "", 2)
			case "disabled_observer":
				changeRuntimeCommandCronFixture(f, "observation_enabled", "", "", 0)
			case "failed":
				changeRuntimeCommandCronFixture(f, "collection_success", "", "", 0)
			case "never":
				changeRuntimeCommandCronFixture(f, "collection_success", "", "", 0)
				changeRuntimeCommandCronFixture(f, "collection_last_success_timestamp_seconds", "", "", 0)
			case "stale":
				changeRuntimeCommandCronFixture(f, "collection_last_success_timestamp_seconds", "", "", float64(now.Add(-3*time.Minute).Unix()))
			case "before_boundary":
				s.after = now
				changeRuntimeCommandCronFixture(f, "collection_last_success_timestamp_seconds", "", "", float64(now.Add(-40*time.Second).Unix()))
			case "running":
				changeRuntimeCommandCronFixture(f, "sweep_running", "", "", 1)
				changeRuntimeCommandCronFixture(f, "sweep_last_start_timestamp_seconds", "", "", float64(now.Unix()))
			case "not_started":
				changeRuntimeCommandCronFixture(f, "sweep_last_start_timestamp_seconds", "", "", 0)
				changeRuntimeCommandCronFixture(f, "sweep_last_success_timestamp_seconds", "", "", 0)
			case "never_succeeded":
				changeRuntimeCommandCronFixture(f, "sweep_last_success_timestamp_seconds", "", "", 0)
			case "fractional":
				changeRuntimeCommandCronFixture(f, "schedules", "due", "", 1.5)
			case "invalid_age":
				changeRuntimeCommandCronFixture(f, "age_unknown", "due", "", 1)
			case "unknown_known_age":
				changeRuntimeCommandCronFixture(f, "schedules", "disabled", "", 1)
				changeRuntimeCommandCronFixture(f, "oldest_due_timestamp_seconds", "disabled", "", float64(now.Add(-time.Hour).Unix()))
			case "max_exact":
				changeRuntimeCommandCronFixture(f, "schedules", "unknown", "", float64(api.ObsRuntimeCommandCronSchedulesMax))
				changeRuntimeCommandCronFixture(f, "age_unknown", "unknown", "", float64(api.ObsRuntimeCommandCronSchedulesMax))
			case "running_without_start":
				changeRuntimeCommandCronFixture(f, "sweep_running", "", "", 1)
				changeRuntimeCommandCronFixture(f, "sweep_last_start_timestamp_seconds", "", "", 0)
			case "success_without_start":
				changeRuntimeCommandCronFixture(f, "sweep_last_start_timestamp_seconds", "", "", 0)
			case "running_future_success":
				changeRuntimeCommandCronFixture(f, "sweep_running", "", "", 1)
			}
			if fault == "partial" || fault == "missing_sweep" {
				for _, q := range []string{runtimeCommandCronQuery("schedd"), "timestamp(" + runtimeCommandCronQuery("schedd") + ")"} {
					rows := []inputFixtureRow{}
					for _, row := range f[q] {
						drop := fault == "partial" && row.Metric["class"] == "not_due" && row.Metric["obs_metric"] == runtimeCommandCronPrefix+"schedules" || fault == "missing_sweep" && row.Metric["obs_metric"] == runtimeCommandCronPrefix+"sweep_running"
						if !drop {
							rows = append(rows, row)
						}
					}
					f[q] = rows
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			h := out.CommandCronHealth
			if !RuntimeCommandCronHealthValid(out.ObsRuntimeBuilds, h) {
				t.Fatalf("invalid %s: %+v", fault, h)
			}
			positive := fault == "mixed" || fault == "partial" || fault == "max_exact"
			incomplete := fault == "failed" || fault == "never" || fault == "stale" || fault == "before_boundary" || fault == "partial" || fault == "missing_sweep" || fault == "fractional" || fault == "invalid_age" || fault == "unknown_known_age" || fault == "running_without_start" || fault == "success_without_start" || fault == "running_future_success"
			if (h.State == "nonempty") != positive || h.Incomplete != incomplete {
				t.Fatalf("lost evidence %s: %+v", fault, h)
			}
			if (fault == "failed" || fault == "stale") && h.Counts.SweepIdleScopes != 1 {
				t.Fatal("collection poisoned sweep activity")
			}
			if fault == "not_started" && (h.Counts.SweepNeverStartedScopes != 1 || h.Items[0].Sweep.Values.LastStartedAt != nil) {
				t.Fatal("fabricated sweep")
			}
			if fault == "running" && h.Counts.SweepRunningScopes != 1 {
				t.Fatal("active sweep lost")
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "private-host") {
				t.Fatal("raw labels leaked")
			}
		})
	}
}

func TestRuntimeCommandCronFramesProfilesAndPreview(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, r := runtimeFixture(role, now, 1)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.CommandCronHealth != nil {
				t.Fatal("v1 gained cron evidence")
			}
		} else if !RuntimeCommandCronHealthValid(out.ObsRuntimeBuilds, out.CommandCronHealth) {
			t.Fatal("invalid selected evidence")
		}
	}
	for _, fault := range []string{"mixed_clocks", "duplicate", "extra_label", "wrong_class", "missing_metric", "down", "registry_unavailable"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			if fault == "down" {
				f[`up{job="schedd"}`][0].Value[1] = "0"
			} else if fault == "registry_unavailable" {
				r.err = fmt.Errorf("private-provider-error")
			} else {
				for _, q := range []string{runtimeCommandCronQuery("schedd"), "timestamp(" + runtimeCommandCronQuery("schedd") + ")"} {
					rows := []inputFixtureRow{}
					for _, row := range f[q] {
						if row.Metric["obs_metric"] == runtimeCommandCronPrefix+"schedules" && row.Metric["class"] == "due" {
							if fault == "missing_metric" {
								continue
							}
							if fault == "duplicate" {
								rows = append(rows, row)
							}
							if fault == "extra_label" {
								row.Metric["secret"] = "private-provider-error"
							}
							if fault == "wrong_class" {
								row.Metric["class"] = "arbitrary"
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
			if !RuntimeCommandCronHealthValid(out.ObsRuntimeBuilds, out.CommandCronHealth) || !out.CommandCronHealth.Incomplete || out.CommandCronHealth.State == "empty" {
				t.Fatalf("gap fabricated empty: %+v", out.CommandCronHealth)
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
	changeRuntimeCommandCronFixture(f, "schedules", "disabled", "", 1)
	changeRuntimeCommandCronFixture(f, "age_unknown", "disabled", "", 1)
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.CommandCronHealth
	if !RuntimeCommandCronHealthValid(out.ObsRuntimeBuilds, h) || h.Counts.NonemptyScopes != 41 || h.Counts.InactiveScopes != 41 || h.Counts.DueScopes != 0 || h.OmittedItems < 9 {
		t.Fatalf("lost full scope accounting: %+v", h)
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	if len(data) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("API budget exceeded")
	}
	if len(h.Items) > 0 && h.Items[0].Classes[5].Values.Schedules != 1 {
		t.Fatal("overlapping schedules were summed")
	}
}
