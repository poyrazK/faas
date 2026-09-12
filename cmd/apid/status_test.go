package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestStatusHistoryRollup seeds 90 days of terminal invocation rows and
// verifies that the public status projection keeps exactly the last 30 days,
// calculates weighted uptime, and includes the operator incident timeline
// (issue #276 / spec §12).
func TestStatusHistoryRollup(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "status-history@localhost", api.PlanScale)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "status-history",
		Runtime:   "node22",
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	now := time.Now().UTC()
	for daysAgo := 0; daysAgo < 90; daysAgo++ {
		createdAt := now.AddDate(0, 0, -daysAgo)
		for i := 0; i < 3; i++ {
			stateValue := state.InvocationCompleted
			if daysAgo >= statusHistoryDays || i == 2 {
				stateValue = state.InvocationFailed
			}
			if _, err := store.EnqueueInvocation(ctx, state.Invocation{
				AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke,
				State: stateValue, CreatedAt: createdAt, DueAt: createdAt,
			}); err != nil {
				t.Fatalf("EnqueueInvocation day %d row %d: %v", daysAgo, i, err)
			}
		}
	}
	if _, err := store.InsertStatusIncident(ctx, state.StatusIncidentComponentApid,
		state.StatusIncidentSeverityDegraded, "API latency elevated"); err != nil {
		t.Fatalf("InsertStatusIncident: %v", err)
	}

	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[{"value":[0,"0"]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"99.5"]}]}}`))
	}))
	t.Cleanup(prom.Close)

	c := newStatusCacheWithStore(prom.URL, store, slog.Default())
	snap, err := c.Get(ctx)
	if err != nil {
		t.Fatalf("status Get: %v", err)
	}
	if len(snap.Uptime30d) != statusHistoryDays {
		t.Fatalf("uptime buckets = %d, want %d", len(snap.Uptime30d), statusHistoryDays)
	}
	want := float64(2) / 3 * 100
	if snap.Uptime30dPct == nil || *snap.Uptime30dPct != want {
		t.Fatalf("uptime_30d_pct = %v, want %v", snap.Uptime30dPct, want)
	}
	if snap.Uptime30d[0].Total != 3 || snap.Uptime30d[0].Successful != 2 {
		t.Fatalf("oldest bucket = %+v, want 2/3", snap.Uptime30d[0])
	}
	if len(snap.Incidents) != 1 || snap.Incidents[0].Summary != "API latency elevated" {
		t.Fatalf("incidents = %+v, want one projected incident", snap.Incidents)
	}
}

// TestStatusJSONHandlerNoPrometheusURL is the degraded path. With
// an empty prometheus URL the handler must return 200 + a payload
// whose Source explains the gap — never 5xx.
func TestStatusJSONHandlerNoPrometheusURL(t *testing.T) {
	s := newServer(nil, slog.Default(), "unset", nil)
	s.WithStatusCache("", "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/status/slo.json", nil)
	s.statusJSONHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var snap StatusPage
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(snap.Source, "degraded:") {
		t.Errorf("source = %q, want degraded prefix", snap.Source)
	}
}

// An idle histogram is returned by Prometheus as the string "NaN". The
// public handler must still emit valid JSON rather than committing a 200
// response and then failing json.Encoder with an empty body.
func TestStatusJSONHandlerIdleHistogramEmitsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		switch {
		case strings.Contains(query, "gateway_wake_latency_seconds_bucket"):
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"NaN"]}]}}`))
		case strings.Contains(query, "builderd_ops_total"):
			// Prometheus evaluates the query's idle fallback, vector(100).
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"100"]}]}}`))
		case strings.Contains(query, "ALERTS"):
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"0"]}]}}`))
		default:
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"100"]}]}}`))
		}
	}))
	t.Cleanup(srv.Close)

	s := newServer(nil, slog.Default(), "unset", nil)
	s.WithStatusCache(srv.URL, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status/slo.json", nil)
	s.statusJSONHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var snap StatusPage
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if snap.APIAvailabilityPct != 100 || snap.WakeP95MS != nil || snap.BuildSuccessPct != 100 {
		t.Fatalf("snapshot = %+v, want finite idle API/build=100 and wake unavailable", snap)
	}
}

func TestStatusQueriesDefineIdleValues(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		fallback string
		guard    string
	}{
		{
			name:     "api availability",
			query:    statusAPIAvailabilityQuery,
			fallback: "or vector(100)",
			guard:    "sum(rate(gateway_requests_total{app!=\"-\",code=~\"2..|5..\"}[5m])) > 0",
		},
		{
			name:     "wake p95",
			query:    statusWakeP95Query,
			fallback: "",
			guard:    "sum(rate(gateway_wake_latency_seconds_count[5m])) > 0",
		},
		{
			name:     "build success",
			query:    statusBuildSuccessQuery,
			fallback: "or vector(100)",
			guard:    "sum(rate(builderd_ops_total{op=\"build\",code!=\"user_error\"}[5m])) > 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.query, tt.guard) {
				t.Fatalf("query %q is missing its non-idle denominator guard %q", tt.query, tt.guard)
			}
			if tt.fallback != "" && !strings.Contains(tt.query, tt.fallback) {
				t.Fatalf("query %q is missing idle fallback %q", tt.query, tt.fallback)
			}
			if tt.name == "wake p95" && strings.Contains(tt.query, "or vector(") {
				t.Fatalf("query %q synthesizes a wake value for an idle period", tt.query)
			}
		})
	}
	if !strings.Contains(statusBuildSuccessQuery, `code=~"ok|cache_hit"`) {
		t.Fatalf("build success numerator includes a non-success outcome: %q", statusBuildSuccessQuery)
	}
	if strings.Contains(statusAPIAvailabilityQuery, `gateway_requests_total[5m]`) ||
		!strings.Contains(statusAPIAvailabilityQuery, `app!="-"`) {
		t.Fatalf("API availability query includes unresolved-host traffic: %q", statusAPIAvailabilityQuery)
	}
	if !strings.Contains(statusAPIAvailabilityQuery, `code=~"2..|5.."`) ||
		strings.Contains(statusAPIAvailabilityQuery, `code=~"[45].."`) {
		t.Fatalf("API availability query does not exclude client 4xx outcomes: %q", statusAPIAvailabilityQuery)
	}
}

func TestStatusHistoryNoTrafficRemainsUnknown(t *testing.T) {
	store := state.NewMemStore()
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "gateway_wake_latency_seconds_bucket") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"100"]}]}}`))
	}))
	t.Cleanup(prom.Close)

	snap, err := newStatusCacheWithStore(prom.URL, store, slog.Default()).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Uptime30dPct != nil {
		t.Fatalf("uptime_30d_pct = %v, want null with no observations", snap.Uptime30dPct)
	}
	if len(snap.Uptime30d) != statusHistoryDays {
		t.Fatalf("daily buckets = %d, want %d", len(snap.Uptime30d), statusHistoryDays)
	}
	for _, bucket := range snap.Uptime30d {
		if bucket.UptimePct != nil || bucket.Total != 0 {
			t.Fatalf("empty day represented as measured uptime: %+v", bucket)
		}
	}
}

