package operatorobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func addRuntimeBuilderFixture(f inputFixture, now time.Time) {
	query := runtimeBuilderQuery()
	defaults := map[string]float64{
		"builderd_build_worker_started_timestamp_seconds/":        float64(now.Add(-time.Hour).Unix()),
		"builderd_build_worker_last_attempt_timestamp_seconds/":   float64(now.Add(-5 * time.Second).Unix()),
		"builderd_build_worker_last_completed_timestamp_seconds/": float64(now.Add(-4 * time.Second).Unix()),
		"builderd_build_worker_enabled/":                          1, "builderd_build_worker_interval_seconds/": 2, "builderd_build_worker_busy_allowance_seconds/": 900,
		"builderd_build_slots_limit/": 1,
	}
	for _, queue := range []string{"build", "cancel"} {
		defaults["builderd_notification_queue_capacity/"+queue] = 64
		defaults["builderd_notification_queue_workers/"+queue] = 2
		defaults["builderd_notification_queue_enabled/"+queue] = 1
	}
	for _, up := range f[`up{job="builderd"}`] {
		for signal := range runtimeBuilderSignalKeys() {
			name, queue, _ := strings.Cut(signal, "/")
			for _, q := range []string{query, "timestamp(" + query + ")"} {
				value := defaults[signal]
				if strings.HasPrefix(q, "timestamp(") {
					value = float64(now.Unix())
				}
				row := inputRow("builderd", up.Metric["instance"], "", value)
				for key, value := range up.Metric {
					row.Metric[key] = value
				}
				row.Metric["obs_metric"] = name
				if queue != "" {
					row.Metric["queue"] = queue
				}
				f[q] = append(f[q], row)
			}
		}
	}
}

func changeRuntimeBuilderFixture(f inputFixture, metric, queue, instance string, value float64) {
	for i, row := range f[runtimeBuilderQuery()] {
		if row.Metric["obs_metric"] == metric && row.Metric["queue"] == queue && (instance == "" || row.Metric["instance"] == instance) {
			f[runtimeBuilderQuery()][i].Value[1] = strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
}

func TestRuntimeBuilderWorkerAndPressureEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, worker, state string
		incomplete          bool
		change              func(inputFixture)
	}{
		{"waiting", "waiting", "observed", false, func(inputFixture) {}},
		{"dispatch_handoff", "waiting", "observed", false, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_notification_queue_capacity", "build", "", 0)
			changeRuntimeBuilderFixture(f, "builderd_notification_queue_active", "build", "", 1)
		}},
		{"long_build", "working", "observed", false, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_build_worker_inflight", "", "", 1)
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_attempt_timestamp_seconds", "", "", float64(now.Add(-10*time.Minute).Unix()))
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_completed_timestamp_seconds", "", "", float64(now.Add(-11*time.Minute).Unix()))
		}},
		{"hung_build", "overdue", "overdue", false, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_build_worker_inflight", "", "", 1)
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_attempt_timestamp_seconds", "", "", float64(now.Add(-20*time.Minute).Unix()))
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_completed_timestamp_seconds", "", "", float64(now.Add(-21*time.Minute).Unix()))
		}},
		{"idle_stall", "overdue", "overdue", false, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_attempt_timestamp_seconds", "", "", float64(now.Add(-91*time.Second).Unix()))
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_completed_timestamp_seconds", "", "", float64(now.Add(-90*time.Second).Unix()))
		}},
		{"draining", "draining", "observed", false, func(f inputFixture) { changeRuntimeBuilderFixture(f, "builderd_build_worker_enabled", "", "", 0) }},
		{"before_first_poll", "waiting", "observed", false, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_build_worker_started_timestamp_seconds", "", "", float64(now.Add(-10*time.Second).Unix()))
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_attempt_timestamp_seconds", "", "", 0)
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_completed_timestamp_seconds", "", "", 0)
		}},
		{"invalid_flags", "unavailable", "incomplete", true, func(f inputFixture) { changeRuntimeBuilderFixture(f, "builderd_build_worker_enabled", "", "", 2) }},
		{"invalid_clock", "unavailable", "incomplete", true, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_completed_timestamp_seconds", "", "", float64(now.Add(time.Second).Unix()))
		}},
		{"invalid_budget", "unavailable", "incomplete", true, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_build_worker_interval_seconds", "", "", 0)
		}},
		{"invalid_queue", "waiting", "incomplete", true, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_notification_queue_active", "cancel", "", 3)
		}},
		{"partial_positive", "overdue", "overdue", true, func(f inputFixture) {
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_attempt_timestamp_seconds", "", "", float64(now.Add(-91*time.Second).Unix()))
			changeRuntimeBuilderFixture(f, "builderd_build_worker_last_completed_timestamp_seconds", "", "", float64(now.Add(-90*time.Second).Unix()))
			changeRuntimeBuilderFixture(f, "builderd_build_slots_limit", "", "", 0)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, selected.role, now)
			changeRuntimeBuilderFixture(f, "builderd_build_slots_in_use", "", "", 1)
			changeRuntimeBuilderFixture(f, "builderd_notification_queue_waiting", "build", "", 1)
			changeRuntimeBuilderFixture(f, "builderd_notification_queue_oldest_enqueued_timestamp_seconds", "build", "", float64(now.Add(-5*time.Minute).Unix()))
			tc.change(f)
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			h := out.BuilderHealth
			if !RuntimeBuilderHealthValid(out.ObsRuntimeBuilds, h) || h.State != tc.state || h.Incomplete != tc.incomplete || len(h.Items) != 1 || h.Items[0].Worker.State != tc.worker || h.Counts.WaitingQueues != 1 {
				t.Fatalf("incorrect builder observation: %+v", h)
			}
			if tc.name != "partial_positive" && h.Counts.SaturatedSlots != 1 {
				t.Fatal("slot saturation evidence lost")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-host") || out.State != "matching" {
				t.Fatal("builder evidence leaked labels or changed identity")
			}
		})
	}
}

