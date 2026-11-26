package operatorobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func addRuntimePreparationFixture(f inputFixture, now time.Time) {
	for _, up := range f[`up{job="imaged"}`] {
		for i, name := range runtimePreparationMetricNames() {
			phases := []string{""}
			if i < 3 {
				phases = runtimePreparationPhases()
			}
			for _, phase := range phases {
				value := float64(0)
				if name == "collection_success" || name == "observation_enabled" {
					value = 1
				}
				if name == "collection_last_success_timestamp_seconds" {
					value = float64(now.Unix())
				}
				for _, q := range []string{runtimePreparationQuery(), "timestamp(" + runtimePreparationQuery() + ")"} {
					v := value
					if strings.HasPrefix(q, "timestamp(") {
						v = float64(now.Unix())
					}
					row := inputRow("imaged", up.Metric["instance"], "", v)
					for k, v := range up.Metric {
						row.Metric[k] = v
					}
					row.Metric["obs_metric"] = runtimePreparationPrefix + name
					if phase != "" {
						row.Metric["phase"] = phase
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

func changeRuntimePreparationFixture(f inputFixture, name, phase, instance string, value float64) {
	for i, row := range f[runtimePreparationQuery()] {
		if row.Metric["obs_metric"] == runtimePreparationPrefix+name && row.Metric["phase"] == phase && (instance == "" || row.Metric["instance"] == instance) {
			f[runtimePreparationQuery()][i].Value[1] = strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
}

func TestRuntimePreparationPhasesAndFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, state string
		incomplete  bool
		change      func(inputFixture, *RuntimeBuildSelection)
	}{
		{"empty", "empty", false, func(inputFixture, *RuntimeBuildSelection) {}},
		{"known_age", "waiting", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "pending", "preparing", "", 5)
			changeRuntimePreparationFixture(f, "oldest_phase_started_timestamp_seconds", "preparing", "", float64(now.Add(-time.Hour).Unix()))
		}},
		{"unknown_ages", "waiting", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "pending", "scan_complete", "", 2)
			changeRuntimePreparationFixture(f, "phase_age_unknown", "scan_complete", "", 2)
		}},
		{"mixed_ages", "waiting", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "pending", "layer_published", "", 3)
			changeRuntimePreparationFixture(f, "phase_age_unknown", "layer_published", "", 1)
			changeRuntimePreparationFixture(f, "oldest_phase_started_timestamp_seconds", "layer_published", "", float64(now.Add(-time.Hour).Unix()))
		}},
		{"disabled_pending", "waiting", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "observation_enabled", "", "", 0)
			changeRuntimePreparationFixture(f, "pending", "preparing", "", 1)
			changeRuntimePreparationFixture(f, "phase_age_unknown", "preparing", "", 1)
		}},
		{"failed_collection", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "collection_success", "", "", 0)
		}},
		{"never_observed", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "collection_success", "", "", 0)
			changeRuntimePreparationFixture(f, "collection_last_success_timestamp_seconds", "", "", 0)
		}},
		{"expired_collection", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "collection_last_success_timestamp_seconds", "", "", float64(now.Add(-3*time.Minute).Unix()))
		}},
		{"pre_boundary", "incomplete", true, func(f inputFixture, s *RuntimeBuildSelection) {
			s.after = now
			changeRuntimePreparationFixture(f, "collection_last_success_timestamp_seconds", "", "", float64(now.Add(-40*time.Second).Unix()))
		}},
		{"invalid_count", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "pending", "preparing", "", -1)
		}},
		{"invalid_age", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "phase_age_unknown", "preparing", "", 1)
		}},
		{"known_without_clock", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "pending", "preparing", "", 1)
		}},
		{"large_exact", "waiting", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "pending", "preparing", "", float64(api.ObsRuntimePreparationPendingMax))
			changeRuntimePreparationFixture(f, "phase_age_unknown", "preparing", "", float64(api.ObsRuntimePreparationPendingMax))
		}},
		{"partial_positive", "waiting", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePreparationFixture(f, "pending", "preparing", "", 1)
			changeRuntimePreparationFixture(f, "phase_age_unknown", "preparing", "", 1)
			changeRuntimePreparationFixture(f, "pending", "scan_complete", "", -1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, selected.role, now)
			tc.change(f, &selected)
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			h := out.PreparationHealth
			if !RuntimePreparationHealthValid(out.ObsRuntimeBuilds, h) || h.State != tc.state || h.Incomplete != tc.incomplete {
				t.Fatalf("invalid preparation: %+v", h)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-host") || out.State != "matching" {
				t.Fatal("preparation leaked labels or changed identity")
			}
			if tc.name == "unknown_ages" && (h.Counts.UnknownAgeScopes != 1 || h.Items[0].Phases[2].Values.OldestKnownAgeSeconds != nil) {
				t.Fatal("unknown age became a known zero")
			}
			if tc.name == "disabled_pending" && (h.Counts.DisabledScopes != 1 || h.Counts.WaitingScopes != 1) {
				t.Fatal("disabled observer implied drained work")
			}
		})
	}
}

