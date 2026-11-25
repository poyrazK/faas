package operatorobs

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func addRuntimeCommandCronOutcomeFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for _, d := range runtimeCommandCronOutcomeDimensions() {
				metric, _ := runtimeCommandCronOutcomeMetric(d)
				for _, q := range []string{runtimeCommandCronOutcomeQuery(job), "timestamp(" + runtimeCommandCronOutcomeQuery(job) + ")"} {
					n := float64(0)
					if strings.HasPrefix(q, "timestamp(") {
						n = float64(now.Unix())
					}
					row := inputRow(job, up.Metric["instance"], "", n)
					for k, v := range up.Metric {
						row.Metric[k] = v
					}
					row.Metric["obs_metric"] = metric
					row.Metric["outcome"] = d.Outcome
					if !strings.HasPrefix(q, "timestamp(") {
						row.Metric["__name__"] = metric
					}
					f[q] = append(f[q], row)
				}
			}
		}
	}
}

func TestRuntimeCommandCronOutcomesYieldToLoopReads(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("single-box-v2", now, 1)
	addLoopFixture(f, selected.role, now)
	loopQueries := map[string]bool{}
	for query := range f {
		if strings.Contains(query, "_loop_stalled{") || strings.Contains(query, "_loop_last_beat_age_seconds{") {
			loopQueries[query] = true
		}
	}
	var remaining, counterReads, timingReads, deliveryReads, delayedReads atomic.Int32
	remaining.Store(int32(len(loopQueries)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if query == runtimeCommandCronOutcomeQuery("schedd") || query == "timestamp("+runtimeCommandCronOutcomeQuery("schedd")+")" {
			counterReads.Add(1)
			if remaining.Load() != 0 {
				t.Error("new counter reads preempted existing loop evidence")
			}
		}
		if query == runtimeCommandCronTimingQuery("schedd") || query == "timestamp("+runtimeCommandCronTimingQuery("schedd")+")" {
			timingReads.Add(1)
			if remaining.Load() != 0 {
				t.Error("timing reads preempted existing loop evidence")
			}
		}
		if query == runtimeHTTPCronDeliveryQuery("schedd") || query == "timestamp("+runtimeHTTPCronDeliveryQuery("schedd")+")" {
			deliveryReads.Add(1)
			if remaining.Load() != 0 {
				t.Error("HTTP delivery reads preempted existing loop evidence")
			}
		}
		if query == runtimeDelayedTaskDispatchQuery("schedd") || query == "timestamp("+runtimeDelayedTaskDispatchQuery("schedd")+")" {
			delayedReads.Add(1)
			if remaining.Load() != 0 {
				t.Error("delayed dispatch reads preempted loop evidence")
			}
		}
		if loopQueries[query] {
			remaining.Add(-1)
		}
		rows, ok := f[query]
		if !ok {
			t.Error("unexpected query")
		}
		if rows == nil {
			rows = []inputFixtureRow{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}})
	}))
	defer srv.Close()
	out := RuntimeBuildsWithLoopHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), registry, selected, now)
	if remaining.Load() != 0 || counterReads.Load() != 2 || deliveryReads.Load() != 2 || delayedReads.Load() != 2 || out.DelayedTaskDispatch.Counts.Baseline != 4 || out.HTTPCronDelivery.Counts.Baseline != 5 || timingReads.Load() != 2 || out.CommandCronTiming.Counts.Baseline != 13 || out.LoopHealth.Incomplete || out.CommandCronOutcomes.Counts.Baseline != 29 {
		t.Fatal("priority or counter collection was lost")
	}
}
func changeRuntimeCommandCronOutcomeFixture(f inputFixture, source, outcome string, n float64) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		q := runtimeCommandCronOutcomeQuery(job)
		for i, row := range f[q] {
			m, _ := runtimeCommandCronOutcomeMetric(RuntimeCommandCronOutcomeDimension{Source: source, Outcome: outcome})
			if row.Metric["obs_metric"] == m && row.Metric["outcome"] == outcome {
				f[q][i].Value[1] = strconv.FormatFloat(n, 'f', -1, 64)
			}
		}
	}
}
func TestRuntimeCommandCronOutcomesComparisons(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	for _, fault := range []string{"baseline", "unchanged", "admission_error", "decision_canceled", "sweep_error", "exclusive_pending", "queued", "coalesced", "skipped_overlap", "missed_deadline", "rejected", "stale_attempt", "unknown_outcome", "lookup_error", "sweep_success", "admission_canceled", "counter_reset", "fractional", "process_changed", "repeated", "uncertain_window", "mixed_clocks", "previous_missing", "partial", "partial_sweep", "duplicate", "orphan", "stale", "max_exact"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			previous, _, _ := runtimeFixture(s.role, at, 1)
			addLoopFixture(previous, s.role, at)
			s.compareAt = at
			for _, spec := range runtimeBuildJobSpecs(s.role) {
				if spec.daemon == "schedd" {
					q := runtimeProcessQuery(spec)
					for i := range f[q] {
						f[q][i].Value[1] = "160"
					}
				}
			}
			switch fault {
			case "baseline":
				s.compareAt = time.Time{}
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "error", 100)
			case "admission_error":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "error", 3)
			case "decision_canceled":
				changeRuntimeCommandCronOutcomeFixture(f, "decision", "canceled", 3)
			case "sweep_error":
				changeRuntimeCommandCronOutcomeFixture(f, "sweep", "error", 3)
			case "exclusive_pending", "queued", "coalesced", "skipped_overlap", "missed_deadline", "rejected":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", fault, 3)
			case "stale_attempt":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "stale", 3)
			case "unknown_outcome":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "unknown", 3)
			case "lookup_error":
				changeRuntimeCommandCronOutcomeFixture(f, "decision", "lookup_error", 3)
			case "sweep_success":
				changeRuntimeCommandCronOutcomeFixture(f, "sweep", "success", 3)
			case "admission_canceled":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "canceled", 3)
			case "counter_reset":
				changeRuntimeCommandCronOutcomeFixture(previous, "admission", "error", 3)
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "error", 1)
			case "fractional":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "error", 1.5)
			case "process_changed", "repeated", "uncertain_window":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "error", 3)
				for _, spec := range runtimeBuildJobSpecs(s.role) {
					if spec.daemon != "schedd" {
						continue
					}
					q := runtimeProcessQuery(spec)
					if fault == "process_changed" {
						for i := range f[q] {
							f[q][i].Value[1] = "5"
						}
					} else if fault == "uncertain_window" {
						for i := range f[q] {
							f[q][i].Value[1] = "130"
						}
					} else {
						for i := range f[q] {
							f[q][i].Value[1] = "130"
						}
						for i := range f["timestamp("+q+")"] {
							f["timestamp("+q+")"][i].Value[1] = strconv.FormatInt(at.Unix(), 10)
						}
					}
				}
			case "partial_sweep":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "queued", 3)
			case "partial":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "error", 3)
			case "max_exact":
				changeRuntimeCommandCronOutcomeFixture(f, "admission", "error", float64(api.ObsRuntimeAppTaskOperationCounterMax))
			}
			for _, job := range []string{"schedd", "schedd-compute"} {
				target := f
				if fault == "previous_missing" {
					target = previous
				}
				for _, q := range []string{runtimeCommandCronOutcomeQuery(job), "timestamp(" + runtimeCommandCronOutcomeQuery(job) + ")"} {
					rows := []inputFixtureRow{}
					for _, row := range target[q] {
						if fault == "partial_sweep" && row.Metric["obs_metric"] == "schedd_command_cron_sweeps_total" && row.Metric["outcome"] == "success" {
							continue
						}
						if row.Metric["obs_metric"] == "schedd_command_cron_admissions_total" && row.Metric["outcome"] == "queued" {
							if fault == "previous_missing" || fault == "partial" {
								continue
							}
							if fault == "duplicate" {
								rows = append(rows, row)
							}
							if fault == "orphan" {
								row.Metric["private_error"] = "private-provider-error"
							}
							if fault == "mixed_clocks" && strings.HasPrefix(q, "timestamp(") {
								row.Value[1] = strconv.FormatInt(now.Add(-time.Second).Unix(), 10)
							}
							if fault == "stale" && strings.HasPrefix(q, "timestamp(") {
								row.Value[1] = strconv.FormatInt(now.Add(-3*time.Minute).Unix(), 10)
							}
						}
						rows = append(rows, row)
					}
					target[q] = rows
				}
			}
			out := RuntimeBuildsWithLoopHealth(t.Context(), runtimeProcessClient(t, f, previous, now, at, ""), r, s, now)
			h := out.CommandCronOutcomes
			if !RuntimeCommandCronOutcomesValid(out.ObsRuntimeBuilds, h) {
				t.Fatalf("invalid operation evidence: %+v", h)
			}
			increased := fault == "partial_sweep" || fault == "exclusive_pending" || fault == "coalesced" || fault == "skipped_overlap" || fault == "missed_deadline" || fault == "rejected" || fault == "lookup_error" || fault == "stale_attempt" || fault == "unknown_outcome" || fault == "queued" || fault == "sweep_success" || fault == "admission_canceled" || fault == "admission_error" || fault == "decision_canceled" || fault == "sweep_error" || fault == "partial" || fault == "max_exact"
			if (h.Counts.Increased > 0) != increased || out.State != "matching" {
				t.Fatalf("misleading changes: %+v", h)
			}
			if fault == "baseline" && (h.Counts.Baseline != 58 || h.Counts.Increased != 0) {
				t.Fatal("old counters became new activity")
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "private-provider-error") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestRuntimeCommandCronOutcomesProfilesPreviewAndPartialRecovery(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, selected, registry := runtimeFixture(role, now, 1)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
		if strings.HasSuffix(role, "v1") {
			if out.CommandCronOutcomes != nil {
				t.Fatal("legacy profile gained result evidence")
			}
		} else if !RuntimeCommandCronOutcomesValid(out.ObsRuntimeBuilds, out.CommandCronOutcomes) {
			t.Fatal("invalid profile result evidence")
		}
	}
	f, selected, registry := runtimeFixture("control-plane-v2", now, 40)
	addLoopFixture(f, selected.role, now)
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	h := out.CommandCronOutcomes
	if !RuntimeCommandCronOutcomesValid(out.ObsRuntimeBuilds, h) || h.Counts.Baseline != 41*29 || h.OmittedItems < 9 {
		t.Fatalf("lost full-set coverage: %+v", h)
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("API response exceeded bound")
	}
}

