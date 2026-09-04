package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApidProxyDoesNotExposeMetricsDiscovery(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamHits++
	}))
	t.Cleanup(upstream.Close)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := newApidProxy(upstream.URL, next, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{
		"/v1/internal/metrics/targets",
		"/v1/internal/metrics/promtail-targets",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status=%d, want 404", rec.Code)
			}
		})
	}
	if upstreamHits != 0 {
		t.Fatalf("upstream hits=%d, want 0", upstreamHits)
	}
}
