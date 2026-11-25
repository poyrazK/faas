package operatorobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRuntimeBuildProfilesFreezeV1AndTelemetryRoles(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	want := map[string][]string{
		"single-box-v1":    {"apid", "gatewayd", "schedd", "vmmd"},
		"control-plane-v1": {"apid", "gatewayd", "schedd", "gatewayd-internal", "schedd-compute", "vmmd"},
		"single-box-v2":    {"apid", "gatewayd", "schedd", "vmmd", "builderd", "imaged"},
		"control-plane-v2": {"apid", "gatewayd", "schedd", "gatewayd-internal", "schedd-compute", "vmmd", "builderd", "imaged"},
	}
	wantCounts := map[string]int{"single-box-v1": 4, "control-plane-v1": 9, "single-box-v2": 6, "control-plane-v2": 13}
	if len(RuntimeBuildRoles()) != len(want) || api.ObsRuntimeBuildJobsMax != 8 {
		t.Fatal("profile choices or job budget changed")
	}
	for _, role := range RuntimeBuildRoles() {
		t.Run(role, func(t *testing.T) {
			if !RuntimeBuildRoleValid(role) {
				t.Fatal("known profile rejected")
			}
			for i, spec := range runtimeBuildJobSpecs(role) {
				prefix, daemon := spec.job, spec.job
				switch spec.job {
				case "gatewayd":
					prefix, daemon = "gatewayd_public", "gatewayd-public"
				case "gatewayd-internal":
					prefix = "gatewayd"
				case "schedd-compute":
					prefix, daemon = "schedd", "schedd"
				}
				discovery := (role == "control-plane-v1" || role == "control-plane-v2") && i >= 3
				if spec.metric != prefix+"_daemon_build_info" || spec.daemon != daemon || spec.discovery != discovery {
					t.Fatalf("profile selector/inventory changed: %+v", spec)
				}
			}
			f, selected, registry := runtimeFixture(role, now, 2)
			query := "release=" + selected.release + "&after=" + selected.after.Format(time.RFC3339Nano) + "&expected_role=" + role
			if _, err := RuntimeBuildsSelection(query, false, now); err != nil {
				t.Fatal(err)
			}
			out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			jobs := []string{}
			expected := 0
			for _, job := range out.Jobs {
				jobs = append(jobs, job.Job)
				expected += job.Expected
			}
			if !reflect.DeepEqual(jobs, want[role]) || out.State != "matching" || expected != wantCounts[role] || out.Counts.Expected != expected || out.Counts.Matching != expected || !RuntimeBuildsValid(out) || !RuntimeReadinessValid(out) || out.Readiness.Counts.Ready != expected {
				t.Fatalf("profile scope/evidence: %+v", out)
			}
			copySpecs := runtimeBuildJobSpecs(role)
			copySpecs[0].metric = "changed"
			if runtimeBuildJobSpecs(role)[0].metric != "apid_daemon_build_info" {
				t.Fatal("profile callers share mutable definitions")
			}
		})
	}
	if !reflect.DeepEqual(InputHealthRoles(), []string{"single-box-v1", "control-plane-v1"}) || len(inputJobSpecs("single-box-v1")) != 4 || len(inputJobSpecs("control-plane-v1")) != 6 {
		t.Fatal("build profiles expanded telemetry health")
	}
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		if _, err := InputHealthSelection("expected_role="+role, false); err == nil {
			t.Fatal("v2 build profile accepted as telemetry role")
		}
	}
	if RuntimeBuildRoleValid("control-plane") || RuntimeBuildRoleValid("") {
		t.Fatal("implicit profile accepted")
	}
}

func TestRuntimeBuildV2BuildAndImageEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, daemon := range []string{"builderd", "imaged"} {
			for _, failure := range []string{"missing", "stale", "mismatched", "conflicting"} {
				t.Run(role+"/"+daemon+"/"+failure, func(t *testing.T) {
					f, selected, registry := runtimeFixture(role, now, 2)
					query := fmt.Sprintf(`%s_daemon_build_info{job=%q,daemon=%q}`, daemon, daemon, daemon)
					times := "timestamp(" + query + ")"
					switch failure {
					case "missing":
						f[query], f[times] = nil, nil
					case "stale":
						f[times][0].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
					case "mismatched":
						for _, q := range []string{query, times} {
							f[q][0].Metric["git_sha"] = strings.Repeat("b", 40)
						}
					case "conflicting":
						for _, q := range []string{query, times} {
							row := f[q][0]
							labels := map[string]string{}
							for key, value := range row.Metric {
								labels[key] = value
							}
							labels["git_sha"] = strings.Repeat("b", 40)
							row.Metric = labels
							f[q] = append(f[q], row)
						}
					}
					out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
					if !RuntimeBuildsValid(out) || out.State == "matching" {
						t.Fatalf("ignored %s identity failure: %+v", daemon, out)
					}
					n := 1
					if role == "control-plane-v2" {
						n = 2
					}
					switch failure {
					case "missing":
						if out.Counts.Missing != n {
							t.Fatalf("missing count: %+v", out.Counts)
						}
					case "stale":
						if out.Counts.Stale != 1 {
							t.Fatalf("stale count: %+v", out.Counts)
						}
					case "mismatched":
						if out.State != "mixed" || out.Counts.Mismatched != 1 || out.Counts.DistinctRevisions != 2 {
							t.Fatalf("revision count: %+v", out.Counts)
						}
					case "conflicting":
						if out.Counts.Unavailable != 1 || out.Counts.DistinctRevisions != 1 {
							t.Fatalf("conflicting count: %+v", out.Counts)
						}
					}
					body, _ := json.Marshal(out)
					if strings.Contains(string(body), "private-host") || strings.Contains(string(body), "private-build-time") {
						t.Fatal("raw source labels leaked")
					}
				})
			}
		}
	}
}

func TestRuntimeBuildV2ExactComputeInventoryBeyondPreview(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("control-plane-v2", now, 50)
	for query, rows := range f {
		if strings.Contains(query, `job="builderd"`) {
			rows[49].Metric["node"], rows[49].Metric["node_id"] = "replacement", "replacement-id"
		}
		if strings.Contains(query, `job="imaged"`) && strings.Contains(query, "build_info") {
			rows[49].Metric["git_sha"] = strings.Repeat("b", 40)
		}
	}
	out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeBuildsValid(out) || out.State != "mixed" || out.Counts.Expected != 253 || out.Counts.Observed != 253 || out.Counts.Missing != 1 || out.Counts.Unexpected != 1 || out.Counts.Mismatched != 1 || out.OmittedItems != 222 || len(out.Items) != api.ObsRuntimeBuildPreviewMax {
		t.Fatalf("truncated/new member evidence: %+v", out)
	}
	registry.err = errors.New("private database failure")
	out = RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeBuildsValid(out) || out.State != "incomplete" {
		t.Fatal("unavailable registry qualified")
	}
	for _, job := range out.Jobs[3:] {
		if job.State != "unknown" || job.Reason != "inventory_unavailable" {
			t.Fatalf("unavailable compute source: %+v", job)
		}
	}
	f, selected, registry = runtimeFixture("control-plane-v2", now, 0)
	out = RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeBuildsValid(out) || out.State != "matching" || out.Counts.Expected != 3 || out.Counts.Observed != 3 || len(out.Jobs) != 8 {
		t.Fatalf("fresh empty registry: %+v", out)
	}
}
