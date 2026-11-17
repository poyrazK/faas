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

func addRuntimeBuildRecoveryFixture(f inputFixture, now time.Time) {
	for _, up := range f[`up{job="apid"}`] {
		for i, name := range runtimeBuildRecoveryMetricNames() {
			value := float64(0)
			if i == 0 {
				value = 1
			}
			if i == 1 {
				value = float64(now.Unix())
			}
			for _, q := range []string{runtimeBuildRecoveryQuery(), "timestamp(" + runtimeBuildRecoveryQuery() + ")"} {
				v := value
				if strings.HasPrefix(q, "timestamp(") {
					v = float64(now.Unix())
				}
				row := inputRow("apid", up.Metric["instance"], "", v)
				for k, v := range up.Metric {
					row.Metric[k] = v
				}
				row.Metric["obs_metric"] = name
				if !strings.HasPrefix(q, "timestamp(") {
					row.Metric["__name__"] = name
				}
				f[q] = append(f[q], row)
			}
		}
	}
}

func changeRuntimeBuildRecoveryFixture(f inputFixture, name, instance string, value float64) {
	for i, row := range f[runtimeBuildRecoveryQuery()] {
		if row.Metric["obs_metric"] == name && (instance == "" || row.Metric["instance"] == instance) {
			f[runtimeBuildRecoveryQuery()][i].Value[1] = strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
}
func runningRecoveryFixture(f inputFixture, now time.Time) {
	changeRuntimeBuildRecoveryFixture(f, "faas_build_running_count", "", 2)
	changeRuntimeBuildRecoveryFixture(f, "faas_build_running_oldest_started_timestamp_seconds", "", float64(now.Add(-3*time.Hour).Unix()))
}
func cleanupRecoveryFixture(f inputFixture, now time.Time) {
	changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_pending", "", 3)
	changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_retry_pending", "", 2)
	changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_oldest_created_timestamp_seconds", "", float64(now.Add(-24*time.Hour).Unix()))
}

func TestRuntimeBuildRecoveryValuesAndFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, state string
		incomplete  bool
		change      func(inputFixture, *RuntimeBuildSelection)
	}{
		{"empty", "empty", false, func(inputFixture, *RuntimeBuildSelection) {}},
		{"running", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) { runningRecoveryFixture(f, now) }},
		{"cleanup", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) { cleanupRecoveryFixture(f, now) }},
		{"both", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			runningRecoveryFixture(f, now)
			cleanupRecoveryFixture(f, now)
		}},
		{"partial_running", "nonempty", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			runningRecoveryFixture(f, now)
			changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_pending", "", -1)
		}},
		{"partial_cleanup", "nonempty", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			cleanupRecoveryFixture(f, now)
			changeRuntimeBuildRecoveryFixture(f, "faas_build_running_count", "", -1)
		}},
		{"failed", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			cleanupRecoveryFixture(f, now)
			changeRuntimeBuildRecoveryFixture(f, "apid_build_recovery_collection_success", "", 0)
		}},
		{"never_observed", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeBuildRecoveryFixture(f, "apid_build_recovery_collection_success", "", 0)
			changeRuntimeBuildRecoveryFixture(f, "apid_build_recovery_collection_last_success_timestamp_seconds", "", 0)
		}},
		{"stale", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeBuildRecoveryFixture(f, "apid_build_recovery_collection_last_success_timestamp_seconds", "", float64(now.Add(-3*time.Minute).Unix()))
		}},
		{"before_boundary", "incomplete", true, func(f inputFixture, s *RuntimeBuildSelection) {
			s.after = now
			changeRuntimeBuildRecoveryFixture(f, "apid_build_recovery_collection_last_success_timestamp_seconds", "", float64(now.Add(-40*time.Second).Unix()))
		}},
		{"invalid_subset", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_retry_pending", "", 1)
		}},
		{"fractional", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_pending", "", 1.5)
		}},
		{"missing_origin", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeBuildRecoveryFixture(f, "faas_build_running_count", "", 1)
		}},
		{"inconsistent_empty", "incomplete", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_oldest_created_timestamp_seconds", "", float64(now.Add(-time.Hour).Unix()))
		}},
		{"large_exact", "nonempty", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			cleanupRecoveryFixture(f, now)
			changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_pending", "", float64(api.ObsRuntimeBuildRecoveryCountMax))
			changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_retry_pending", "", float64(api.ObsRuntimeBuildRecoveryCountMax))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, s, r := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, s.role, now)
			tc.change(f, &s)
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			h := out.BuildRecoveryHealth
			if !RuntimeBuildRecoveryHealthValid(out.ObsRuntimeBuilds, h) || h.State != tc.state || h.Incomplete != tc.incomplete {
				t.Fatalf("invalid recovery evidence: %+v", h)
			}
			if tc.name == "cleanup" && (h.Counts.RetriedCleanupViews != 1 || *h.Items[0].Cleanup.Values.OldestAgeSeconds != 86400) {
				t.Fatal("retry rewrote obligation age or subset")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-host") || out.State != "matching" {
				t.Fatal("recovery changed identity or exposed labels")
			}
		})
	}
}