func TestStatusJSONHandlerWithoutCacheStillReturnsValidJSON(t *testing.T) {
	s := newServer(state.NewMemStore(), slog.Default(), "gregale.dev", nil)
	rec := httptest.NewRecorder()
	s.statusJSONHandler(rec, httptest.NewRequest(http.MethodGet, "/status/slo.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var snapshot StatusPage
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("invalid degraded JSON: %v; body=%s", err, rec.Body.String())
	}
	if !strings.HasPrefix(snapshot.Source, "degraded:") {
		t.Fatalf("source = %q, want degraded prefix", snapshot.Source)
	}
}

// TestStatusCacheFreshnessFastPath: a freshly-fetched cache must not
// re-query Prometheus within the 30s TTL. fetch() runs four PromQL
// queries per refresh (api avail, wake p95, build success, degraded
// flag), so the first Get makes 4 server hits and the second (within
// TTL) makes 0. The alert query is a comparison expression and
// returns resultType=scalar; the fixture routes by query string.
func TestStatusCacheFreshnessFastPath(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"99.5"]}]}}`))
	}))
	defer srv.Close()

	c := newStatusCache(srv.URL, slog.Default())
	// First Get: 4 server hits (one per PromQL query in fetch).
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatalf("first get: %v", err)
	}
	if calls != 4 {
		t.Errorf("first get: server called %d times, want 4 (one per query)", calls)
	}
	// Second Get within TTL: cache hit, 0 server hits.
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatalf("second get: %v", err)
	}
	if calls != 4 {
		t.Errorf("second get: server called %d times, want 4 (cache hit suppressed refresh)", calls)
	}
}

// TestStatusCacheStaleOnError: if Prometheus starts failing, Get
// must return the last good snapshot with Source= degraded, not
// surface an error. The page should never 5xx during a transient
// Prometheus hiccup — that's the explicit contract.
func TestStatusCacheStaleOnError(t *testing.T) {
	var healthy bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"99.5"]}]}}`))
	}))
	defer srv.Close()

	c := newStatusCache(srv.URL, slog.Default())
	healthy = true
	snap, err := c.Get(context.Background())
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	if snap.APIAvailabilityPct != 99.5 {
		t.Errorf("seeded pct = %v, want 99.5", snap.APIAvailabilityPct)
	}
	healthy = false
	c.mu.Lock()
	c.lastEval = time.Now().Add(-time.Hour) // force a refresh attempt
	c.mu.Unlock()
	snap, err = c.Get(context.Background())
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	if snap.APIAvailabilityPct != 99.5 {
		t.Errorf("stale pct = %v, want 99.5 (graceful degradation)", snap.APIAvailabilityPct)
	}
	if !strings.HasPrefix(snap.Source, "degraded:") {
		t.Errorf("source = %q, want degraded prefix", snap.Source)
	}
	healthy = true
	recovered, err := c.getEvaluation(context.Background())
	if err != nil {
		t.Fatalf("recovered refresh: %v", err)
	}
	if recovered.dataStatus != "fresh" || recovered.legacy.Source != appmetrics.SourcePrometheus {
		t.Fatalf("recovered evaluation data=%q source=%q, want fresh prometheus", recovered.dataStatus, recovered.legacy.Source)
	}
}

