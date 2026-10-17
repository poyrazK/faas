package operatorobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

type inputFixtureRow struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}
type inputFixture map[string][]inputFixtureRow

func inputRow(job, instance, daemon string, value float64) inputFixtureRow {
	labels := map[string]string{"job": job, "instance": instance}
	if daemon != "" {
		labels["daemon"] = daemon
	}
	return inputFixtureRow{labels, [2]any{float64(1), strconv.FormatFloat(value, 'f', -1, 64)}}
}

func inputFixtureFor(role string, now time.Time) inputFixture {
	f := inputFixture{}
	for _, spec := range inputJobSpecs(role) {
		up := fmt.Sprintf(`up{job=%q}`, spec.job)
		f[up] = []inputFixtureRow{inputRow(spec.job, "private:9100", "", 1)}
		f["timestamp("+up+")"] = []inputFixtureRow{inputRow(spec.job, "private:9100", "", float64(now.Unix()))}
		f[inputMetricQuery(spec)] = []inputFixtureRow{inputRow(spec.job, "private:9100", spec.daemon, float64(now.Unix()))}
		if spec.discovery {
			f.discovery(spec.job, now, 1, 0)
		}
	}
	return f
}

func (f inputFixture) discovery(job string, now time.Time, targets, invalid int) {
	for name, value := range map[string]float64{"targets": float64(targets), "registry_nodes": float64(targets + invalid), "invalid_targets": float64(invalid), "last_success_timestamp_seconds": float64(now.Unix())} {
		q := fmt.Sprintf(`apid_metrics_discovery_%s{job="apid",exported_job=%q}`, name, job)
		row := inputRow("apid", "private:9101", "", value)
		row.Metric["exported_job"] = job
		f[q] = []inputFixtureRow{row}
		row = inputRow("apid", "private:9101", "", float64(now.Unix()))
		row.Metric["exported_job"] = job
		f["timestamp("+q+")"] = []inputFixtureRow{row}
	}
}

func inputFixtureClient(t *testing.T, f inputFixture, now time.Time) *promql.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/query" || r.URL.Query().Get("time") != now.UTC().Format(time.RFC3339Nano) {
			t.Errorf("unpinned request: %s %s", r.Method, r.URL)
		}
		q := r.URL.Query().Get("query")
		rows, exists := f[q]
		if !exists {
			t.Errorf("unexpected query: %s", q)
		}
		if rows == nil {
			rows = []inputFixtureRow{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}})
	}))
	t.Cleanup(srv.Close)
	return promql.NewClient(srv.URL, srv.Client())
}

