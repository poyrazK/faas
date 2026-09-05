package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControlPlaneProxyKeepsAPIOnControlPlane(t *testing.T) {
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("control-plane"))
	}))
	defer controlPlane.Close()

	compute := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("compute"))
	})
	handler, err := newControlPlaneProxy(controlPlane.URL, compute, slog.Default())
	if err != nil {
		t.Fatalf("newControlPlaneProxy: %v", err)
	}

	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/v1/whoami", want: "control-plane"},
		{path: "/v1/apps/demo/invoke", want: "control-plane"},
		{path: "/v1/apps/demo/logs", want: "compute"},
		{path: "/v1/apps/demo/logs/stream", want: "compute"},
		{path: "/v1/synthesize", want: "compute"},
		{path: "/v1/invocations:dispatch", want: "compute"},
		{path: "/v1/invocations:dispatch_batch", want: "compute"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://edge.local"+tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := strings.TrimSpace(rec.Body.String()); got != tc.want {
				t.Fatalf("body = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestControlPlaneProxyDoesNotExposeMetricsDiscovery(t *testing.T) {
	var upstreamHits int
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(controlPlane.Close)

	handler, err := newControlPlaneProxy(controlPlane.URL, http.NotFoundHandler(), slog.Default())
	if err != nil {
		t.Fatalf("newControlPlaneProxy: %v", err)
	}
	for _, path := range []string{
		"/v1/internal/metrics/targets",
		"/v1/internal/metrics/promtail-targets",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://edge.local"+path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status=%d, want 404", rec.Code)
			}
		})
	}
	if upstreamHits != 0 {
		t.Fatalf("upstream hits=%d, want 0", upstreamHits)
	}
}

func TestControlPlaneProxyReportsUnavailableAPI(t *testing.T) {
	handler, err := newControlPlaneProxy("http://127.0.0.1:1", http.NotFoundHandler(), slog.Default())
	if err != nil {
		t.Fatalf("newControlPlaneProxy: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://edge.local/v1/whoami", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if !strings.Contains(string(body), "control_plane_unavailable") {
		t.Fatalf("body = %q, want control_plane_unavailable", body)
	}
}

func TestIsComputeOwnedLogsPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: "/v1/apps/demo/logs", want: true},
		{path: "/v1/apps/demo/logs/stream", want: true},
		{path: "/v1/apps/demo/invoke", want: false},
		{path: "/v1/apps//logs", want: false},
	} {
		if got := isComputeOwnedLogsPath(tc.path); got != tc.want {
			t.Errorf("isComputeOwnedLogsPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestIsComputeOwnedGatewayPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: "/v1/synthesize", want: true},
		{path: "/v1/invocations:dispatch", want: true},
		{path: "/v1/invocations:dispatch_batch", want: true},
		{path: "/v1/invocations", want: false},
		{path: "/v1/whoami", want: false},
	} {
		if got := isComputeOwnedGatewayPath(tc.path); got != tc.want {
			t.Errorf("isComputeOwnedGatewayPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