func TestStatusCacheMarksSnapshotOlderThanNinetySecondsStale(t *testing.T) {
	c := newStatusCache("", slog.Default())
	c.cached = statusEvaluation{
		legacy: StatusPage{AsOf: time.Now().UTC()}, states: publicstatus.Evaluate(nil, nil),
		indicatorAvailable: map[string]bool{}, dataStatus: "fresh",
		updatedAt: time.Now().UTC().Add(-91 * time.Second), telemetryAvailable: true,
	}
	c.lastEval = time.Now()
	c.hasCached = true
	evaluation, err := c.getEvaluation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.dataStatus != "stale" {
		t.Fatalf("data status=%q, want stale", evaluation.dataStatus)
	}
}

// TestStatusHandler_ServesHTMLFile writes a fake status page to a
// temp file, points s.statusPagePath at it, and asserts the handler
// streams the file body with the right Content-Type.
func TestStatusHandler_ServesHTMLFile(t *testing.T) {
	tmp := t.TempDir()
	page := tmp + "/index.html"
	if err := os.WriteFile(page, []byte("<!doctype html><h1>status ok</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newServer(nil, slog.Default(), "unset", nil)
	s.WithStatusCache("", page)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	s.statusHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "status ok") {
		t.Errorf("body = %q, missing rendered content", rec.Body.String())
	}
}

// TestStatusHandler_MissingFileFallback: with no statusPagePath set
// AND the production default /etc/faas/statuspage/index.html missing,
// the handler must fall back to the embedded "source unavailable"
// page (spec §12: never 5xx just because the file is missing).
func TestStatusHandler_MissingFileFallback(t *testing.T) {
	s := newServer(nil, slog.Default(), "unset", nil)
	s.WithStatusCache("", "/nonexistent/path/index.html")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	s.statusHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fallback page)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Status source unavailable") {
		t.Errorf("body = %q, missing fallback banner", rec.Body.String())
	}
}

