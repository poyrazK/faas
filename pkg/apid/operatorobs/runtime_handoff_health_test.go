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

func addRuntimeHandoffFixture(f inputFixture, now time.Time) {
	defaults := map[string]float64{"collection_success": 1, "collection_last_success_timestamp_seconds": float64(now.Unix()), "pending": 0, "oldest_finished_timestamp_seconds": 0, "recovery_enabled": 1, "recovery_inflight": 0, "recovery_last_attempt_timestamp_seconds": float64(now.Add(-5 * time.Second).Unix()), "recovery_last_completed_timestamp_seconds": float64(now.Add(-4 * time.Second).Unix())}
	for _, up := range f[`up{job="imaged"}`] {
		for suffix, value := range defaults {
			for _, q := range []string{runtimeHandoffQuery(), "timestamp(" + runtimeHandoffQuery() + ")"} {
				v := value
				if strings.HasPrefix(q, "timestamp(") {
					v = float64(now.Unix())
				}
				row := inputRow("imaged", up.Metric["instance"], "", v)
				for k, v := range up.Metric {
					row.Metric[k] = v
				}
				row.Metric["obs_metric"] = "imaged_build_handoff_" + suffix
				if !strings.HasPrefix(q, "timestamp(") {
					row.Metric["__name__"] = row.Metric["obs_metric"]
				}
				f[q] = append(f[q], row)
			}
		}
	}
}

func changeRuntimeHandoffFixture(f inputFixture, suffix, instance string, value float64) {
	for i, row := range f[runtimeHandoffQuery()] {
		if row.Metric["obs_metric"] == "imaged_build_handoff_"+suffix && (instance == "" || row.Metric["instance"] == instance) {
			f[runtimeHandoffQuery()][i].Value[1] = strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
}

func TestRuntimeHandoffSnapshotAndRecoveryEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, state, backlog, recovery string
		incomplete                     bool
		change                         func(inputFixture, *RuntimeBuildSelection)
	}{
		{"empty", "empty", "empty", "waiting", false, func(inputFixture, *RuntimeBuildSelection) {}},
		{"waiting", "waiting", "waiting", "waiting", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "pending", "", 1)
			changeRuntimeHandoffFixture(f, "oldest_finished_timestamp_seconds", "", float64(now.Add(-10*time.Minute).Unix()))
		}},
		{"long_recovery", "empty", "empty", "working", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "recovery_inflight", "", 1)
			changeRuntimeHandoffFixture(f, "recovery_last_attempt_timestamp_seconds", "", float64(now.Add(-time.Hour).Unix()))
			changeRuntimeHandoffFixture(f, "recovery_last_completed_timestamp_seconds", "", float64(now.Add(-2*time.Hour).Unix()))
		}},
		{"stopping_inflight", "empty", "empty", "stopping", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "recovery_enabled", "", 0)
			changeRuntimeHandoffFixture(f, "recovery_inflight", "", 1)
			changeRuntimeHandoffFixture(f, "recovery_last_completed_timestamp_seconds", "", float64(now.Add(-6*time.Second).Unix()))
		}},
		{"not_started", "empty", "empty", "not_started", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "recovery_last_attempt_timestamp_seconds", "", 0)
			changeRuntimeHandoffFixture(f, "recovery_last_completed_timestamp_seconds", "", 0)
		}},
		{"collection_failed", "incomplete", "unavailable", "waiting", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "collection_success", "", 0)
		}},
		{"never_observed", "incomplete", "unavailable", "waiting", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "collection_success", "", 0)
			changeRuntimeHandoffFixture(f, "collection_last_success_timestamp_seconds", "", 0)
		}},
		{"stale_collection", "incomplete", "stale", "waiting", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "collection_last_success_timestamp_seconds", "", float64(now.Add(-3*time.Minute).Unix()))
		}},
		{"collection_before_boundary", "incomplete", "unavailable", "waiting", true, func(f inputFixture, s *RuntimeBuildSelection) {
			s.after = now
			changeRuntimeHandoffFixture(f, "collection_last_success_timestamp_seconds", "", float64(now.Add(-40*time.Second).Unix()))
		}},
		{"invalid_pending", "incomplete", "unavailable", "waiting", true, func(f inputFixture, _ *RuntimeBuildSelection) { changeRuntimeHandoffFixture(f, "pending", "", -1) }},
		{"inconsistent_empty", "incomplete", "unavailable", "waiting", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "oldest_finished_timestamp_seconds", "", float64(now.Add(-time.Minute).Unix()))
		}},
		{"partial_positive", "waiting", "waiting", "unavailable", true, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "pending", "", 2)
			changeRuntimeHandoffFixture(f, "oldest_finished_timestamp_seconds", "", float64(now.Add(-time.Hour).Unix()))
			changeRuntimeHandoffFixture(f, "recovery_enabled", "", 2)
		}},
		{"large_scope", "waiting", "waiting", "waiting", false, func(f inputFixture, _ *RuntimeBuildSelection) {
			changeRuntimeHandoffFixture(f, "pending", "", float64(api.ObsRuntimeHandoffPendingMax))
			changeRuntimeHandoffFixture(f, "oldest_finished_timestamp_seconds", "", float64(now.Add(-time.Minute).Unix()))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, selected.role, now)
			tc.change(f, &selected)
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			h := out.HandoffHealth
			if !RuntimeHandoffHealthValid(out.ObsRuntimeBuilds, h) || h.State != tc.state || h.Incomplete != tc.incomplete || len(h.Items) != 1 || h.Items[0].Backlog.State != tc.backlog || h.Items[0].Recovery.State != tc.recovery {
				t.Fatalf("incorrect handoff evidence: %+v", h)
			}
			if (h.Counts.WaitingScopes > 0) != (tc.backlog == "waiting") || (h.Counts.EmptyScopes > 0) != (tc.backlog == "empty") {
				t.Fatal("unknown became an empty scope")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-host") || out.State != "matching" {
				t.Fatal("handoff evidence leaked source labels or changed identity")
			}
		})
	}
}

