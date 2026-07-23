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
)

// TestStatusJSONHandlerNoPrometheusURL is the degraded path. With
// an empty prometheus URL the handler must return 200 + a payload
// whose Source explains the gap — never 5xx.
func TestStatusJSONHandlerNoPrometheusURL(t *testing.T) {
	s := newServer(nil, slog.Default(), "DOMAIN", nil)
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
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[{"value":[0,"0"]}]}}`))
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
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[{"value":[0,"0"]}]}}`))
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
	s := newServer(nil, slog.Default(), "DOMAIN", nil)
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
	s := newServer(nil, slog.Default(), "DOMAIN", nil)
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
//  3. Alert query fails    → Degraded=false, Source stays "prometheus"
//     (graceful degradation — a PromQL hiccup on ALERTS{} must not
//     poison the public snapshot; the pre-existing full-pipeline
//     failure path still surfaces via Source="degraded: <error>").
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
				// Prometheus emits `resultType: "scalar"` for
				// comparison expressions like `count(...) > 0`.
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[{"value":[0,"1"]}]}}`))
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
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[{"value":[0,"0"]}]}}`))
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
		if snap.Source != "prometheus" {
			t.Errorf("Source = %q, want %q (graceful degradation)", snap.Source, "prometheus")
		}
	})

	// Regression for the scalar-shape bug: previously the alert query
	// response used `resultType: "vector"` in the fixture, which masked
	// the real PromQL behaviour. `count(ALERTS{...}) > 0` is a
	// comparison expression and Prometheus emits it as a scalar. A
	// pure-scalar test (no ALERTS branch in the handler) pins the
	// contract.
	t.Run("scalar_response_pure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[{"value":[0,"1"]}]}}`))
				return
			}
			primary(w)
		}))
		defer srv.Close()

		c := newStatusCache(srv.URL, slog.Default())
		snap, err := c.Get(context.Background())
		if err != nil {
			t.Fatalf("scalar-shaped alert query should parse; got err=%v", err)
		}
		if !snap.Degraded {
			t.Errorf("Degraded = false, want true for scalar firing count")
		}
	})
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
