package operatorobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func runtimeProcessQuery(spec runtimeBuildJobSpec) string {
	return fmt.Sprintf(`%s_uptime_seconds{job=%q,daemon=%q}`, strings.TrimSuffix(spec.metric, "_build_info"), spec.job, spec.daemon)
}

func addRuntimeProcessFixture(f inputFixture, role string, now time.Time) {
	for _, spec := range runtimeBuildJobSpecs(role) {
		query := runtimeProcessQuery(spec)
		for _, q := range []string{query, "timestamp(" + query + ")"} {
			f[q] = []inputFixtureRow{}
			for _, up := range f[fmt.Sprintf(`up{job=%q}`, spec.job)] {
				value := float64(130)
				if strings.HasPrefix(q, "timestamp(") {
					value = float64(now.Unix())
				}
				row := inputRow(spec.job, up.Metric["instance"], spec.daemon, value)
				for k, v := range up.Metric {
					row.Metric[k] = v
				}
				row.Metric["daemon"] = spec.daemon
				f[q] = append(f[q], row)
			}
		}
	}
}

func runtimeProcessClient(t *testing.T, current, previous inputFixture, now, at time.Time, fail string) *promql.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		clock := r.URL.Query().Get("time")
		f := current
		if clock == at.Format(time.RFC3339Nano) {
			f = previous
		} else if clock != now.Format(time.RFC3339Nano) {
			t.Errorf("unpinned process read: %s", clock)
		}
		if fail != "" && query == fail {
			http.Error(w, "private-process-error", http.StatusServiceUnavailable)
			return
		}
		rows, ok := f[query]
		if !ok {
			t.Errorf("unexpected process query: %s", query)
		}
		if rows == nil {
			rows = []inputFixtureRow{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}})
	}))
	t.Cleanup(server.Close)
	return promql.NewClient(server.URL, server.Client())
}