func TestRuntimeBuildRecoveryProfilesFramesAndPreview(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, r := runtimeFixture(role, now, 1)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.BuildRecoveryHealth != nil {
				t.Fatal("v1 gained recovery evidence")
			}
			continue
		}
		if !RuntimeBuildRecoveryHealthValid(out.ObsRuntimeBuilds, out.BuildRecoveryHealth) {
			t.Fatal("invalid selected recovery evidence")
		}
	}
	f, s, r := runtimeFixture("control-plane-v2", now, 1)
	addLoopFixture(f, s.role, now)
	for q, rows := range f {
		for i := 1; i < 45; i++ {
			for _, row := range rows {
				if row.Metric["job"] != "apid" {
					continue
				}
				copy := inputRow("apid", fmt.Sprintf("private-replica-%d:9100", i), "", 0)
				for k, v := range row.Metric {
					copy.Metric[k] = v
				}
				copy.Metric["instance"] = fmt.Sprintf("private-replica-%d:9100", i)
				copy.Value = row.Value
				f[q] = append(f[q], copy)
			}
		}
	}
	changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_pending", "private-replica-44:9100", 3)
	changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_retry_pending", "private-replica-44:9100", 2)
	changeRuntimeBuildRecoveryFixture(f, "faas_build_vm_cleanup_oldest_created_timestamp_seconds", "private-replica-44:9100", float64(now.Add(-24*time.Hour).Unix()))
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.BuildRecoveryHealth
	if !RuntimeBuildRecoveryHealthValid(out.ObsRuntimeBuilds, h) || h.Counts.Observed != 45 || h.Counts.CleanupViews != 1 || h.Counts.EmptyViews != 44 || h.OmittedItems < 13 {
		t.Fatalf("full replica view counts lost: %+v", h)
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("API budget exceeded")
	}
	for _, fault := range []string{"mixed_clocks", "duplicate", "orphan", "missing_metric", "stale_sample", "scrape_down", "registry_unavailable"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			q, tq := runtimeBuildRecoveryQuery(), "timestamp("+runtimeBuildRecoveryQuery()+")"
			switch fault {
			case "registry_unavailable":
				r.err = errors.New("private-provider-error")
			case "scrape_down":
				f[`up{job="apid"}`][0].Value[1] = "0"
			default:
				for _, query := range []string{q, tq} {
					rows := []inputFixtureRow{}
					for _, row := range f[query] {
						if row.Metric["obs_metric"] == "faas_build_vm_cleanup_pending" {
							if fault == "missing_metric" {
								continue
							}
							if fault == "duplicate" {
								rows = append(rows, row)
							}
							if query == tq {
								if fault == "mixed_clocks" {
									row.Value[1] = fmt.Sprint(now.Add(-time.Second).Unix())
								}
								if fault == "stale_sample" {
									row.Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
								}
								if fault == "orphan" {
									row.Metric["private_label"] = "private-provider-error"
								}
							}
						}
						rows = append(rows, row)
					}
					f[query] = rows
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
			h := out.BuildRecoveryHealth
			if !RuntimeBuildRecoveryHealthValid(out.ObsRuntimeBuilds, h) {
				t.Fatal("invalid unavailable evidence")
			}
			if fault == "registry_unavailable" {
				if h.Incomplete || h.State != "empty" {
					t.Fatal("compute registry poisoned static apid observation")
				}
			} else if h.State == "empty" || !h.Incomplete {
				t.Fatal("source gap became empty")
			}
		})
	}
}