func TestInputHealthRolesSelectionAndIdle(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range InputHealthRoles() {
		t.Run(role, func(t *testing.T) {
			f := inputFixtureFor(role, now)
			out := InputHealth(t.Context(), inputFixtureClient(t, f, now), role, now)
			if out.State != "healthy" || len(out.Jobs) != len(inputJobSpecs(role)) {
				t.Fatalf("healthy: %+v", out)
			}
			for _, j := range out.Jobs {
				if j.Metrics[0].Counts.Fresh != 1 || j.Targets.Counts.FreshUp != 1 {
					t.Fatalf("missing evidence: %+v", j)
				}
			}
			if role == "control-plane-v1" {
				for _, spec := range inputJobSpecs(role)[3:] {
					f.discovery(spec.job, now, 0, 0)
					f[fmt.Sprintf(`up{job=%q}`, spec.job)] = nil
					f[fmt.Sprintf(`timestamp(up{job=%q})`, spec.job)] = nil
				}
				out = InputHealth(t.Context(), inputFixtureClient(t, f, now), role, now)
				if out.State != "healthy" {
					t.Fatalf("idle registry: %+v", out)
				}
				for _, j := range out.Jobs[3:] {
					if j.Metrics[0].State != "not_applicable" || *j.Expectation.MinimumTargets != 0 {
						t.Fatalf("idle job: %+v", j)
					}
				}
			}
		})
	}
	if out := InputHealth(t.Context(), promql.NewClient("http://unused.invalid", nil), "", now); out.State != "unknown" || out.Reason != "expected_role_not_selected" || len(out.Jobs) != 0 {
		t.Fatalf("unselected: %+v", out)
	}
	for _, raw := range []string{"expected_role=", "expected_role=compute", "expected_role=single-box-v1&expected_role=single-box-v1", "expected_role=control-plane-v1&url=private", "limit=1", "&", "expected_role=%zz"} {
		if _, err := InputHealthSelection(raw, false); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := InputHealthSelection("", true); err == nil {
		t.Fatal("accepted bare query")
	}
	for _, role := range InputHealthRoles() {
		if got, err := InputHealthSelection("expected_role="+role, false); err != nil || got != role {
			t.Fatal(got, err)
		}
	}
}

func TestInputHealthTargetAndMetricGaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	spec := inputJobSpecs("single-box-v1")[0]
	up, ts := `up{job="apid"}`, `timestamp(up{job="apid"})`
	for _, tc := range []struct {
		name                                             string
		mutate                                           func(inputFixture)
		target, metric                                   string
		down, stale, missing, metricMissing, metricStale int
	}{
		{"missing target", func(f inputFixture) { f[up] = nil; f[ts] = nil }, "degraded", "unknown", 0, 0, 1, 0, 0},
		{"failed scrape", func(f inputFixture) { f[up][0].Value[1] = "0" }, "degraded", "unknown", 1, 0, 0, 0, 0},
		{"stale scrape", func(f inputFixture) { f[ts][0].Value[1] = strconv.FormatInt(now.Add(-121*time.Second).Unix(), 10) }, "degraded", "unknown", 0, 1, 0, 0, 0},
		{"missing gauge", func(f inputFixture) { f[inputMetricQuery(spec)] = nil }, "healthy", "degraded", 0, 0, 0, 1, 0},
		{"stale gauge", func(f inputFixture) {
			f[inputMetricQuery(spec)][0].Value[1] = strconv.FormatInt(now.Add(-121*time.Second).Unix(), 10)
		}, "healthy", "degraded", 0, 0, 0, 0, 1},
		{"sibling cannot hide absence", func(f inputFixture) {
			f[up] = append(f[up], inputRow("apid", "second:9100", "", 1))
			f[ts] = append(f[ts], inputRow("apid", "second:9100", "", float64(now.Unix())))
		}, "healthy", "degraded", 0, 0, 0, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := inputFixtureFor("single-box-v1", now)
			tc.mutate(f)
			j := InputHealth(t.Context(), inputFixtureClient(t, f, now), "single-box-v1", now).Jobs[0]
			if j.State != "degraded" || j.Targets.State != tc.target || j.Metrics[0].State != tc.metric {
				t.Fatalf("gap: %+v", j)
			}
			c := j.Targets.Counts
			if c.Down != tc.down || c.Stale != tc.stale || *c.MissingMinimum != tc.missing {
				t.Fatalf("targets: %+v", c)
			}
			if m := j.Metrics[0].Counts; m != nil && (m.Missing != tc.metricMissing || m.Stale != tc.metricStale) {
				t.Fatalf("metrics: %+v", m)
			}
		})
	}
}

func TestInputHealthRejectsInvalidEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	spec := inputJobSpecs("single-box-v1")[0]
	for _, tc := range []struct {
		name   string
		mutate func(inputFixture)
		part   string
	}{
		{"duplicate targets", func(f inputFixture) { q := `up{job="apid"}`; f[q] = append(f[q], f[q][0]) }, "targets"},
		{"unmatched timestamp", func(f inputFixture) { f[`timestamp(up{job="apid"})`][0].Metric["instance"] = "other" }, "targets"},
		{"future target", func(f inputFixture) {
			f[`timestamp(up{job="apid"})`][0].Value[1] = strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
		}, "targets"},
		{"nonboolean up", func(f inputFixture) { f[`up{job="apid"}`][0].Value[1] = "2" }, "targets"},
		{"missing identity", func(f inputFixture) { delete(f[`up{job="apid"}`][0].Metric, "instance") }, "targets"},
		{"wrong job", func(f inputFixture) { f[`up{job="apid"}`][0].Metric["job"] = "private" }, "targets"},
		{"duplicate gauge", func(f inputFixture) { q := inputMetricQuery(spec); f[q] = append(f[q], f[q][0]) }, "metric"},
		{"wrong daemon", func(f inputFixture) { f[inputMetricQuery(spec)][0].Metric["daemon"] = "other" }, "metric"},
		{"future gauge", func(f inputFixture) {
			f[inputMetricQuery(spec)][0].Value[1] = strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
		}, "metric"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := inputFixtureFor("single-box-v1", now)
			tc.mutate(f)
			j := InputHealth(t.Context(), inputFixtureClient(t, f, now), "single-box-v1", now).Jobs[0]
			if tc.part == "targets" {
				if j.Targets.State != "unknown" || j.Targets.Counts != nil || j.Metrics[0].Reason != "target_evidence_unavailable" {
					t.Fatalf("targets accepted: %+v", j)
				}
			} else if j.Metrics[0].State != "unknown" || j.Metrics[0].Counts != nil {
				t.Fatalf("metric accepted: %+v", j)
			}
		})
	}
}

