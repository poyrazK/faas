package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDashboardProxy_ForwardsDashboardPaths confirms a /dashboard/
// request hits the upstream (a fake apid) and the response body
// round-trips. The next handler is never invoked.
func TestDashboardProxy_ForwardsDashboardPaths(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		if r.Header.Get("x-faas-request-id") == "" {
			t.Error("upstream request missing x-faas-request-id header (gatewayd should generate one)")
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("apid: dashboard rendered"))
	}))
	t.Cleanup(upstream.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next handler invoked; should have been proxied")
		w.WriteHeader(http.StatusTeapot)
	})
	handler := newDashboardProxy(upstream.URL, next, log)

	for _, path := range []string{"/dashboard/", "/dashboard/apps", "/dashboard/apps/foo", "/oauth/callback"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("code = %d, want 200", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "apid: dashboard rendered") {
				t.Errorf("body = %q, want proxied apid body", rec.Body.String())
			}
		})
	}
	if upstreamHits != 4 {
		t.Errorf("upstream hits = %d, want 4", upstreamHits)
	}
}

// TestDashboardProxy_PassesThroughNonDashboardPaths confirms requests
// outside the dashboard prefix fall through to the next handler
// (gateway.Handler's wake/proxy path) without touching apid.
func TestDashboardProxy_PassesThroughNonDashboardPaths(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamHits++
	}))
	t.Cleanup(upstream.Close)

	var nextHits int
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextHits++
		w.WriteHeader(http.StatusOK)
	})
	handler := newDashboardProxy(upstream.URL, next, log)

	for _, path := range []string{"/", "/api/v1/apps", "/healthz", "/apps/jane/api"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("code = %d, want 200", rec.Code)
			}
		})
	}
	if upstreamHits != 0 {
		t.Errorf("upstream hits = %d, want 0 (paths should fall through)", upstreamHits)
	}
	if nextHits != 4 {
		t.Errorf("next hits = %d, want 4", nextHits)
	}
}

// TestDashboardProxy_DisabledWhenTargetEmpty confirms the wrapper is
// a no-op when target is empty — every request goes to next.
func TestDashboardProxy_DisabledWhenTargetEmpty(t *testing.T) {
	var nextHits int
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextHits++
		w.WriteHeader(http.StatusOK)
	})
	handler := newDashboardProxy("", next, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", rec.Code)
	}
	if nextHits != 1 {
		t.Errorf("next hits = %d, want 1", nextHits)
	}
}

// TestDashboardProxy_UpstreamDown confirms a 503 RFC 7807 problem is
// emitted when apid is unreachable (instead of the stdlib's bare
// "EOF" connection-reset text).
func TestDashboardProxy_UpstreamDown(t *testing.T) {
	// Reserve a port we know is free, then immediately close the listener
	// so nothing answers.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := "http://" + ln.Addr().String()
	_ = ln.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newDashboardProxy(addr, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next should not be called for dashboard paths")
	}), log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), "apid_unavailable") {
		t.Errorf("body = %q, want apid_unavailable problem code", rec.Body.String())
	}
}