func TestRuntimePreparationFramesProfilesAndPreview(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, registry := runtimeFixture(role, now, 45)
		addLoopFixture(f, role, now)
		changeRuntimePreparationFixture(f, "pending", "scan_complete", "private-host-44:9100", 1)
		changeRuntimePreparationFixture(f, "phase_age_unknown", "scan_complete", "private-host-44:9100", 1)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.PreparationHealth != nil {
				t.Fatal("v1 gained preparation evidence")
			}
			continue
		}
		if !RuntimePreparationHealthValid(out.ObsRuntimeBuilds, out.PreparationHealth) {
			t.Fatalf("invalid bounded preview: %+v", out.PreparationHealth)
		}
		if role == "control-plane-v2" && (out.PreparationHealth.Counts.WaitingScopes != 1 || out.PreparationHealth.Phases[2].UnknownAgeScopes != 1 || out.PreparationHealth.OmittedItems < 13) {
			t.Fatal("full-set phase evidence lost")
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
			t.Fatal("API budget exceeded")
		}
	}
	for _, fault := range []string{"mixed_clocks", "duplicate", "orphan", "missing_phase", "unknown_phase", "stale_sample", "unknown_inventory", "no_targets", "scrape_down"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			q, tq := runtimePreparationQuery(), "timestamp("+runtimePreparationQuery()+")"
			switch fault {
			case "unknown_inventory":
				r.err = errors.New("private-error")
			case "no_targets":
				f, s, r = runtimeFixture("control-plane-v2", now, 0)
				addLoopFixture(f, s.role, now)
			case "scrape_down":
				f[`up{job="imaged"}`][0].Value[1] = "0"
			default:
				for _, query := range []string{q, tq} {
					rows := []inputFixtureRow{}
					for _, row := range f[query] {
						if row.Metric["phase"] == "preparing" {
							if fault == "missing_phase" {
								continue
							}
							if fault == "unknown_phase" {
								row.Metric["phase"] = "private-stage"
							}
							if fault == "orphan" && query == tq {
								row.Metric["private_label"] = "private-error"
							}
							if fault == "duplicate" {
								rows = append(rows, row)
							}
							if query == tq && strings.HasSuffix(row.Metric["obs_metric"], "_pending") {
								if fault == "mixed_clocks" {
									row.Value[1] = fmt.Sprint(now.Add(-time.Second).Unix())
								}
								if fault == "stale_sample" {
									row.Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
								}
							}
						}
						rows = append(rows, row)
					}
					f[query] = rows
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			if !RuntimePreparationHealthValid(out.ObsRuntimeBuilds, out.PreparationHealth) || out.PreparationHealth.State == "empty" || fault != "no_targets" && !out.PreparationHealth.Incomplete {
				t.Fatalf("fault became known empty: %+v", out.PreparationHealth)
			}
		})
	}
}