func TestRuntimeCommandCronOutcomesEveryCategory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	f, selected, registry := runtimeFixture("single-box-v2", now, 1)
	addLoopFixture(f, selected.role, now)
	previous, _, _ := runtimeFixture(selected.role, at, 1)
	addLoopFixture(previous, selected.role, at)
	selected.compareAt = at
	for _, spec := range runtimeBuildJobSpecs(selected.role) {
		if spec.daemon == "schedd" {
			for i := range f[runtimeProcessQuery(spec)] {
				f[runtimeProcessQuery(spec)][i].Value[1] = "160"
			}
		}
	}
	for _, d := range runtimeCommandCronOutcomeDimensions() {
		changeRuntimeCommandCronOutcomeFixture(f, d.Source, d.Outcome, 1)
	}
	out := RuntimeBuildsWithLoopHealth(t.Context(), runtimeProcessClient(t, f, previous, now, at, ""), registry, selected, now)
	h := out.CommandCronOutcomes
	if !RuntimeCommandCronOutcomesValid(out.ObsRuntimeBuilds, h) || h.Counts.Increased != 29 || h.Incomplete || out.State != "matching" {
		t.Fatalf("lost categories: %+v", h)
	}
	for _, o := range h.Outcomes {
		if o.Increased != 1 || o.Unknown != 0 {
			t.Fatalf("lost outcome %s/%s", o.Source, o.Outcome)
		}
	}
	// Domain, wire and clone readers must all retain the actual unknown admission
	// return independently of unavailable or missing counter evidence.
	clone := CloneRuntimeCommandCronOutcomes(h)
	last := clone.Outcomes[28]
	if last.Source != "admission" || last.Outcome != "unknown" || last.Increased != 1 || last.Unknown != 0 {
		t.Fatal("unknown return became missing telemetry")
	}
}