func TestRuntimeBuilderSourceAndInventoryGaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, fault := range []string{"nonfinite_source", "unknown_inventory", "empty_inventory", "scrape_down", "scrape_stale"} {
		t.Run(fault, func(t *testing.T) {
			f, selected, registry := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, selected.role, now)
			switch fault {
			case "nonfinite_source":
				changeRuntimeBuilderFixture(f, "builderd_build_worker_interval_seconds", "", "", math.NaN())
			case "unknown_inventory":
				registry.err = errors.New("private-inventory-error")
			case "empty_inventory":
				f, selected, registry = runtimeFixture("control-plane-v2", now, 0)
				addLoopFixture(f, selected.role, now)
			case "scrape_down":
				f[`up{job="builderd"}`][0].Value[1] = "0"
			case "scrape_stale":
				f[`timestamp(up{job="builderd"})`][0].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			if !RuntimeBuilderHealthValid(out.ObsRuntimeBuilds, out.BuilderHealth) || out.BuilderHealth.Counts.OverdueWorkers != 0 || out.BuilderHealth.Counts.Complete != 0 {
				t.Fatalf("unusable source/inventory became usable: %+v", out.BuilderHealth)
			}
			if fault == "empty_inventory" && out.BuilderHealth.State != "unknown" {
				t.Fatal("empty fleet became observed progress")
			}
		})
	}
}

func TestRuntimeBuilderProfilesClocksAndPreview(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, selected, registry := runtimeFixture(role, now, 45)
		addLoopFixture(f, role, now)
		changeRuntimeBuilderFixture(f, "builderd_build_worker_last_attempt_timestamp_seconds", "", "private-host-44:9100", float64(now.Add(-91*time.Second).Unix()))
		changeRuntimeBuilderFixture(f, "builderd_build_worker_last_completed_timestamp_seconds", "", "private-host-44:9100", float64(now.Add(-90*time.Second).Unix()))
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
		if strings.HasSuffix(role, "v1") {
			if out.BuilderHealth != nil {
				t.Fatal("v1 profile gained builder evidence")
			}
			continue
		}
		if !RuntimeBuilderHealthValid(out.ObsRuntimeBuilds, out.BuilderHealth) {
			t.Fatalf("invalid full-set builder counts: %+v", out.BuilderHealth)
		}
		if role == "control-plane-v2" && (out.BuilderHealth.Counts.Observed != 45 || out.BuilderHealth.Counts.OverdueWorkers != 1 || out.BuilderHealth.OmittedItems < 13) {
			t.Fatal("fault beyond preview was lost")
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		if len(body) >= 64<<10 {
			t.Fatal("builder evidence exceeded API envelope")
		}
	}
	for _, fault := range []string{"stale", "before_boundary", "duplicate", "missing", "orphan"} {
		t.Run(fault, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, selected.role, now)
			q := runtimeBuilderQuery()
			if fault == "missing" {
				f[q], f["timestamp("+q+")"] = []inputFixtureRow{}, []inputFixtureRow{}
			} else {
				for i, row := range f["timestamp("+q+")"] {
					if row.Metric["obs_metric"] != "builderd_build_worker_enabled" {
						continue
					}
					switch fault {
					case "stale":
						f["timestamp("+q+")"][i].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
					case "before_boundary":
						selected.after = now
						f["timestamp("+q+")"][i].Value[1] = fmt.Sprint(now.Add(-40 * time.Second).Unix())
					case "duplicate":
						f["timestamp("+q+")"] = append(f["timestamp("+q+")"], row)
					case "orphan":
						row.Metric["private_extra"] = "private-error"
					}
					break
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			if !RuntimeBuilderHealthValid(out.ObsRuntimeBuilds, out.BuilderHealth) || !out.BuilderHealth.Incomplete || out.BuilderHealth.Counts.OverdueWorkers != 0 {
				t.Fatal("invalid or stale telemetry became usable")
			}
		})
	}
}