func TestRuntimeHandoffProfilesFramesAndUnknownInventory(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, selected, registry := runtimeFixture(role, now, 45)
		addLoopFixture(f, role, now)
		changeRuntimeHandoffFixture(f, "pending", "private-host-44:9100", 100000)
		changeRuntimeHandoffFixture(f, "oldest_finished_timestamp_seconds", "private-host-44:9100", float64(now.Add(-time.Hour).Unix()))
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
		if strings.HasSuffix(role, "v1") {
			if out.HandoffHealth != nil {
				t.Fatal("v1 gained handoff evidence")
			}
			continue
		}
		if !RuntimeHandoffHealthValid(out.ObsRuntimeBuilds, out.HandoffHealth) {
			t.Fatalf("invalid full-set handoff counts: %+v", out.HandoffHealth)
		}
		if role == "control-plane-v2" && (out.HandoffHealth.Counts.Observed != 45 || out.HandoffHealth.Counts.WaitingScopes != 1 || out.HandoffHealth.OmittedItems < 13) {
			t.Fatal("waiting scope beyond preview was lost")
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
			t.Fatal("handoff exceeded API byte budget")
		}
	}
	for _, fault := range []string{"mixed_snapshot_clocks", "mixed_recovery_clocks", "duplicate", "orphan", "missing_pending", "stale_sample", "unknown_inventory", "empty_inventory", "scrape_down"} {
		t.Run(fault, func(t *testing.T) {
			f, selected, registry := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, selected.role, now)
			q, tq := runtimeHandoffQuery(), "timestamp("+runtimeHandoffQuery()+")"
			switch fault {
			case "unknown_inventory":
				registry.err = errors.New("private-source-error")
			case "empty_inventory":
				f, selected, registry = runtimeFixture("control-plane-v2", now, 0)
				addLoopFixture(f, selected.role, now)
			case "scrape_down":
				f[`up{job="imaged"}`][0].Value[1] = "0"
			default:
				for _, query := range []string{q, tq} {
					rows := []inputFixtureRow{}
					for _, row := range f[query] {
						name := row.Metric["obs_metric"]
						if fault == "missing_pending" && strings.HasSuffix(name, "_pending") {
							continue
						}
						if query == tq {
							switch {
							case fault == "mixed_snapshot_clocks" && strings.HasSuffix(name, "_pending"):
								row.Value[1] = fmt.Sprint(now.Add(-time.Second).Unix())
							case fault == "mixed_recovery_clocks" && strings.HasSuffix(name, "_recovery_inflight"):
								row.Value[1] = fmt.Sprint(now.Add(-time.Second).Unix())
							case fault == "duplicate" && strings.HasSuffix(name, "_collection_success"):
								rows = append(rows, row)
							case fault == "orphan" && strings.HasSuffix(name, "_pending"):
								row.Metric["private_label"] = "private-source-error"
							case fault == "stale_sample" && strings.HasSuffix(name, "_pending"):
								row.Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
							}
						}
						rows = append(rows, row)
					}
					f[query] = rows
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			if !RuntimeHandoffHealthValid(out.ObsRuntimeBuilds, out.HandoffHealth) || out.HandoffHealth.Counts.Complete != 0 || out.HandoffHealth.Counts.EmptyScopes != 0 && fault != "mixed_recovery_clocks" {
				t.Fatalf("untrusted handoff became empty/complete: %+v", out.HandoffHealth)
			}
		})
	}
}
