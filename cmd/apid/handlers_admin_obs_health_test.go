// handlers_admin_obs_health_test.go — Obs-Meta + Trace-IDs Mega-PR /
// C7: tests for GET /v1/admin/obs/health.
//
// Pins the JSON shape, the admin-only auth gate, and the SQL-derived
// data flow. PromQL-derived fields are pinned at the seed default
// (0 / 1.0) when s.promqlClient is nil, while prometheus_available
// must be false. The healthy PromQL path is covered separately below so the
// no-data fallback remains distinguishable from a Prometheus outage.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// newObsHealthEnv mirrors newObsEnv (handlers_admin_obs_test.go)
// for the /v1/admin/obs/health endpoint. Wires an admin
// allowlist entry so the adminAllows gate fires as expected.
// The API key carries the scopes argument so auth-scope tests
// can pin a non-admin caller without retuning the env.
func newObsHealthEnv(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "obs-health-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	srv.WithAdminAllowlist(adminEmail)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct}
}

// TestObsHealthHandler_StableJSONShapeOnEmptyDB exercises the
// happy path: an admin caller hits the endpoint against a fresh
// MemStore with no operator actions in the window. The response
// MUST carry every closed-set field — the dashboard renders the
// tile without per-field nil-checks. SQL-derived fields are
// seeded from api.ObsHealthKindVocabulary (counts → 0, ratios →
// 1.0).
//
// PromQL-derived fields return their nil-promql defaults (0 for
// counters, 1.0 for the coverage ratio) because the test server
// has s.promqlClient == nil. The availability flag prevents those
// defaults from being mistaken for live data.
func TestObsHealthHandler_StableJSONShapeOnEmptyDB(t *testing.T) {
	e := newObsHealthEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")

	rec := e.do(t, "GET", "/v1/admin/obs/health", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp api.ObsHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}

	// Closed-set shape: every kind in the vocabulary must be
	// present (counts: 0, ratios: 1.0).
	if got := len(resp.OperatorIntentOutcomeMissingCounts); got != len(api.ObsHealthKindVocabulary) {
		t.Errorf("missing-counts len = %d, want %d", got, len(api.ObsHealthKindVocabulary))
	}
	for _, k := range api.ObsHealthKindVocabulary {
		if v, ok := resp.OperatorIntentOutcomeMissingCounts[k]; !ok {
			t.Errorf("missing-counts missing key %q", k)
		} else if v != 0 {
			t.Errorf("missing-counts[%q] = %d, want 0", k, v)
		}
		if v, ok := resp.TraceIDCompletenessRatio[k]; !ok {
			t.Errorf("ratio missing key %q", k)
		} else if v != 1.0 {
			t.Errorf("ratio[%q] = %v, want 1.0 (vacuous truth on empty)", k, v)
		}
	}

	// PromQL-derived fields: nil-promql defaults.
	if resp.AuditLogWriteTotal5m != 0 {
		t.Errorf("AuditLogWriteTotal5m = %d, want 0 (nil-promql)", resp.AuditLogWriteTotal5m)
	}
	if resp.AuditLogWriteFailures5m != 0 {
		t.Errorf("AuditLogWriteFailures5m = %d, want 0 (nil-promql)", resp.AuditLogWriteFailures5m)
	}
	if resp.AuditLogCoverageRatio5m != 1.0 {
		t.Errorf("AuditLogCoverageRatio5m = %v, want 1.0 (nil-promql)", resp.AuditLogCoverageRatio5m)
	}
	if resp.AlertsFiring != 0 {
		t.Errorf("AlertsFiring = %d, want 0 (nil-promql)", resp.AlertsFiring)
	}
	if resp.PrometheusAvailable {
		t.Error("PrometheusAvailable = true, want false (nil-promql)")
	}
	if resp.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero, want a non-zero UTC timestamp")
	}
}

