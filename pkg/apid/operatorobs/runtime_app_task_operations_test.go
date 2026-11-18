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

func addRuntimeAppTaskOperationFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for _, op := range runtimeAppTaskOperationNames() {
				for _, outcome := range runtimeAppTaskOperationOutcomes() {
					if outcome == "capacity" {
						continue
					}
					for _, q := range []string{runtimeAppTaskOperationQuery(job), "timestamp(" + runtimeAppTaskOperationQuery(job) + ")"} {
						n := float64(0)
						if strings.HasPrefix(q, "timestamp(") {
							n = float64(now.Unix())
						}
						row := inputRow(job, up.Metric["instance"], "", n)
						for k, v := range up.Metric {
							row.Metric[k] = v
						}
						row.Metric["obs_metric"] = runtimeAppTaskOperationMetric
						row.Metric["operation"] = op
						row.Metric["outcome"] = outcome
						if !strings.HasPrefix(q, "timestamp(") {
							row.Metric["__name__"] = runtimeAppTaskOperationMetric
						}
						f[q] = append(f[q], row)
					}
				}
			}
		}
	}
}
func changeRuntimeAppTaskOperationFixture(f inputFixture, op, outcome string, n float64) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		q := runtimeAppTaskOperationQuery(job)
		for i, row := range f[q] {
			if row.Metric["operation"] == op && row.Metric["outcome"] == outcome {
				f[q][i].Value[1] = strconv.FormatFloat(n, 'f', -1, 64)
			}
		}
	}
}
func TestRuntimeAppTaskOperationsComparisons(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	for _, fault := range []string{"baseline", "unchanged", "errors", "canceled", "lease_lost", "counter_reset", "fractional", "process_changed", "repeated", "uncertain_window", "mixed_clocks", "previous_missing", "partial", "duplicate", "orphan", "stale", "max_exact"} {
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
				changeRuntimeAppTaskOperationFixture(f, "restore", "error", 100)
			case "errors":
				changeRuntimeAppTaskOperationFixture(f, "restore", "error", 3)
			case "canceled":
				changeRuntimeAppTaskOperationFixture(f, "execute", "canceled", 3)
			case "lease_lost":
				changeRuntimeAppTaskOperationFixture(f, "renew", "lease_lost", 3)
			case "counter_reset":
				changeRuntimeAppTaskOperationFixture(previous, "restore", "error", 3)
				changeRuntimeAppTaskOperationFixture(f, "restore", "error", 1)
			case "fractional":
				changeRuntimeAppTaskOperationFixture(f, "restore", "error", 1.5)
			case "process_changed", "repeated", "uncertain_window":
				changeRuntimeAppTaskOperationFixture(f, "restore", "error", 3)
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
			case "partial":
				changeRuntimeAppTaskOperationFixture(f, "restore", "error", 3)
			case "max_exact":
				changeRuntimeAppTaskOperationFixture(f, "restore", "error", float64(api.ObsRuntimeAppTaskOperationCounterMax))
			}
			for _, job := range []string{"schedd", "schedd-compute"} {
				target := f
				if fault == "previous_missing" {
					target = previous
				}
				for _, q := range []string{runtimeAppTaskOperationQuery(job), "timestamp(" + runtimeAppTaskOperationQuery(job) + ")"} {
					rows := []inputFixtureRow{}
					for _, row := range target[q] {
						if row.Metric["operation"] == "restore" && row.Metric["outcome"] == "success" {
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
			h := out.AppTaskOperations
			if !RuntimeAppTaskOperationsValid(out.ObsRuntimeBuilds, h) {
				t.Fatalf("invalid operation evidence: %+v", h)
			}
			increased := fault == "errors" || fault == "canceled" || fault == "lease_lost" || fault == "partial" || fault == "max_exact"
			if (h.Counts.Increased > 0) != increased || out.State != "matching" {
				t.Fatalf("misleading changes: %+v", h)
			}
			if fault == "baseline" && (h.Counts.Baseline != 60 || h.Counts.Increased != 0) {
				t.Fatal("old counters became new activity")
			}
			if h.Counts.Missing < 10 && fault != "orphan" {
				t.Fatal("missing capacity series became zero")
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "private-provider-error") {
				t.Fatal("raw error leaked")
			}
		})
	}
}
func TestRuntimeAppTaskOperationsProfilesPreviewAndCapacity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, r := runtimeFixture(role, now, 1)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.AppTaskOperations != nil {
				t.Fatal("v1 gained operation reads")
			}
		} else if !RuntimeAppTaskOperationsValid(out.ObsRuntimeBuilds, out.AppTaskOperations) {
			t.Fatal("invalid profile")
		}
	}
	f, s, r := runtimeFixture("control-plane-v2", now, 1)
	addLoopFixture(f, s.role, now)
	for q, rows := range f {
		for i := 1; i < 40; i++ {
			for _, row := range rows {
				if row.Metric["job"] != "schedd" {
					continue
				}
				clone := inputRow("schedd", fmt.Sprintf("private-operation-%d:9100", i), "", 0)
				for k, v := range row.Metric {
					clone.Metric[k] = v
				}
				clone.Metric["instance"] = fmt.Sprintf("private-operation-%d:9100", i)
				clone.Value = row.Value
				f[q] = append(f[q], clone)
			}
		}
	}
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.AppTaskOperations
	if !RuntimeAppTaskOperationsValid(out.ObsRuntimeBuilds, h) || h.Counts.Baseline != 41*30 || h.Counts.Missing != 41*5 || h.OmittedItems < 9 {
		t.Fatal("full signal coverage lost")
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	if len(data) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("API budget exceeded")
	}
	at := now.Add(-30 * time.Second)
	f, s, r = runtimeFixture("single-box-v2", now, 1)
	addLoopFixture(f, s.role, now)
	previous, _, _ := runtimeFixture(s.role, at, 1)
	addLoopFixture(previous, s.role, at)
	s.compareAt = at
	for _, fixture := range []inputFixture{f, previous} {
		for _, q := range []string{runtimeAppTaskOperationQuery("schedd"), "timestamp(" + runtimeAppTaskOperationQuery("schedd") + ")"} {
			rows := fixture[q]
			for _, row := range rows {
				if row.Metric["operation"] == "restore" && row.Metric["outcome"] == "error" {
					clone := inputRow("schedd", row.Metric["instance"], "", 0)
					for k, v := range row.Metric {
						clone.Metric[k] = v
					}
					clone.Metric["outcome"] = "capacity"
					clone.Value = row.Value
					fixture[q] = append(fixture[q], clone)
				}
			}
		}
	}
	for _, spec := range runtimeBuildJobSpecs(s.role) {
		if spec.job == "schedd" {
			q := runtimeProcessQuery(spec)
			for i := range f[q] {
				f[q][i].Value[1] = "160"
			}
		}
	}
	changeRuntimeAppTaskOperationFixture(f, "restore", "capacity", 4)
	out = RuntimeBuildsWithLoopHealth(t.Context(), runtimeProcessClient(t, f, previous, now, at, ""), r, s, now)
	if !RuntimeAppTaskOperationsValid(out.ObsRuntimeBuilds, out.AppTaskOperations) || out.AppTaskOperations.Operations[0].CapacityRefusals != 1 || out.AppTaskOperations.Operations[0].ErrorIncreases != 0 {
		t.Fatal("capacity conflated with errors")
	}
}
