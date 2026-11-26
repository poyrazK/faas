package operatorobs

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func addRuntimeHTTPDeliveryFixture(f inputFixture, now time.Time) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, job)] {
			for _, d := range runtimeHTTPCronDeliveryDimensions() {
				metric, _ := runtimeHTTPCronDeliveryMetric(d)
				for _, q := range []string{runtimeHTTPCronDeliveryQuery(job), "timestamp(" + runtimeHTTPCronDeliveryQuery(job) + ")"} {
					value := float64(0)
					if strings.HasPrefix(q, "timestamp(") {
						value = float64(now.Unix())
					}
					row := inputRow(job, up.Metric["instance"], "", value)
					for k, v := range up.Metric {
						row.Metric[k] = v
					}
					row.Metric["obs_metric"], row.Metric["outcome"] = metric, d.Outcome
					if !strings.HasPrefix(q, "timestamp(") {
						row.Metric["__name__"] = metric
					}
					f[q] = append(f[q], row)
				}
			}
		}
	}
}
func changeRuntimeHTTPDelivery(f inputFixture, outcome, value string) {
	for _, job := range []string{"schedd", "schedd-compute"} {
		q := runtimeHTTPCronDeliveryQuery(job)
		for i, row := range f[q] {
			if row.Metric["outcome"] == outcome {
				f[q][i].Value[1] = value
			}
		}
	}
}
func TestRuntimeHTTPCronDeliveryComparisons(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	for _, fault := range []string{"baseline", "quiet", "success", "retry", "app_error", "failed", "dead_letter", "partial", "previous_missing", "reset", "fractional", "negative", "max_exact", "over_max", "duplicate", "orphan", "stale", "mixed_clock", "process_changed", "uncertain_window", "repeated", "source_error"} {
		t.Run(fault, func(t *testing.T) {
			f, s, r := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(f, s.role, now)
			prior, _, _ := runtimeFixture(s.role, at, 1)
			addLoopFixture(prior, s.role, at)
			s.compareAt = at
			for _, spec := range runtimeBuildJobSpecs(s.role) {
				if spec.daemon == "schedd" {
					for i := range f[runtimeProcessQuery(spec)] {
						f[runtimeProcessQuery(spec)][i].Value[1] = "160"
					}
				}
			}
			changeRuntimeHTTPDelivery(f, "retry", "3")
			fail := ""
			switch fault {
			case "baseline":
				s.compareAt = time.Time{}
			case "quiet":
				changeRuntimeHTTPDelivery(f, "retry", "0")
			case "success", "retry", "app_error", "failed", "dead_letter":
				changeRuntimeHTTPDelivery(f, "retry", "0")
				changeRuntimeHTTPDelivery(f, fault, "3")
			case "reset":
				changeRuntimeHTTPDelivery(prior, "retry", "7")
			case "fractional":
				changeRuntimeHTTPDelivery(f, "retry", "0.5")
			case "negative":
				changeRuntimeHTTPDelivery(f, "retry", "-1")
			case "max_exact":
				changeRuntimeHTTPDelivery(f, "retry", "9007199254740991")
			case "over_max":
				changeRuntimeHTTPDelivery(f, "retry", "9007199254740992")
			case "source_error":
				fail = runtimeHTTPCronDeliveryQuery("schedd")
			case "partial", "previous_missing", "duplicate", "orphan", "stale", "mixed_clock":
				target := f
				if fault == "previous_missing" {
					target = prior
				}
				for _, job := range []string{"schedd", "schedd-compute"} {
					for _, q := range []string{runtimeHTTPCronDeliveryQuery(job), "timestamp(" + runtimeHTTPCronDeliveryQuery(job) + ")"} {
						rows := []inputFixtureRow{}
						for _, row := range target[q] {
							if row.Metric["outcome"] == "success" {
								switch fault {
								case "partial", "previous_missing":
									continue
								case "duplicate":
									rows = append(rows, row)
								case "orphan":
									row.Metric["private_error"] = "private-provider-error"
								case "stale":
									if strings.HasPrefix(q, "timestamp(") {
										row.Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
									}
								case "mixed_clock":
									if strings.HasPrefix(q, "timestamp(") {
										row.Value[1] = fmt.Sprint(now.Add(-time.Second).Unix())
									}
								}
							}
							rows = append(rows, row)
						}
						target[q] = rows
					}
				}
			case "process_changed", "uncertain_window", "repeated":
				for _, spec := range runtimeBuildJobSpecs(s.role) {
					if spec.daemon != "schedd" {
						continue
					}
					q := runtimeProcessQuery(spec)
					for i := range f[q] {
						f[q][i].Value[1] = "130"
						if fault == "process_changed" {
							f[q][i].Value[1] = "5"
						}
					}
					if fault == "repeated" {
						for i := range f["timestamp("+q+")"] {
							f["timestamp("+q+")"][i].Value[1] = fmt.Sprint(at.Unix())
						}
					}
				}
			}
			client := runtimeProcessClient(t, f, prior, now, at, fail)
			base, results := collectRuntimeBuilds(t.Context(), client, r, s, now)
			rows := make([][]RuntimeHTTPCronDeliveryItem, len(results))
			for i, result := range results {
				if result.job.Daemon == "schedd" {
					rows[i] = readRuntimeHTTPCronDeliveryJob(t.Context(), client, result, s, now)
				}
			}
			h := aggregateRuntimeHTTPCronDelivery(base, rows)
			if !RuntimeHTTPCronDeliveryValid(base, h) || base.State != "matching" {
				t.Fatalf("invalid or gating delivery evidence: %+v", h)
			}
			switch fault {
			case "baseline":
				if h.Counts.Baseline != 10 || h.Counts.Increased != 0 {
					t.Fatal("baseline counted as new attempts")
				}
			case "quiet":
				if h.Counts.Unchanged != 10 || h.Incomplete {
					t.Fatal("quiet observations lost")
				}
			case "success", "retry", "app_error", "failed", "dead_letter", "max_exact":
				if h.Counts.Increased != 2 || h.Incomplete {
					t.Fatal("outcome lost or conflated")
				}
				for _, item := range h.Items {
					for _, signal := range item.Signals {
						if signal.State == "increased" {
							want := int64(3)
							if fault == "max_exact" {
								want = api.ObsRuntimeAppTaskOperationCounterMax
							}
							if signal.Delta == nil || *signal.Delta != want {
								t.Fatal("lost exact attempt difference")
							}
						}
					}
				}
			case "partial", "previous_missing", "duplicate", "stale", "mixed_clock":
				if h.Counts.Increased != 2 || !h.Incomplete || h.Outcomes[1].Increased != 2 || h.Outcomes[0].Unknown != 2 {
					t.Fatal("sibling retry evidence lost across gap")
				}
			case "source_error":
				if h.Counts.Increased != 1 || !h.Incomplete {
					t.Fatal("source loss erased sibling process")
				}
			default:
				if h.Counts.Increased != 0 || !h.Incomplete {
					t.Fatal("unsafe difference established")
				}
			}
			body, _ := json.Marshal(h)
			var decoded RuntimeHTTPCronDelivery
			if json.Unmarshal(body, &decoded) != nil || !RuntimeHTTPCronDeliveryValid(base, &decoded) || CloneRuntimeHTTPCronDelivery(h) == nil || strings.Contains(string(body), "private-") {
				t.Fatal("wire evidence invalid or private source leaked")
			}
		})
	}
}
func TestRuntimeHTTPCronDeliveryProfilesAndBounds(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range RuntimeBuildRoles() {
		f, s, r := runtimeFixture(role, now, 1)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
		if strings.HasSuffix(role, "v1") {
			if out.HTTPCronDelivery != nil {
				t.Fatal("legacy profile gained evidence")
			}
		} else if !RuntimeHTTPCronDeliveryValid(out.ObsRuntimeBuilds, out.HTTPCronDelivery) {
			t.Fatal("invalid delivery profile")
		}
	}
	f, s, r := runtimeFixture("control-plane-v2", now, 40)
	addLoopFixture(f, s.role, now)
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
	h := out.HTTPCronDelivery
	if !RuntimeHTTPCronDeliveryValid(out.ObsRuntimeBuilds, h) || h.Counts.Baseline != 41*5 || h.OmittedItems < 9 {
		t.Fatal("lost full-set coverage")
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("response exceeds existing cap")
	}
}