func TestObsHealthHandler_PrometheusNoActivityIsAvailable(t *testing.T) {
	var queries []string
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1728000000,"0"]}]}}`)
	}))
	defer promServer.Close()

	e := newObsHealthEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	e.s.promqlClient = promql.NewClient(promServer.URL, promServer.Client())
	rec := e.do(t, "GET", "/v1/admin/obs/health", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if !resp.PrometheusAvailable {
		t.Fatal("PrometheusAvailable = false, want true for successful no-activity queries")
	}
	if resp.AuditLogCoverageRatio5m != 1.0 {
		t.Fatalf("AuditLogCoverageRatio5m = %v, want 1.0", resp.AuditLogCoverageRatio5m)
	}
	want := []string{
		`sum(increase(audit_log_write_total[5m])) or vector(0)`,
		`sum(increase(audit_log_write_failures_total[5m])) or vector(0)`,
		`sum(audit_log_coverage_ratio_5m) or vector(1)`,
		`count(ALERTS{alertstate="firing"}) or vector(0)`,
	}
	if len(queries) != len(want) {
		t.Fatalf("PromQL query count = %d, want %d (%v)", len(queries), len(want), queries)
	}
	for i := range want {
		if queries[i] != want[i] {
			t.Errorf("query[%d] = %q, want %q", i, queries[i], want[i])
		}
	}
}

// TestObsHealthHandler_RejectsCustomerScope confirms the
// admin-scope gate fires when the caller carries a non-admin
// scope set. Mirrors the existing /v1/admin/obs/* handlers'
// posture — admin scope is the structural gate; the email
// allowlist (s.adminAllows) is the second layer.
func TestObsHealthHandler_RejectsCustomerScope(t *testing.T) {
	e := newObsHealthEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")

	rec := e.do(t, "GET", "/v1/admin/obs/health", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// TestObsHealthHandler_RejectsAdminScopeWithoutAllowlist confirms
// the adminAllows second-layer gate fires when the caller carries
// admin scope but their email is not in the FAAS_ADMIN_EMAILS
// allowlist. Mirrors the existing
// TestObsOverview_AdminScopeRejectsNonAllowlistedEmail test.
func TestObsHealthHandler_RejectsAdminScopeWithoutAllowlist(t *testing.T) {
	e := newObsHealthEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "stranger@faas.dev")

	rec := e.do(t, "GET", "/v1/admin/obs/health", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// TestSeedHealthKindCounts_StableShape pins the closed-set seed
// invariant: every kind in api.ObsHealthKindVocabulary is present
// and starts at 0. Adding a new kind to the vocabulary without
// updating this seed would surface as a missing key on the wire
// — exactly the bug we want to catch.
func TestSeedHealthKindCounts_StableShape(t *testing.T) {
	got := seedHealthKindCounts()
	if len(got) != len(api.ObsHealthKindVocabulary) {
		t.Errorf("len(seed) = %d, want %d", len(got), len(api.ObsHealthKindVocabulary))
	}
	for _, k := range api.ObsHealthKindVocabulary {
		if v, ok := got[k]; !ok {
			t.Errorf("missing key %q", k)
		} else if v != 0 {
			t.Errorf("[%q] = %d, want 0", k, v)
		}
	}
}

// TestSeedHealthKindRatios_StableShape mirrors
// TestSeedHealthKindCounts_StableShape for the ratios map.
// Per-kind value is 1.0 (vacuous truth on empty).
func TestSeedHealthKindRatios_StableShape(t *testing.T) {
	got := seedHealthKindRatios()
	if len(got) != len(api.ObsHealthKindVocabulary) {
		t.Errorf("len(seed) = %d, want %d", len(got), len(api.ObsHealthKindVocabulary))
	}
	for _, k := range api.ObsHealthKindVocabulary {
		if v, ok := got[k]; !ok {
			t.Errorf("missing key %q", k)
		} else if v != 1.0 {
			t.Errorf("[%q] = %v, want 1.0", k, v)
		}
	}
}

// TestObsHealthResponse_JSONKeysAreStable pins the wire-key
// spelling. Renaming any of the JSON tags is a breaking change
// — the dashboard's tile-mapping contract relies on the exact
// keys. Catches a future refactor that re-tags a field.
func TestObsHealthResponse_JSONKeysAreStable(t *testing.T) {
	raw := `{
		"generated_at": "2026-08-26T00:00:00Z",
		"prometheus_available": false,
		"audit_log_write_total_5m": 0,
		"audit_log_write_failures_5m": 0,
		"audit_log_coverage_ratio_5m": 1.0,
		"operator_intent_outcome_missing_total": {
			"force_park": 0, "force_cold_boot": 0, "force_restart": 0
		},
		"trace_id_completeness_ratio": {
			"force_park": 1.0, "force_cold_boot": 1.0, "force_restart": 1.0
		},
		"alerts_firing": 0
	}`
	var resp api.ObsHealthResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode canonical payload: %v", err)
	}
	// Round-trip: re-encode and check the keys survive.
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	for _, want := range []string{
		"audit_log_write_total_5m",
		"audit_log_write_failures_5m",
		"audit_log_coverage_ratio_5m",
		"operator_intent_outcome_missing_total",
		"trace_id_completeness_ratio",
		"alerts_firing",
		"generated_at",
		"prometheus_available",
	} {
		if !strings.Contains(string(out), `"`+want+`":`) {
			t.Errorf("missing JSON key %q after round-trip", want)
		}
	}
}