func TestRuntimeProcessUptimeComparisonAndClocks(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	for _, tc := range []struct {
		name, state, comparison string
		change                  func(inputFixture, inputFixture, string)
	}{
		{"growth", "unchanged", "unchanged", func(inputFixture, inputFixture, string) {}},
		{"same_revision_restart", "changed", "changed", func(c, p inputFixture, q string) { c[q][0].Value[1] = "5" }},
		{"different_revision_restart", "changed", "changed", func(c, p inputFixture, q string) {
			c[q][0].Value[1] = "5"
			for _, key := range []string{runtimeBuildQuery(), "timestamp(" + runtimeBuildQuery() + ")"} {
				p[key][0].Metric["git_sha"] = strings.Repeat("b", 40)
			}
		}},
		{"unknown_old_revision", "changed", "changed", func(c, p inputFixture, q string) {
			c[q][0].Value[1] = "5"
			p[runtimeBuildQuery()] = nil
			p["timestamp("+runtimeBuildQuery()+")"] = nil
		}},
		{"missing", "incomplete", "", func(c, p inputFixture, q string) { c[q] = nil; c["timestamp("+q+")"] = nil }},
		{"stale", "incomplete", "", func(c, p inputFixture, q string) {
			c["timestamp("+q+")"][0].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
		}},
		{"before_boundary", "incomplete", "", func(c, p inputFixture, q string) {
			c["timestamp("+q+")"][0].Value[1] = fmt.Sprint(now.Add(-100 * time.Second).Unix())
		}},
		{"future", "incomplete", "", func(c, p inputFixture, q string) {
			c["timestamp("+q+")"][0].Value[1] = fmt.Sprint(now.Add(time.Hour).Unix())
		}},
		{"negative", "incomplete", "", func(c, p inputFixture, q string) { c[q][0].Value[1] = "-1" }},
		{"too_large", "incomplete", "", func(c, p inputFixture, q string) {
			c[q][0].Value[1] = fmt.Sprint(api.ObsRuntimeProcessUptimeMax.Seconds() + 1)
		}},
		{"repeated", "incomplete", "repeated_sample", func(c, p inputFixture, q string) {
			c[q][0].Value[1] = "100"
			c["timestamp("+q+")"][0].Value[1] = fmt.Sprint(at.Unix())
		}},
		{"sample_clock_regressed", "incomplete", "unavailable", func(c, p inputFixture, q string) {
			c["timestamp("+q+")"][0].Value[1] = fmt.Sprint(at.Add(-time.Second).Unix())
		}},
		{"start_estimate_regressed", "incomplete", "unavailable", func(c, p inputFixture, q string) { c[q][0].Value[1] = "200" }},
		{"bounded_clock_skew", "unchanged", "unchanged", func(c, p inputFixture, q string) { c[q][0].Value[1] = "150" }},
		{"previous_uptime_missing", "incomplete", "unavailable", func(c, p inputFixture, q string) { p[q] = nil; p["timestamp("+q+")"] = nil }},
		{"orphan_clock", "incomplete", "", func(c, p inputFixture, q string) { c["timestamp("+q+")"][0].Metric["extra"] = "private-process-error" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current, selected, registry := runtimeFixture("single-box-v2", now, 0)
			prior, _, _ := runtimeFixture(selected.role, at, 0)
			selected.compareAt = at
			q := runtimeProcessQuery(runtimeBuildJobSpecs(selected.role)[0])
			for query := range prior {
				if strings.Contains(query, "uptime_seconds") && !strings.HasPrefix(query, "timestamp(") {
					for i := range prior[query] {
						prior[query][i].Value[1] = "100"
					}
				}
			}
			tc.change(current, prior, q)
			out := RuntimeBuilds(t.Context(), runtimeProcessClient(t, current, prior, now, at, ""), registry, selected, now)
			if !RuntimeBuildsValid(out) || !RuntimeReadinessValid(out) || !RuntimeProcessValid(out) || out.State != "matching" || out.Process.State != tc.state || out.Items[0].Process.Comparison != tc.comparison {
				t.Fatalf("unexpected process evidence: %+v item=%+v", out.Process, out.Items[0].Process)
			}
			if tc.comparison == "changed" {
				relation := "same"
				if tc.name == "different_revision_restart" {
					relation = "different"
				}
				if tc.name == "unknown_old_revision" {
					relation = "unknown"
				}
				if out.Items[0].Process.RevisionRelation != relation {
					t.Fatalf("wrong revision classification: %+v", out.Items[0].Process)
				}
			}
			if strings.Contains(tc.name, "revision_restart") && out.Process.Counts.Changed != 1 {
				t.Fatal("process change lost")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-process-error") {
				t.Fatal("raw process data leaked")
			}
		})
	}
}

func TestRuntimeProcessComparisonBeyondPreviewAndMembership(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-90 * time.Second)
	current, selected, registry := runtimeFixture("control-plane-v2", now, 40)
	prior, _, _ := runtimeFixture(selected.role, at, 40)
	selected.after = now.Add(-5 * time.Minute)
	selected.compareAt = at
	for q := range prior {
		if strings.Contains(q, "uptime_seconds") && !strings.HasPrefix(q, "timestamp(") {
			for i := range prior[q] {
				prior[q][i].Value[1] = "40"
			}
		}
	}
	// Start estimate shifts forward even though new uptime exceeds old uptime.
	query := runtimeProcessQuery(runtimeBuildJobSpecs(selected.role)[7])
	current[query][39].Value[1] = "50"
	out := RuntimeBuilds(t.Context(), runtimeProcessClient(t, current, prior, now, at, ""), registry, selected, now)
	if !RuntimeProcessValid(out) || out.Process.Counts.Changed != 1 || out.Process.Counts.SameRevisionChanges != 1 || out.Process.Counts.Compared != 203 || out.OmittedItems != 171 {
		t.Fatalf("preview hid replacement: %+v", out.Process)
	}
	for _, item := range out.Items {
		if item.Process.Comparison == "changed" {
			t.Fatal("fixture change should lie beyond preview")
		}
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("process response exceeded envelope")
	}
	for q, rows := range prior {
		if strings.Contains(q, `job="vmmd"`) {
			for i := range rows {
				if rows[i].Metric["node_id"] == "id-0" {
					rows[i].Metric["node_id"] = "old-node-id"
				}
			}
		}
	}
	current[query][39].Value[1] = "130"
	out = RuntimeBuilds(t.Context(), runtimeProcessClient(t, current, prior, now, at, ""), registry, selected, now)
	if !RuntimeProcessValid(out) || out.Process.Counts.Changed != 0 || out.Process.Counts.NewTargets != 1 || out.Process.Counts.RemovedTargets != 1 || !out.Process.Incomplete {
		t.Fatalf("node replacement became restart: %+v", out.Process)
	}
}

