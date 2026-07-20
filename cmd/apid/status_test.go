package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
// re-query Prometheus within the 30s TTL. fetch() runs three PromQL
// queries per refresh, so the first Get makes 3 server hits and the
// second (within TTL) makes 0.
func TestStatusCacheFreshnessFastPath(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"99.5"]}]}}`))
	}))
	defer srv.Close()

	c := newStatusCache(srv.URL, slog.Default())
	// First Get: 3 server hits (one per PromQL query in fetch).
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatalf("first get: %v", err)
	}
	if calls != 3 {
		t.Errorf("first get: server called %d times, want 3 (one per query)", calls)
	}
	// Second Get within TTL: cache hit, 0 server hits.
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatalf("second get: %v", err)
	}
	if calls != 3 {
		t.Errorf("second get: server called %d times, want 3 (cache hit suppressed refresh)", calls)
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