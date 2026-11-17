package operatorobs

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeDiagnosticSelectionContract(t *testing.T) {
	for _, role := range RuntimeBuildRoles() {
		choices := []string{"all", "none"}
		if strings.HasSuffix(role, "-v2") {
			choices = append(choices, "builder_health", "preparation_health,builder_health")
		}
		for _, raw := range choices {
			d, err := ParseRuntimeDiagnostics(role, raw)
			if err != nil || !RuntimeDiagnosticsValid(role, d) {
				t.Fatal("valid selector rejected", role, raw)
			}
			again, err := ParseRuntimeDiagnostics(role, d.QueryValue())
			if err != nil || !RuntimeDiagnosticsEqual(d, again) {
				t.Fatal("selection failed canonical roundtrip")
			}
			clone := CloneRuntimeDiagnostics(d)
			if len(clone.Selected) > 0 {
				clone.Selected[0] = "invalid"
				if RuntimeDiagnosticsValid(role, clone) || !RuntimeDiagnosticsValid(role, d) {
					t.Fatal("clone aliased or invalid selection accepted")
				}
			}
		}
	}
	for _, raw := range []string{"", "unknown", "loop_health", "none,builder_health", "all,none", "builder_health,builder_health", "builder_health,", " builder_health", strings.Repeat("x", 100000)} {
		if _, err := ParseRuntimeDiagnostics("single-box-v2", raw); err == nil {
			t.Fatal("invalid selector accepted")
		}
	}
	if _, err := ParseRuntimeDiagnostics("single-box-v1", "builder_health"); err == nil {
		t.Fatal("unsupported legacy group accepted")
	}
	now := time.Now().UTC().Truncate(time.Second)
	q := url.Values{"expected_role": {"single-box-v2"}, "release": {strings.Repeat("a", 40)}, "after": {now.Add(-time.Minute).Format(time.RFC3339)}, "diagnostics": {"http_cron_delivery"}}
	if s, err := RuntimeBuildsSelection(q.Encode(), false, now); err != nil || s.diagnostics == nil {
		t.Fatal("API selector missing")
	}
	q.Add("diagnostics", "builder_health")
	if _, err := RuntimeBuildsSelection(q.Encode(), false, now); err == nil {
		t.Fatal("repeated query accepted")
	}
}
func TestRuntimeDiagnosticsSkipQueriesAndPreserveCore(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var baselineReads int32
	cases := append([]string{"", "none"}, RuntimeDiagnosticGroups("single-box-v2")...)
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			f, s, r := runtimeFixture("single-box-v2", now, 1)
			addLoopFixture(f, s.role, now)
			if raw != "" {
				s.diagnostics, _ = ParseRuntimeDiagnostics(s.role, raw)
			}
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				reads.Add(1)
				q := req.URL.Query().Get("query")
				rows, ok := f[q]
				if !ok {
					t.Errorf("unexpected query %s", q)
				}
				if rows == nil {
					rows = []inputFixtureRow{}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}})
			}))
			defer server.Close()
			out := RuntimeBuildsWithLoopHealth(t.Context(), promql.NewClient(server.URL, server.Client()), r, s, now)
			if out.State != "matching" || out.Process == nil || out.Readiness == nil || out.LoopHealth == nil || out.LoopHealth.Incomplete {
				t.Fatal("core evidence changed")
			}
			body, _ := json.Marshal(out)
			var wire map[string]json.RawMessage
			_ = json.Unmarshal(body, &wire)
			if raw == "" {
				baselineReads = reads.Load()
				if out.Diagnostics != nil {
					t.Fatal("default contract changed")
				}
			} else {
				if !RuntimeDiagnosticsValid(s.role, out.Diagnostics) || reads.Load() >= baselineReads {
					t.Fatal("selector did not reduce source queries")
				}
				for _, key := range RuntimeDiagnosticGroups(s.role) {
					_, exists := wire[key]
					if exists != RuntimeDiagnosticSelected(s.diagnostics, key) {
						t.Fatalf("wrong selected data for %s", key)
					}
				}
				if raw == "http_cron_delivery" && (len(out.HTTPCronDelivery.Items) != 1 || out.HTTPCronDelivery.OmittedItems != 0) {
					t.Fatal("focused target detail was lost")
				}
			}
			if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
				t.Fatal("existing response cap exceeded")
			}
		})
	}
}

func TestRuntimeDiagnosticsLegacyCoreOnly(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range []string{"single-box-v1", "control-plane-v1"} {
		t.Run(role, func(t *testing.T) {
			f, s, r := runtimeFixture(role, now, 1)
			addLoopFixture(f, role, now)
			var original map[string]json.RawMessage
			for _, raw := range []string{"", "all", "none"} {
				if raw != "" {
					s.diagnostics, _ = ParseRuntimeDiagnostics(role, raw)
				}
				out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), r, s, now)
				body, _ := json.Marshal(out)
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(body, &fields)
				for _, name := range RuntimeDiagnosticGroups("single-box-v2") {
					if _, exists := fields[name]; exists {
						t.Fatal("legacy profile gained advisory job", name)
					}
				}
				if raw == "" {
					original = fields
				} else {
					if !RuntimeDiagnosticsValid(role, out.Diagnostics) || len(out.Diagnostics.Selected) != 0 || len(out.Diagnostics.NotSelected) != 0 {
						t.Fatal("legacy scope is not core-only")
					}
					delete(fields, "diagnostics")
					if len(fields) != len(original) {
						t.Fatal("core fields changed")
					}
					for key, want := range original {
						if string(fields[key]) != string(want) {
							t.Fatal("legacy core evidence changed", key)
						}
					}
				}
			}
		})
	}
}