func TestRuntimeProcessBaselineSourcesAndSelection(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("single-box-v1", now, 0)
	out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeProcessValid(out) || out.Process.State != "baseline" || out.Process.Counts.Compared != 0 {
		t.Fatalf("first observation inferred history: %+v", out.Process)
	}
	query := runtimeProcessQuery(runtimeBuildJobSpecs(selected.role)[0])
	out = RuntimeBuilds(t.Context(), runtimeProcessClient(t, f, nil, now, time.Time{}, query), registry, selected, now)
	if !RuntimeProcessValid(out) || out.State != "matching" || out.Readiness.State != "ready" || out.Process.Counts.Unavailable != 1 {
		t.Fatalf("optional source changed identity/readiness: %+v", out)
	}
	raw := "release=" + selected.release + "&after=" + selected.after.Format(time.RFC3339Nano) + "&expected_role=" + selected.role
	for _, stamp := range []time.Time{now, now.Add(time.Second), now.Add(-api.ObsRuntimeProcessLookback - time.Second), selected.after.Add(-api.ObsHealthClockSkewTolerance - time.Second)} {
		if _, err := RuntimeBuildsSelection(raw+"&compare_at="+stamp.Format(time.RFC3339Nano), false, now); err == nil {
			t.Fatal("unbounded comparison accepted")
		}
	}
	if _, err := RuntimeBuildsSelection(raw+"&compare_at="+now.Add(-10*time.Second).Format(time.RFC3339Nano), false, now); err != nil {
		t.Fatal(err)
	}
	out.Process.Counts.Fresh++
	clean := SanitizeRuntimeProcess(out)
	if clean.Process != nil || clean.Items[0].Process != nil || !RuntimeBuildsValid(clean) || clean.Readiness == nil {
		t.Fatal("invalid process evidence discarded other evidence")
	}
}

func TestRuntimeProcessMaximumPreviewFitsResponse(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-30 * time.Second)
	current, selected, registry := runtimeFixture("control-plane-v2", now, 40)
	prior, _, _ := runtimeFixture(selected.role, at, 40)
	selected.compareAt = at
	out := RuntimeBuilds(t.Context(), runtimeProcessClient(t, current, prior, now, at, ""), registry, selected, now)
	currentAt, priorAt := now.Add(123456789*time.Nanosecond), at.Add(123456789*time.Nanosecond)
	for i := range out.Items {
		item := &out.Items[i]
		item.Node = strings.Repeat("a", api.ObsServingProbeNodeMaxBytes-4) + fmt.Sprintf("%04d", i)
		item.Version = "v" + strings.Repeat("a", api.ObsRuntimeBuildVersionMaxBytes-1)
		item.ObservedAt, item.ScrapeObservedAt = &currentAt, &currentAt
		item.Readiness.ObservedAt, item.Readiness.ReasonObservedAt = &currentAt, &currentAt
		item.Process.ObservedAt, item.Process.PreviousObservedAt = &currentAt, &priorAt
		value, previous := 100000000.12345679, 99999970.12345679
		item.Process.UptimeSeconds, item.Process.PreviousUptimeSeconds = &value, &previous
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if !RuntimeBuildsValid(out) || !RuntimeReadinessValid(out) || !RuntimeProcessValid(out) || len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatalf("maximum safe preview exceeded contract: bytes=%d", len(body))
	}
	// An unexpected target can have a fresh metric without establishing a
	// comparison for the selected inventory.
	out.Items[0].Unexpected = true
	out.Process.Incomplete = true
	out.Process.State, out.Process.Reason = "incomplete", "incomplete_continuity_evidence"
	out.Counts.Unexpected = 1
	if RuntimeProcessValid(out) {
		t.Fatal("unexpected target established process continuity")
	}
}
