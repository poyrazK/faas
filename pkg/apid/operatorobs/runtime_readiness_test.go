package operatorobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/onebox-faas/faas/pkg/promql"
	"strings"
	"testing"
	"time"
)

func readinessQueries(spec runtimeBuildJobSpec) (string, string) {
	prefix := strings.TrimSuffix(spec.metric, "_build_info")
	return fmt.Sprintf(`%s_ready{job=%q,daemon=%q}`, prefix, spec.job, spec.daemon), fmt.Sprintf(`%s_ready_reason{job=%q,daemon=%q}`, prefix, spec.job, spec.daemon)
}

func addRuntimeReadinessFixture(f inputFixture, role string, now time.Time) {
	for _, spec := range runtimeBuildJobSpecs(role) {
		ready, reason := readinessQueries(spec)
		for _, q := range []string{ready, "timestamp(" + ready + ")", reason, "timestamp(" + reason + ")"} {
			f[q] = []inputFixtureRow{}
		}
		for _, up := range f[fmt.Sprintf(`up{job=%q}`, spec.job)] {
			for i, q := range []string{ready, "timestamp(" + ready + ")", reason, "timestamp(" + reason + ")"} {
				classes := []string{""}
				if i >= 2 {
					classes = runtimeReadinessReasons()
				}
				for _, class := range classes {
					value := float64(1)
					if i >= 2 && class != "ready" {
						value = 0
					}
					if i%2 == 1 {
						value = float64(now.Unix())
					}
					row := inputRow(spec.job, up.Metric["instance"], spec.daemon, value)
					for k, v := range up.Metric {
						row.Metric[k] = v
					}
					row.Metric["daemon"] = spec.daemon
					if class != "" {
						row.Metric["reason"] = class
					}
					f[q] = append(f[q], row)
				}
			}
		}
	}
}

func TestRuntimeReadinessValuesAndReasons(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, state, itemState, reason string
		change                         func(inputFixture, string, string)
	}{
		{"ready", "ready", "ready", "ready", func(inputFixture, string, string) {}},
		{"fresh_zero", "not_ready", "not_ready", "database", func(f inputFixture, r, s string) {
			f[r][0].Value[1] = "0"
			f[s][0].Value[1] = "0"
			f[s][2].Value[1] = "1"
		}},
		{"draining", "not_ready", "not_ready", "draining", func(f inputFixture, r, s string) {
			f[r][0].Value[1] = "0"
			f[s][0].Value[1] = "0"
			f[s][1].Value[1] = "1"
		}},
		{"inconsistent", "incomplete", "conflicting", "conflicting_readiness", func(f inputFixture, r, s string) { f[r][0].Value[1] = "0" }},
		{"two_reasons", "incomplete", "conflicting", "conflicting_readiness", func(f inputFixture, r, s string) { f[s][1].Value[1] = "1" }},
		{"non_binary", "incomplete", "conflicting", "conflicting_readiness", func(f inputFixture, r, s string) { f[r][0].Value[1] = "2" }},
		{"no_active_reason", "incomplete", "conflicting", "conflicting_readiness", func(f inputFixture, r, s string) { f[s][0].Value[1] = "0" }},
		{"incomplete_reasons", "incomplete", "missing", "readiness_missing", func(f inputFixture, r, s string) { f[s] = f[s][:9]; f["timestamp("+s+")"] = f["timestamp("+s+")"][:9] }},

		{"missing", "incomplete", "missing", "readiness_missing", func(f inputFixture, r, s string) { f[r] = nil; f["timestamp("+r+")"] = nil }},
		{"stale_value", "incomplete", "stale", "readiness_stale", func(f inputFixture, r, s string) {
			f["timestamp("+r+")"][0].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
		}},
		{"stale_zero_reason", "incomplete", "stale", "readiness_stale", func(f inputFixture, r, s string) {
			f["timestamp("+s+")"][9].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
		}},
		{"boundary", "incomplete", "unavailable", "readiness_before_boundary", func(f inputFixture, r, s string) {
			f["timestamp("+r+")"][0].Value[1] = fmt.Sprint(now.Add(-100 * time.Second).Unix())
		}},
		{"future", "incomplete", "unavailable", "invalid_sample_time", func(f inputFixture, r, s string) {
			f["timestamp("+r+")"][0].Value[1] = fmt.Sprint(now.Add(time.Hour).Unix())
		}},
		{"unknown_reason", "incomplete", "unavailable", "invalid_evidence", func(f inputFixture, r, s string) { f[s][0].Metric["reason"] = "private-error" }},
		{"orphan_time", "incomplete", "unavailable", "invalid_evidence", func(f inputFixture, r, s string) { f["timestamp("+r+")"][0].Metric["extra"] = "private-error" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v2", now, 0)
			ready, reason := readinessQueries(runtimeBuildJobSpecs(selected.role)[0])
			tc.change(f, ready, reason)
			out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			item := out.Items[0].Readiness
			if !RuntimeBuildsValid(out) || !RuntimeReadinessValid(out) || out.State != "matching" || out.Readiness.State != tc.state || item.State != tc.itemState || item.Reason != tc.reason {
				t.Fatalf("unexpected readiness: %+v %+v", out.Readiness, item)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-error") {
				t.Fatal("raw reason leaked")
			}
		})
	}
}

func TestRuntimeReadinessFullInventoryAndSourceFailures(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("control-plane-v2", now, 50)
	ready, reason := readinessQueries(runtimeBuildJobSpecs(selected.role)[7])
	f[ready][49].Value[1] = "0"
	f[reason][490].Value[1] = "0"
	f[reason][491].Value[1] = "1"
	out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if out.Readiness.State != "not_ready" || out.Readiness.Counts.NotReady != 1 || out.Readiness.Counts.Ready != 252 || out.OmittedItems != 221 || !RuntimeReadinessValid(out) {
		t.Fatalf("preview hid readiness: %+v", out.Readiness)
	}
	unavailable := RuntimeBuilds(t.Context(), nil, registry, selected, now)
	if unavailable.Readiness.Counts.Unavailable != 253 || !RuntimeReadinessValid(unavailable) {
		t.Fatalf("source became ready: %+v", unavailable.Readiness)
	}
	// Unknown optional readiness must not discard the build observation.
	out.Readiness.Reasons[0].Reason = "private-error"
	cleaned := SanitizeRuntimeReadiness(out)
	if cleaned.Readiness != nil || cleaned.Items[0].Readiness != nil || !RuntimeBuildsValid(cleaned) || out.Readiness == nil {
		t.Fatal("invalid readiness corrupted identity")
	}
}

func TestRuntimeReadinessSourceFailureDoesNotEraseBuilds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("single-box-v1", now, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "apid_daemon_ready") {
			http.Error(w, "private-provider-error", http.StatusServiceUnavailable)
			return
		}
		rows, ok := f[query]
		if !ok {
			t.Errorf("unexpected query: %s", query)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}})
	}))
	defer server.Close()
	out := RuntimeBuilds(t.Context(), promql.NewClient(server.URL, server.Client()), registry, selected, now)
	if out.State != "matching" || out.Readiness.State != "incomplete" || out.Readiness.Counts.Unavailable != 1 || out.Readiness.Counts.Ready != 3 || !RuntimeReadinessValid(out) {
		t.Fatalf("readiness source changed builds: %+v", out)
	}
	body, _ := json.Marshal(out)
	if strings.Contains(string(body), "private-provider-error") {
		t.Fatal("source error leaked")
	}
}
