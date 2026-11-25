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

func addRuntimePostCaptureFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"imaged"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for i, name := range runtimePostCaptureMetricNames() {
				classes := []string{""}
				if i < 3 {
					classes = runtimePostCaptureClasses()
				}
				for _, class := range classes {
					value := float64(0)
					if name == "collection_success" || name == "observation_enabled" {
						value = 1
					}
					if name == "collection_last_success_timestamp_seconds" {
						value = float64(now.Unix())
					}
					for _, q := range []string{runtimePostCaptureQuery(job), "timestamp(" + runtimePostCaptureQuery(job) + ")"} {
						v := value
						if strings.HasPrefix(q, "timestamp(") {
							v = float64(now.Unix())
						}
						row := inputRow(job, up.Metric["instance"], "", v)
						for k, v := range up.Metric {
							row.Metric[k] = v
						}
						row.Metric["obs_metric"] = runtimePostCapturePrefix + name
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

func changeRuntimePostCaptureFixture(f inputFixture, name, class, instance string, value float64) {
	for _, job := range []string{"imaged"} {
		for i, row := range f[runtimePostCaptureQuery(job)] {
			if row.Metric["obs_metric"] == runtimePostCapturePrefix+name && row.Metric["class"] == class && (instance == "" || row.Metric["instance"] == instance) {
				f[runtimePostCaptureQuery(job)][i].Value[1] = strconv.FormatFloat(value, 'f', -1, 64)
			}
		}
	}
}

func TestRuntimePostCaptureClassesAndFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, state string
		incomplete  bool
		change      func(inputFixture, *RuntimeBuildSelection)
	}{
		{"empty", "empty", false, func(inputFixture, *RuntimeBuildSelection) {}},
		{"known_age", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "pending", "publication_pending", "", 5)
			changeRuntimePostCaptureFixture(f, "oldest_boundary_timestamp_seconds", "publication_pending", "", float64(now.Add(-time.Hour).Unix()))
		}},
		{"unknown_ages", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "pending", "dependency_held", "", 2)
			changeRuntimePostCaptureFixture(f, "age_unknown", "dependency_held", "", 2)
		}},
		{"mixed_ages", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "pending", "activation_pending", "", 3)
			changeRuntimePostCaptureFixture(f, "age_unknown", "activation_pending", "", 1)
			changeRuntimePostCaptureFixture(f, "oldest_boundary_timestamp_seconds", "activation_pending", "", float64(now.Add(-time.Hour).Unix()))
		}},
		{"disabled_pending", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "observation_enabled", "", "", 0)
			changeRuntimePostCaptureFixture(f, "pending", "publication_pending", "", 1)
			changeRuntimePostCaptureFixture(f, "age_unknown", "publication_pending", "", 1)
		}},
		{"failed_collection", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "collection_success", "", "", 0)
		}},
		{"never_observed", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "collection_success", "", "", 0)
			changeRuntimePostCaptureFixture(f, "collection_last_success_timestamp_seconds", "", "", 0)
		}},
		{"expired_collection", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "collection_last_success_timestamp_seconds", "", "", float64(now.Add(-3*time.Minute).Unix()))
		}},
		{"pre_boundary", "incomplete", true, func(f inputFixture, s *RuntimeBuildSelection) {
			s.after = now
			changeRuntimePostCaptureFixture(f, "collection_last_success_timestamp_seconds", "", "", float64(now.Add(-40*time.Second).Unix()))
		}},
		{"invalid_count", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "pending", "publication_pending", "", -1)
		}},
		{"invalid_age", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "age_unknown", "publication_pending", "", 1)
		}},
		{"known_without_clock", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "pending", "publication_pending", "", 1)
		}},
		{"large_exact", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "pending", "publication_pending", "", float64(api.ObsRuntimePostCapturePendingMax))
			changeRuntimePostCaptureFixture(f, "age_unknown", "publication_pending", "", float64(api.ObsRuntimePostCapturePendingMax))
		}},
		{"partial_positive", "nonempty", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimePostCaptureFixture(f, "pending", "publication_pending", "", 1)
			changeRuntimePostCaptureFixture(f, "age_unknown", "publication_pending", "", 1)
			changeRuntimePostCaptureFixture(f, "pending", "dependency_held", "", -1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, selected.role, now)
			tc.change(f, &selected)
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			h := out.PostCaptureHealth
			if !RuntimePostCaptureHealthValid(out.ObsRuntimeBuilds, h) || h.State != tc.state || h.Incomplete != tc.incomplete {
				t.Fatalf("invalid post_capture: %+v", h)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-host") || out.State != "matching" {
				t.Fatal("post_capture leaked labels or changed identity")
			}
			if tc.name == "unknown_ages" && (h.Counts.UnknownAgeViews != 1 || h.Items[0].Classes[2].Values.OldestKnownAgeSeconds != nil) {
				t.Fatal("unknown age became a known zero")
			}
			if tc.name == "disabled_pending" && (h.Counts.DisabledViews != 1 || h.Counts.NonemptyViews != 1) {
				t.Fatal("disabled observer implied drained work")
			}
		})
	}
}

