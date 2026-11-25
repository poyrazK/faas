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

func addRuntimeAppTaskResultFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for _, d := range runtimeAppTaskResultDimensions() {
				metric, _ := runtimeAppTaskResultMetric(d)
				for _, q := range []string{runtimeAppTaskResultQuery(job), "timestamp(" + runtimeAppTaskResultQuery(job) + ")"} {
					n := float64(0)
					if strings.HasPrefix(q, "timestamp(") {
						n = float64(now.Unix())
					}
					row := inputRow(job, up.Metric["instance"], "", n)
					for k, v := range up.Metric {
						row.Metric[k] = v
					}
					row.Metric["obs_metric"] = metric
					if d.Source == "completion" {
						row.Metric["kind"], row.Metric["status"] = d.Kind, d.Outcome
					} else {
						row.Metric["outcome"] = d.Outcome
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
func changeRuntimeAppTaskResultFixture(f inputFixture, kind, outcome string, n float64) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		q := runtimeAppTaskResultQuery(job)
		for i, row := range f[q] {
			if row.Metric["kind"] == kind && (row.Metric["status"] == outcome || row.Metric["outcome"] == outcome) {
				f[q][i].Value[1] = strconv.FormatFloat(n, 'f', -1, 64)
			}
		}
	}
}
func TestRuntimeAppTaskResultsComparisons(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	for _, fault := range []string{"baseline", "unchanged", "failed", "cancelled", "failed_runs", "timed_out", "queued", "requeued_restores", "recovery_cancelled", "counter_reset", "fractional", "process_changed", "repeated", "uncertain_window", "mixed_clocks", "previous_missing", "partial", "partial_recovery", "duplicate", "orphan", "stale", "max_exact"} {
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
				changeRuntimeAppTaskResultFixture(f, "release", "failed", 100)
			case "failed":
				changeRuntimeAppTaskResultFixture(f, "release", "failed", 3)
			case "cancelled":
				changeRuntimeAppTaskResultFixture(f, "manual", "cancelled", 3)
			case "failed_runs":
				changeRuntimeAppTaskResultFixture(f, "", "failed_runs", 3)
			case "timed_out", "queued":
				changeRuntimeAppTaskResultFixture(f, "cron", fault, 3)
			case "requeued_restores":
				changeRuntimeAppTaskResultFixture(f, "", fault, 3)
			case "recovery_cancelled":
				changeRuntimeAppTaskResultFixture(f, "", "cancelled", 3)
			case "counter_reset":
				changeRuntimeAppTaskResultFixture(previous, "release", "failed", 3)
				changeRuntimeAppTaskResultFixture(f, "release", "failed", 1)
			case "fractional":
				changeRuntimeAppTaskResultFixture(f, "release", "failed", 1.5)
			case "process_changed", "repeated", "uncertain_window":
				changeRuntimeAppTaskResultFixture(f, "release", "failed", 3)
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
			case "partial_recovery":
				changeRuntimeAppTaskResultFixture(f, "cron", "queued", 3)
			case "partial":
				changeRuntimeAppTaskResultFixture(f, "release", "failed", 3)
			case "max_exact":
				changeRuntimeAppTaskResultFixture(f, "release", "failed", float64(api.ObsRuntimeAppTaskOperationCounterMax))
			}
			for _, job := range []string{"schedd", "schedd-compute"} {
				target := f
				if fault == "previous_missing" {
					target = previous
				}
				for _, q := range []string{runtimeAppTaskResultQuery(job), "timestamp(" + runtimeAppTaskResultQuery(job) + ")"} {
					rows := []inputFixtureRow{}
					for _, row := range target[q] {
						if fault == "partial_recovery" && row.Metric["outcome"] == "requeued_restores" {
							continue
						}
						if row.Metric["kind"] == "release" && row.Metric["status"] == "succeeded" {
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
			h := out.AppTaskResults
			if !RuntimeAppTaskResultsValid(out.ObsRuntimeBuilds, h) {
				t.Fatalf("invalid operation evidence: %+v", h)
			}
			increased := fault == "partial_recovery" || fault == "timed_out" || fault == "queued" || fault == "requeued_restores" || fault == "recovery_cancelled" || fault == "failed" || fault == "cancelled" || fault == "failed_runs" || fault == "partial" || fault == "max_exact"
			if (h.Counts.Increased > 0) != increased || out.State != "matching" {
				t.Fatalf("misleading changes: %+v", h)
			}
			if fault == "baseline" && (h.Counts.Baseline != 36 || h.Counts.Increased != 0) {
				t.Fatal("old counters became new activity")
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "private-provider-error") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestRuntimeAppTaskResultsProfilesPreviewAndPartialRecovery(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, selected, registry := runtimeFixture(role, now, 1)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
		if strings.HasSuffix(role, "v1") {
			if out.AppTaskResults != nil {
				t.Fatal("legacy profile gained result evidence")
			}
		} else if !RuntimeAppTaskResultsValid(out.ObsRuntimeBuilds, out.AppTaskResults) {
			t.Fatal("invalid profile result evidence")
		}
	}
	f, selected, registry := runtimeFixture("control-plane-v2", now, 40)
	addLoopFixture(f, selected.role, now)
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	h := out.AppTaskResults
	if !RuntimeAppTaskResultsValid(out.ObsRuntimeBuilds, h) || h.Counts.Baseline != 41*18 || h.OmittedItems < 9 {
		t.Fatalf("lost full-set coverage: %+v", h)
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("API response exceeded bound")
	}
}
