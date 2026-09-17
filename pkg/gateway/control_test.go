// spec: §6.3
package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

func TestControlMuxHealthz(t *testing.T) {
	mux := ControlMux(NewMetrics(), nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("healthz body = %q, want \"ok\"", string(body))
	}
}

func TestControlMuxReadyz(t *testing.T) {
	t.Run("not-ready when callback false", func(t *testing.T) {
		mux := ControlMux(NewMetrics(), func() bool { return false }, nil)
		srv := httptest.NewServer(mux)
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("readyz status = %d, want 503", resp.StatusCode)
		}
	})
	t.Run("ready when callback true", func(t *testing.T) {
		mux := ControlMux(NewMetrics(), func() bool { return true }, nil)
		srv := httptest.NewServer(mux)
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("readyz status = %d, want 200", resp.StatusCode)
		}
	})
	t.Run("not-ready when no callback registered", func(t *testing.T) {
		// Post-#568 (ADR-070) the pre-split always-200 default was
		// inverted to always-503. A daemon that forgets to wire a
		// probe is a wiring bug; we surface it via /readyz so the
		// operator sees the daemon draining, instead of forwarding
		// traffic to a partial-boot instance.
		mux := ControlMux(NewMetrics(), nil, nil)
		srv := httptest.NewServer(mux)
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("readyz status = %d, want 503", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "no probe registered") {
			t.Errorf("readyz body = %q, want substring \"no probe registered\"", string(body))
		}
	})
}

func TestControlMuxMetrics(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequest("app-1", "pro", "200")
	mux := ControlMux(m, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("metrics status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "gateway_requests_total") {
		t.Errorf("metrics body missing gateway_requests_total:\n%s", string(body))
	}
}

func TestControlMuxRequestMetricsAreBoundedToScaleUpInput(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequest("app-1", "pro", "200")
	mux := ControlMux(m, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics/gateway-requests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request metrics status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "gateway_requests_total") {
		t.Fatalf("request metrics body missing gateway_requests_total:\n%s", text)
	}
	if strings.Contains(text, "gateway_request_duration_seconds") {
		t.Fatalf("request metrics endpoint leaked the full gateway registry:\n%s", text)
	}
}

func TestRunControlServerShutsDownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mux := ControlMux(NewMetrics(), nil, nil)
	errc := make(chan error, 1)
	go func() {
		// Bind to a loopback ephemeral port to avoid ":9090 in use" in CI.
		errc <- RunControlServer(ctx, "127.0.0.1:0", mux)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("RunControlServer returned %v, want nil or ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("RunControlServer did not return after ctx cancel")
	}
}

// TestControlMuxWithExtraExposesExtraGatherer exercises the gatewayd-public
// shape: no default Metrics, just an extra gatherer holding the budget
// metric families. The /metrics scrape must surface the budget series
// so the scraper sees gateway_request_budget_seconds after a request
// fires. ADR-093 PR-B wiring contract.
func TestControlMuxWithExtraExposesExtraGatherer(t *testing.T) {
	reg := prometheus.NewRegistry()
	budgetMetrics, err := reqbudget.NewMetrics(reg, "gateway")
	if err != nil {
		t.Fatalf("reqbudget.NewMetrics: %v", err)
	}
	// Touch the histogram and counter so the scraped body has rows
	// — prometheus.CounterVec / HistogramVec don't materialise a row
	// until WithLabelValues is called.
	budgetMetrics.RequestBudgetSeconds.
		WithLabelValues("forward", "POST:/test", "set").
		Observe(0.42)
	budgetMetrics.RequestBudgetExceededTotal.
		WithLabelValues("forward", "POST:/test", "http").
		Inc()

	mux := ControlMuxWithExtra(nil, reg, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	wantSubstrings := []string{
		"gateway_request_budget_seconds",
		"gateway_request_budget_exceeded_total",
		`endpoint="POST:/test"`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics body missing %q\n--- body ---\n%s", want, string(body))
		}
	}
}

// TestControlMuxWithExtraNilExtraMatchesControlMux pins the
// ControlMuxWithExtra(nil, nil, ...) fast-path: with both gatherers
// nil, /metrics is unmounted (matches the historical ControlMux
// behaviour for daemons that don't expose Prometheus).
func TestControlMuxWithExtraNilExtraMatchesControlMux(t *testing.T) {
	mux := ControlMuxWithExtra(nil, nil, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/metrics status = %d, want 404 (route unmounted)", resp.StatusCode)
	}
}

// TestControlMuxWithExtraCombinesBothGatherers pins the dual-gatherer
// path: when both m and extra are non-nil, a single scrape must surface
// series from BOTH registries. This is the gatewayd-internal-plus-extra
// shape (gatewayd-internal's Metrics bundle plus a budget gatherer).
func TestControlMuxWithExtraCombinesBothGatherers(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequest("app-1", "free", "200")

	extraReg := prometheus.NewRegistry()
	budgetMetrics, err := reqbudget.NewMetrics(extraReg, "gateway")
	if err != nil {
		t.Fatalf("reqbudget.NewMetrics: %v", err)
	}
	budgetMetrics.RequestBudgetExceededTotal.
		WithLabelValues("forward", "POST:/test", "http").
		Inc()

	mux := ControlMuxWithExtra(m, extraReg, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		"gateway_requests_total",
		"gateway_request_budget_exceeded_total",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics body missing %q (combined gatherers should serve both)\n--- body ---\n%s", want, string(body))
		}
	}
}

// TestNewControlHTTPServer_AppliesCanonicalShape pins the full
// envelope of the control-plane listener (ADR-122 / post-merge audit,
// issue #995 closure). REGRESSION GUARD: a future edit that drops one
// of the five knobs from NewControlHTTPServer fails this test. The
// helper is exported so the test can inspect the struct fields
// directly without binding a real listener (mirrors the
// pkg/githubd.NewWebhookHTTPServer precedent).
func TestNewControlHTTPServer_AppliesCanonicalShape(t *testing.T) {
	srv := NewControlHTTPServer("127.0.0.1:9090", http.NewServeMux())
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("RHT = %v, want 5s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 10*time.Second {
		t.Errorf("RT = %v, want 10s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 10*time.Second {
		t.Errorf("WT = %v, want 10s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 60*time.Second {
		t.Errorf("IT = %v, want 60s", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != int(api.DefaultMaxHeaderBytes) {
		t.Errorf("MHB = %d, want %d", srv.MaxHeaderBytes, int(api.DefaultMaxHeaderBytes))
	}
}