func TestRuntimePostCaptureFramesProfilesAndPreview(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, registry := runtimeFixture(role, now, 45)
		addLoopFixture(f, role, now)
		changeRuntimePostCaptureFixture(f, "pending", "dependency_held", "private-host-44:9100", 1)
		changeRuntimePostCaptureFixture(f, "age_unknown", "dependency_held", "private-host-44:9100", 1)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.PostCaptureHealth != nil {
				t.Fatal("v1 gained post_capture evidence")
			}
			continue
		}
		if !RuntimePostCaptureHealthValid(out.ObsRuntimeBuilds, out.PostCaptureHealth) {
			t.Fatalf("invalid bounded preview: %+v", out.PostCaptureHealth)
		}
		if role == "control-plane-v2" && (out.PostCaptureHealth.Counts.NonemptyViews != 1 || out.PostCaptureHealth.Classes[2].UnknownAgeViews != 1 || out.PostCaptureHealth.OmittedItems < 13) {
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
			job := "imaged"
			q, tq := runtimePostCaptureQuery(job), "timestamp("+runtimePostCaptureQuery(job)+")"
			switch fault {
			case "unknown_inventory":
				r.err = errors.New("private-error")
			case "no_targets":
				f, s, r = runtimeFixture("single-box-v2", now, 0)
				addLoopFixture(f, s.role, now)
				f[`up{job="imaged"}`] = nil
				f[`timestamp(up{job="imaged"})`] = nil
			case "scrape_down":
				f[`up{job="imaged"}`][0].Value[1] = "0"
			default:
				for _, query := range []string{q, tq} {
					rows := []inputFixtureRow{}
					for _, row := range f[query] {
						if row.Metric["class"] == "publication_pending" {
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
			if !RuntimePostCaptureHealthValid(out.ObsRuntimeBuilds, out.PostCaptureHealth) || out.PostCaptureHealth.State == "empty" || !out.PostCaptureHealth.Incomplete {
				t.Fatalf("fault became known empty: %+v", out.PostCaptureHealth)
			}
		})
	}
}

func TestRuntimePostCaptureAllClassesAndReplicaViews(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for index, class := range runtimePostCaptureClasses() {
		t.Run(class, func(t *testing.T) {
			f, s, r := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, s.role, now)
			changeRuntimePostCaptureFixture(f, "pending", class, "", 2)
			changeRuntimePostCaptureFixture(f, "age_unknown", class, "", 1)
			if class == "unknown" {
				changeRuntimePostCaptureFixture(f, "age_unknown", class, "", 2)
			} else {
				changeRuntimePostCaptureFixture(f, "oldest_boundary_timestamp_seconds", class, "", float64(now.Add(-time.Hour).Unix()))
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			h := out.PostCaptureHealth
			if !RuntimePostCaptureHealthValid(out.ObsRuntimeBuilds, h) || h.State != "nonempty" || h.Classes[index].NonemptyViews != 1 || h.Classes[index].UnknownAgeViews != 1 || h.Incomplete || out.State != "matching" {
				t.Fatalf("class changed identity or lost age evidence: %+v", h)
			}
		})
	}
	// Replica views can disagree after separate durable reads. Retain both.
	f, s, r := runtimeFixture("control-plane-v2", now, 2)
	addLoopFixture(f, s.role, now)
	changeRuntimePostCaptureFixture(f, "pending", "activation_pending", "private-host-1:9100", 2)
	changeRuntimePostCaptureFixture(f, "age_unknown", "activation_pending", "private-host-1:9100", 2)
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.PostCaptureHealth
	if !RuntimePostCaptureHealthValid(out.ObsRuntimeBuilds, h) || h.Scope != RuntimePostCaptureScope || h.State != "nonempty" || h.Incomplete || h.Counts.NonemptyViews != 1 || h.Counts.EmptyViews != 1 || h.Counts.Expected != 2 || h.Counts.Observed != 2 {
		t.Fatalf("replica views were collapsed or treated as conflicts: %+v", h)
	}
	// The shared response cap may clip target details as advisory sections grow.
	if len(h.Items)+h.OmittedItems != 2 {
		t.Fatal("replica preview omissions lost")
	}
	client := inputFixtureClient(t, f, now)
	base, results := collectRuntimeBuilds(t.Context(), client, r, s, now)
	rows := make([][]RuntimePostCaptureItem, len(results))
	for i, result := range results {
		if result.job.Job == "imaged" {
			rows[i] = readRuntimePostCaptureJob(t.Context(), client, result, s, now)
		}
	}
	full := aggregateRuntimePostCapture(base, rows)
	if len(full.Items) != 2 || full.Items[0].Job != "imaged" || full.Items[0].TargetID == full.Items[1].TargetID {
		t.Fatal("replica identities lost before response fitting")
	}
}

func TestRuntimePostCaptureUnknownClassRejectsKnownTiming(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, s, r := runtimeFixture("single-box-v2", now, 1)
	addLoopFixture(f, s.role, now)
	changeRuntimePostCaptureFixture(f, "pending", "unknown", "", 2)
	changeRuntimePostCaptureFixture(f, "age_unknown", "unknown", "", 1)
	changeRuntimePostCaptureFixture(f, "oldest_boundary_timestamp_seconds", "unknown", "", float64(now.Add(-time.Hour).Unix()))
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.PostCaptureHealth
	if !RuntimePostCaptureHealthValid(out.ObsRuntimeBuilds, h) || !h.Incomplete || h.State != "incomplete" || h.Items[0].Classes[7].Values != nil || h.Items[0].Classes[7].Reason != "invalid_value" {
		t.Fatalf("unknown class accepted a known boundary: %+v", h)
	}
}
