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

func addRuntimeSnapshotPrimeFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for i, name := range runtimeSnapshotPrimeMetricNames() {
				classes := []string{""}
				if i < 3 {
					classes = runtimeSnapshotPrimeClasses()
				}
				for _, class := range classes {
					value := float64(0)
					if name == "collection_success" || name == "observation_enabled" {
						value = 1
					}
					if name == "collection_last_success_timestamp_seconds" {
						value = float64(now.Unix())
					}
					for _, q := range []string{runtimeSnapshotPrimeQuery(job), "timestamp(" + runtimeSnapshotPrimeQuery(job) + ")"} {
						v := value
						if strings.HasPrefix(q, "timestamp(") {
							v = float64(now.Unix())
						}
						row := inputRow(job, up.Metric["instance"], "", v)
						for k, v := range up.Metric {
							row.Metric[k] = v
						}
						row.Metric["obs_metric"] = runtimeSnapshotPrimePrefix + name
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

func changeRuntimeSnapshotPrimeFixture(f inputFixture, name, class, instance string, value float64) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for i, row := range f[runtimeSnapshotPrimeQuery(job)] {
			if row.Metric["obs_metric"] == runtimeSnapshotPrimePrefix+name && row.Metric["class"] == class && (instance == "" || row.Metric["instance"] == instance) {
				f[runtimeSnapshotPrimeQuery(job)][i].Value[1] = strconv.FormatFloat(value, 'f', -1, 64)
			}
		}
	}
}

func TestRuntimeSnapshotPrimeClassesAndFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, state string
		incomplete  bool
		change      func(inputFixture, *RuntimeBuildSelection)
	}{
		{"empty", "empty", false, func(inputFixture, *RuntimeBuildSelection) {}},
		{"known_age", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "pending", "waiting", "", 5)
			changeRuntimeSnapshotPrimeFixture(f, "oldest_attempt_timestamp_seconds", "waiting", "", float64(now.Add(-time.Hour).Unix()))
		}},
		{"unknown_ages", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "pending", "active", "", 2)
			changeRuntimeSnapshotPrimeFixture(f, "age_unknown", "active", "", 2)
		}},
		{"mixed_ages", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "pending", "release_blocked", "", 3)
			changeRuntimeSnapshotPrimeFixture(f, "age_unknown", "release_blocked", "", 1)
			changeRuntimeSnapshotPrimeFixture(f, "oldest_attempt_timestamp_seconds", "release_blocked", "", float64(now.Add(-time.Hour).Unix()))
		}},
		{"disabled_pending", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "observation_enabled", "", "", 0)
			changeRuntimeSnapshotPrimeFixture(f, "pending", "waiting", "", 1)
			changeRuntimeSnapshotPrimeFixture(f, "age_unknown", "waiting", "", 1)
		}},
		{"failed_collection", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "collection_success", "", "", 0)
		}},
		{"never_observed", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "collection_success", "", "", 0)
			changeRuntimeSnapshotPrimeFixture(f, "collection_last_success_timestamp_seconds", "", "", 0)
		}},
		{"expired_collection", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "collection_last_success_timestamp_seconds", "", "", float64(now.Add(-3*time.Minute).Unix()))
		}},
		{"pre_boundary", "incomplete", true, func(f inputFixture, s *RuntimeBuildSelection) {
			s.after = now
			changeRuntimeSnapshotPrimeFixture(f, "collection_last_success_timestamp_seconds", "", "", float64(now.Add(-40*time.Second).Unix()))
		}},
		{"invalid_count", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "pending", "waiting", "", -1)
		}},
		{"invalid_age", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "age_unknown", "waiting", "", 1)
		}},
		{"known_without_clock", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "pending", "waiting", "", 1)
		}},
		{"large_exact", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "pending", "waiting", "", float64(api.ObsRuntimeSnapshotPrimePendingMax))
			changeRuntimeSnapshotPrimeFixture(f, "age_unknown", "waiting", "", float64(api.ObsRuntimeSnapshotPrimePendingMax))
		}},
		{"partial_positive", "nonempty", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeSnapshotPrimeFixture(f, "pending", "waiting", "", 1)
			changeRuntimeSnapshotPrimeFixture(f, "age_unknown", "waiting", "", 1)
			changeRuntimeSnapshotPrimeFixture(f, "pending", "active", "", -1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, selected.role, now)
			tc.change(f, &selected)
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			h := out.SnapshotPrimeHealth
			if !RuntimeSnapshotPrimeHealthValid(out.ObsRuntimeBuilds, h) || h.State != tc.state || h.Incomplete != tc.incomplete {
				t.Fatalf("invalid snapshot_prime: %+v", h)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-host") || out.State != "matching" {
				t.Fatal("snapshot_prime leaked labels or changed identity")
			}
			if tc.name == "unknown_ages" && (h.Counts.UnknownAgeScopes != 1 || h.Items[0].Classes[2].Values.OldestKnownAgeSeconds != nil) {
				t.Fatal("unknown age became a known zero")
			}
			if tc.name == "disabled_pending" && (h.Counts.DisabledScopes != 1 || h.Counts.NonemptyScopes != 1) {
				t.Fatal("disabled observer implied drained work")
			}
		})
	}
}

