package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpsec"
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

func TestControlPlaneProxyPreservesTrustedIngressClient(t *testing.T) {
	var gotXFF, gotProto string
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotProto = r.Header.Get("X-Forwarded-Proto")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(controlPlane.Close)
	handler, err := newControlPlaneProxy(controlPlane.URL, http.NotFoundHandler(), slog.Default(),
		netip.MustParsePrefix("127.0.0.0/8"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://api.gregale.dev/v1/whoami", nil)
	req.RemoteAddr = "127.0.0.1:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")
	req.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if gotXFF != "203.0.113.42" || gotProto != "https" {
		t.Fatalf("forwarding context = (%q, %q), want trusted client and https", gotXFF, gotProto)
	}
}

func TestControlPlaneProxyRejectsSpoofedForwardingContext(t *testing.T) {
	var gotXFF, gotProto string
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotProto = r.Header.Get("X-Forwarded-Proto")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(controlPlane.Close)
	handler, err := newControlPlaneProxy(controlPlane.URL, http.NotFoundHandler(), slog.Default(),
		netip.MustParsePrefix("127.0.0.0/8"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://api.gregale.dev/v1/whoami", nil)
	req.RemoteAddr = "198.51.100.9:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")
	req.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if gotXFF != "198.51.100.9" || gotProto != "http" {
		t.Fatalf("forwarding context = (%q, %q), want immediate peer and http", gotXFF, gotProto)
	}
}

func TestControlPlaneProxyScopesHealthAndReadinessToPlatformHost(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "gregale.dev")
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("platform-health"))
	}))
	defer controlPlane.Close()
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("app-health"))
	})
	handler, err := newControlPlaneProxy(controlPlane.URL, app, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		host string
		path string
		want string
	}{
		{host: "gregale.dev", path: "/healthz", want: "platform-health"},
		{host: "api.gregale.dev", path: "/healthz", want: "platform-health"},
		{host: "api.gregale.dev", path: "/readyz", want: "platform-health"},
		{host: "127.0.0.1:8080", path: "/readyz", want: "platform-health"},
		{host: "healthy-app.gregale.dev", path: "/healthz", want: "app-health"},
		{host: "healthy-app.gregale.dev", path: "/readyz", want: "app-health"},
		{host: "nonexistent.gregale.dev", path: "/readyz", want: "app-health"},
		{host: "customer.example", path: "/readyz", want: "app-health"},
	} {
		t.Run(tc.host+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Body.String(); got != tc.want {
				t.Fatalf("body = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestControlPlaneProxyPreservesPublicReadinessStatus(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "gregale.dev")
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Fatalf("path = %q, want /readyz", r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "not-ready")
	}))
	t.Cleanup(controlPlane.Close)
	handler, err := newControlPlaneProxy(controlPlane.URL, http.NotFoundHandler(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://api.gregale.dev/readyz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "not-ready" {
		t.Fatalf("response = %d %q, want 503 not-ready", rec.Code, rec.Body.String())
	}
}

func TestControlPlaneProxyRoutesExactGitHubWebhookOnPlatformHost(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "gregale.dev")
	githubd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhooks/github" {
			t.Fatalf("githubd path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Hub-Signature-256"); got != "sha256=test" {
			t.Fatalf("signature header = %q", got)
		}
		_, _ = io.WriteString(w, "githubd")
	}))
	t.Cleanup(githubd.Close)
	t.Setenv("FAAS_GITHUBD_LOOPBACK", githubd.URL)

	controlPlane := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(controlPlane.Close)
	compute := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "compute")
	})
	handler, err := newControlPlaneProxy(controlPlane.URL, compute, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		host string
		path string
		want string
	}{
		{host: "api.gregale.dev", path: "/webhooks/github", want: "githubd"},
		{host: "gregale.dev", path: "/webhooks/github", want: "githubd"},
		{host: "app.gregale.dev", path: "/webhooks/github", want: "compute"},
		{host: "api.gregale.dev", path: "/webhooks/github/extra", want: "compute"},
	} {
		t.Run(tc.host+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://"+tc.host+tc.path, strings.NewReader("{}"))
			req.Header.Set("X-Hub-Signature-256", "sha256=test")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Body.String(); got != tc.want {
				t.Fatalf("body = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestControlPlaneProxyPublicEdgeOwnsStaticSecurityHeaders(t *testing.T) {
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add(httpsec.HeaderXFrameOptions, "inner-one")
		w.Header().Add(httpsec.HeaderXFrameOptions, "inner-two")
		w.Header().Set("X-Customer-Header", "preserved")
		w.WriteHeader(http.StatusOK)
	}))
	defer controlPlane.Close()
	handler, err := newControlPlaneProxy(controlPlane.URL, http.NotFoundHandler(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://gregale.dev/v1/whoami", nil)
	rec := httptest.NewRecorder()
	httpsec.Static(handler).ServeHTTP(rec, req)
	if got := rec.Header().Values(httpsec.HeaderXFrameOptions); len(got) != 1 || got[0] != httpsec.ValueXFrameOptions {
		t.Fatalf("X-Frame-Options = %v, want one canonical value", got)
	}
	if got := rec.Header().Get("X-Customer-Header"); got != "preserved" {
		t.Fatalf("customer header = %q, want preserved", got)
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

func TestControlPlaneProxyScopesMetricsByHost(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "gregale.dev")
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("platform /metrics must not reach apid")
	}))
	t.Cleanup(controlPlane.Close)
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "customer_workload_metric 1\n")
	})
	handler, err := newControlPlaneProxy(controlPlane.URL, app, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("platform API returns problem JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "https://api.gregale.dev/metrics", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/problem+json") {
			t.Fatalf("Content-Type = %q, want application/problem+json", got)
		}
		var problem api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
		if problem.Code != api.CodeNotFound {
			t.Fatalf("problem code = %q, want %q", problem.Code, api.CodeNotFound)
		}
	})

	for _, host := range []string{"demo.gregale.dev", "customer.example"} {
		t.Run(host+" reaches workload", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://"+host+"/metrics", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Body.String(); got != "customer_workload_metric 1\n" {
				t.Fatalf("body = %q, want customer workload response", got)
			}
		})
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