func TestInputHealthDiscoveryContract(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	q := `apid_metrics_discovery_targets{job="apid",exported_job="vmmd"}`
	last := `apid_metrics_discovery_last_success_timestamp_seconds{job="apid",exported_job="vmmd"}`
	for _, tc := range []struct {
		name          string
		mutate        func(inputFixture)
		state, reason string
	}{
		{"missing", func(f inputFixture) { f[q] = nil }, "unknown", "discovery_missing"},
		{"multiple producers", func(f inputFixture) { f[q] = append(f[q], f[q][0]) }, "unknown", "discovery_invalid"},
		{"wrong collision mapping", func(f inputFixture) { f[q][0].Metric["exported_job"] = "private" }, "unknown", "discovery_invalid"},
		{"mixed producers", func(f inputFixture) { f[last][0].Metric["instance"] = "other" }, "unknown", "discovery_invalid"},
		{"stale scrape", func(f inputFixture) {
			f["timestamp("+q+")"][0].Value[1] = strconv.FormatInt(now.Add(-121*time.Second).Unix(), 10)
		}, "unknown", "discovery_stale"},
		{"stale refresh", func(f inputFixture) { f[last][0].Value[1] = strconv.FormatInt(now.Add(-121*time.Second).Unix(), 10) }, "unknown", "discovery_stale"},
		{"boot zero", func(f inputFixture) { f[last][0].Value[1] = "0" }, "unknown", "discovery_stale"},
		{"fraction", func(f inputFixture) { f[q][0].Value[1] = "0.5" }, "unknown", "discovery_invalid"},
		{"overflow", func(f inputFixture) { f[q][0].Value[1] = "4097" }, "unknown", "discovery_invalid"},
		{"inconsistent counts", func(f inputFixture) { f[q][0].Value[1] = "2" }, "unknown", "discovery_invalid"},
		{"omitted invalid", func(f inputFixture) { f.discovery("vmmd", now, 1, 1) }, "degraded", "discovery_invalid_targets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := inputFixtureFor("control-plane-v1", now)
			tc.mutate(f)
			out := InputHealth(t.Context(), inputFixtureClient(t, f, now), "control-plane-v1", now)
			e := out.Jobs[5].Expectation
			if e.State != tc.state || e.Reason != tc.reason {
				t.Fatalf("expectation: %+v", e)
			}
			if e.State == "unknown" && (e.MinimumTargets != nil || e.InvalidTargets != nil || e.ObservedAt != nil || out.State != "partial") {
				t.Fatalf("unknown invented counts: %+v", out)
			}
		})
	}
	f := inputFixtureFor("control-plane-v1", now)
	f.discovery("vmmd", now, 2, 0)
	out := InputHealth(t.Context(), inputFixtureClient(t, f, now), "control-plane-v1", now)
	if *out.Jobs[5].Targets.Counts.MissingMinimum != 1 || out.State != "degraded" {
		t.Fatalf("missing discovered target: %+v", out.Jobs[5])
	}
	f.discovery("vmmd", now, 0, 0)
	out = InputHealth(t.Context(), inputFixtureClient(t, f, now), "control-plane-v1", now)
	if *out.Jobs[5].Targets.Counts.Extra != 1 || out.Jobs[5].Targets.State != "partial" {
		t.Fatalf("unexpected target: %+v", out.Jobs[5])
	}
}

func TestInputHealthFailurePrivacyAndCancellation(t *testing.T) {
	now := time.Now().UTC()
	for _, body := range []string{"private upstream failure", `{"status":"success","warnings":["private"],"data":{"resultType":"vector","result":[]}}`, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"NaN"]}]}}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		out := InputHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), "control-plane-v1", now)
		srv.Close()
		data, _ := json.Marshal(out)
		if out.State != "partial" || strings.Contains(string(data), "private") || strings.Contains(string(data), srv.URL) {
			t.Fatalf("failed source: %s", data)
		}
		for _, j := range out.Jobs {
			if j.Targets.Counts != nil {
				t.Fatal("failed source became zero")
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out := InputHealth(ctx, nil, "control-plane-v1", now)
	if out.Jobs[5].Expectation.State != "unknown" || out.Jobs[0].Targets.State != "unknown" {
		t.Fatalf("canceled: %+v", out)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	out = InputHealth(ctx, promql.NewClient(srv.URL, srv.Client()), "control-plane-v1", now)
	if time.Since(start) > time.Second || out.Jobs[5].Targets.Counts != nil {
		t.Fatal("shared cancellation failed")
	}
	if !reflect.DeepEqual(InputHealthRoles(), []string{"single-box-v1", "control-plane-v1"}) || api.ObsInputHealthJobsMax < len(inputJobSpecs("control-plane-v1")) {
		t.Fatal("unbounded inventory")
	}
}