// TestStatus_DegradedFlag drives the 4th PromQL query through four
// cases that together pin down the contract:
//
//  1. Firing alerts present → Degraded=true, Source="degraded: firing alerts".
//  2. No firing alerts     → Degraded=false, Source="prometheus".
//  3. Alert query fails    → Degraded=false, Source is degraded because
//     component health is unknown even though the three SLO queries work.
//  4. Scalar result shape  → covers the bug where the alert query
//     `count(ALERTS{...}) > 0` returns resultType=scalar (not vector)
//     and the previous parser required vector and rejected the
//     payload with "no data". Without this branch the degraded pill
//     never flips on in production.
func TestStatus_DegradedFlag(t *testing.T) {
	primary := func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"99.5"]}]}}`))
	}

	t.Run("firing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"component":"apid","severity":"warn"},"value":[0,"1"]}]}}`))
				return
			}
			primary(w)
		}))
		defer srv.Close()

		c := newStatusCache(srv.URL, slog.Default())
		snap, err := c.Get(context.Background())
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !snap.Degraded {
			t.Errorf("Degraded = false, want true when alerts firing")
		}
		if snap.Source != "degraded: firing alerts" {
			t.Errorf("Source = %q, want %q", snap.Source, "degraded: firing alerts")
		}
	})

	t.Run("not_firing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
				return
			}
			primary(w)
		}))
		defer srv.Close()

		c := newStatusCache(srv.URL, slog.Default())
		snap, err := c.Get(context.Background())
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if snap.Degraded {
			t.Errorf("Degraded = true, want false when no alerts firing")
		}
		if snap.Source != "prometheus" {
			t.Errorf("Source = %q, want %q", snap.Source, "prometheus")
		}
	})

	t.Run("alert_query_fails_primary_ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
				http.Error(w, "no alerts metric registered", http.StatusInternalServerError)
				return
			}
			primary(w)
		}))
		defer srv.Close()

		c := newStatusCache(srv.URL, slog.Default())
		snap, err := c.Get(context.Background())
		if err != nil {
			t.Fatalf("get: %v (primary queries should have succeeded)", err)
		}
		if snap.Degraded {
			t.Errorf("Degraded = true, want false when alert query fails (graceful degradation)")
		}
		if !strings.HasPrefix(snap.Source, "degraded:") {
			t.Errorf("Source = %q, want degraded prefix for missing alert telemetry", snap.Source)
		}
	})

	t.Run("alerts_available_primary_indicators_missing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
				return
			}
			http.Error(w, "indicator unavailable", http.StatusInternalServerError)
		}))
		defer srv.Close()

		c := newStatusCache(srv.URL, slog.Default())
		evaluation, err := c.getEvaluation(context.Background())
		if err != nil {
			t.Fatalf("alert telemetry should keep component status usable: %v", err)
		}
		if !evaluation.telemetryAvailable || evaluation.dataStatus != "stale" {
			t.Fatalf("evaluation availability=%v data=%q, want component telemetry with stale indicators", evaluation.telemetryAvailable, evaluation.dataStatus)
		}
		if !strings.HasPrefix(evaluation.legacy.Source, "degraded:") {
			t.Fatalf("legacy source=%q, want degraded partial-telemetry marker", evaluation.legacy.Source)
		}
	})

	// The evaluator needs the original labels, not a scalar count, so it can
	// map each firing alert to the customer-facing capability ledger.
	t.Run("labeled_vector", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"component":"gatewayd-public","severity":"page"},"value":[0,"1"]}]}}`))
				return
			}
			primary(w)
		}))
		defer srv.Close()

		c := newStatusCache(srv.URL, slog.Default())
		snap, err := c.Get(context.Background())
		if err != nil {
			t.Fatalf("labeled alert vector should parse; got err=%v", err)
		}
		if !snap.Degraded {
			t.Errorf("Degraded = false, want true for firing alert vector")
		}
	})
}

func TestStatusDegradedQueryExcludesNonServiceAlerts(t *testing.T) {
	var alertQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("query"); strings.Contains(q, "ALERTS") {
			alertQuery = q
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[{"value":[0,"0"]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"100"]}]}}`))
	}))
	defer srv.Close()

	if _, err := newStatusCache(srv.URL, slog.Default()).Get(context.Background()); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(alertQuery, `family!~"alert_preset_signals|alert_preset_correlation"`) {
		t.Fatalf("alert query does not exclude tenant preset families: %s", alertQuery)
	}
	if !strings.Contains(alertQuery, `public_status!="internal"`) {
		t.Fatalf("alert query does not exclude internal operator alerts: %s", alertQuery)
	}
}

// TestStatus_AllQueriesFail pins the full-pipeline failure path:
// when ALL four PromQL queries fail (e.g. Prometheus down), fetch
// must return a non-nil error so the JSON handler can fall back to
// the stale cache and stamp Source="degraded: <error>". This is the
// only path that surfaces a real outage on the public page; without
// it the page would silently emit the last good snapshot forever.
func TestStatus_AllQueriesFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newStatusCache(srv.URL, slog.Default())
	_, err := c.Get(context.Background())
	if err == nil {
		t.Fatal("Get returned nil error; want non-nil when every query fails")
	}
	if !strings.Contains(err.Error(), "down") {
		t.Errorf("err = %q, want it to mention the upstream error", err.Error())
	}
}