func TestRuntimeSnapshotPrimeFramesProfilesAndPreview(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, registry := runtimeFixture(role, now, 45)
		addLoopFixture(f, role, now)
		changeRuntimeSnapshotPrimeFixture(f, "pending", "active", "private-host-44:9100", 1)
		changeRuntimeSnapshotPrimeFixture(f, "age_unknown", "active", "private-host-44:9100", 1)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.SnapshotPrimeHealth != nil {
				t.Fatal("v1 gained snapshot_prime evidence")
			}
			continue
		}
		if !RuntimeSnapshotPrimeHealthValid(out.ObsRuntimeBuilds, out.SnapshotPrimeHealth) {
			t.Fatalf("invalid bounded preview: %+v", out.SnapshotPrimeHealth)
		}
		if role == "control-plane-v2" && (out.SnapshotPrimeHealth.Counts.NonemptyScopes != 1 || out.SnapshotPrimeHealth.Classes[2].UnknownAgeScopes != 1 || out.SnapshotPrimeHealth.OmittedItems < 13) {
			t.Fatal("full-set class evidence lost")
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
			t.Fatal("API budget exceeded")
		}
	}
	for _, fault := range []string{"mixed_clocks", "duplicate", "orphan", "missing_class", "unknown_class", "stale_sample", "unknown_inventory", "no_targets", "scrape_down"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			job := "schedd-compute"
			q, tq := runtimeSnapshotPrimeQuery(job), "timestamp("+runtimeSnapshotPrimeQuery(job)+")"
			switch fault {
			case "unknown_inventory":
				r.err = errors.New("private-error")
			case "no_targets":
				f, s, r = runtimeFixture("single-box-v2", now, 0)
				addLoopFixture(f, s.role, now)
				f[`up{job="schedd"}`] = nil
				f[`timestamp(up{job="schedd"})`] = nil
			case "scrape_down":
				f[`up{job="schedd"}`][0].Value[1] = "0"
			default:
				for _, query := range []string{q, tq} {
					rows := []inputFixtureRow{}
					for _, row := range f[query] {
						if row.Metric["class"] == "waiting" {
							if fault == "missing_class" {
								continue
							}
							if fault == "unknown_class" {
								row.Metric["class"] = "private-stage"
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
			if !RuntimeSnapshotPrimeHealthValid(out.ObsRuntimeBuilds, out.SnapshotPrimeHealth) || out.SnapshotPrimeHealth.State == "empty" || !out.SnapshotPrimeHealth.Incomplete {
				t.Fatalf("fault became known empty: %+v", out.SnapshotPrimeHealth)
			}
		})
	}
}

func TestRuntimeSnapshotPrimeAllClassesAndSchedulerIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for index, class := range runtimeSnapshotPrimeClasses() {
		t.Run(class, func(t *testing.T) {
			f, s, r := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, s.role, now)
			changeRuntimeSnapshotPrimeFixture(f, "pending", class, "", 2)
			changeRuntimeSnapshotPrimeFixture(f, "age_unknown", class, "", 1)
			if class == "unknown" {
				changeRuntimeSnapshotPrimeFixture(f, "age_unknown", class, "", 2)
			} else {
				changeRuntimeSnapshotPrimeFixture(f, "oldest_attempt_timestamp_seconds", class, "", float64(now.Add(-time.Hour).Unix()))
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			h := out.SnapshotPrimeHealth
			if !RuntimeSnapshotPrimeHealthValid(out.ObsRuntimeBuilds, h) || h.State != "nonempty" || h.Classes[index].NonemptyScopes != 1 || h.Classes[index].UnknownAgeScopes != 1 || h.Incomplete || out.State != "matching" {
				t.Fatalf("class changed identity or lost age evidence: %+v", h)
			}
		})
	}
	f, s, r := runtimeFixture("control-plane-v2", now, 1)
	addLoopFixture(f, s.role, now)
	// One job can retain positive evidence while the other job's frame fails.
	for i, row := range f[runtimeSnapshotPrimeQuery("schedd-compute")] {
		if row.Metric["class"] == "active" && strings.HasSuffix(row.Metric["obs_metric"], "_pending") {
			f[runtimeSnapshotPrimeQuery("schedd-compute")][i].Value[1] = "2"
		}
		if row.Metric["class"] == "active" && strings.HasSuffix(row.Metric["obs_metric"], "_age_unknown") {
			f[runtimeSnapshotPrimeQuery("schedd-compute")][i].Value[1] = "2"
		}
	}
	f[runtimeSnapshotPrimeQuery("schedd")][0].Metric["job"] = "schedd-compute"
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.SnapshotPrimeHealth
	if !RuntimeSnapshotPrimeHealthValid(out.ObsRuntimeBuilds, h) || h.State != "nonempty" || !h.Incomplete || h.Counts.NonemptyScopes != 1 || h.Counts.Unavailable != 1 || h.Counts.Expected != 2 {
		t.Fatalf("scheduler views were combined or positive evidence lost: %+v", h)
	}
	if len(h.Items) != 2 || h.Items[0].Job != "schedd-compute" || h.Items[1].Job != "schedd" || h.Items[0].TargetID == h.Items[1].TargetID {
		t.Fatal("scheduler scopes lost their job or target identity")
	}
}

func TestRuntimeSnapshotPrimeUnknownClassRejectsKnownTiming(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, s, r := runtimeFixture("single-box-v2", now, 1)
	addLoopFixture(f, s.role, now)
	changeRuntimeSnapshotPrimeFixture(f, "pending", "unknown", "", 2)
	changeRuntimeSnapshotPrimeFixture(f, "age_unknown", "unknown", "", 1)
	changeRuntimeSnapshotPrimeFixture(f, "oldest_attempt_timestamp_seconds", "unknown", "", float64(now.Add(-time.Hour).Unix()))
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.SnapshotPrimeHealth
	if !RuntimeSnapshotPrimeHealthValid(out.ObsRuntimeBuilds, h) || !h.Incomplete || h.State != "incomplete" || h.Items[0].Classes[5].Values != nil || h.Items[0].Classes[5].Reason != "invalid_value" {
		t.Fatalf("unknown class accepted a known boundary: %+v", h)
	}
}
