// spec: §6.3 — the gateway reports per-app in-flight demand to schedd's scale-in.

package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestHandlerReportsAppInflightWhileUpstreamIsSlow pins the scale-in
// input: a request counts as in flight for its app until it completes, even
// though the completion counter has not moved yet.
func TestHandlerReportsAppInflightWhileUpstreamIsSlow(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("slow"))
	}))
	t.Cleanup(upstream.Close)
	b := &fakeBackend{
		app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:     "jane-api.apps.dom",
		upstream: upstream.Listener.Addr().String(),
	}
	m := NewMetrics()
	h := NewHandlerWith(b, m, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
		done <- rec.Code
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request never reached the upstream")
	}
	if got := testutil.ToFloat64(m.appInflight.WithLabelValues("app-1")); got != 1 {
		t.Fatalf("in flight while upstream is slow = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("app-1", "pro", "200")); got != 0 {
		t.Fatalf("completions before the response = %v, want 0", got)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := testutil.ToFloat64(m.appInflight.WithLabelValues("app-1")); got != 0 {
		t.Fatalf("in flight after completion = %v, want 0", got)
	}

	srv := httptest.NewServer(ControlMux(m, nil, nil))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/metrics/gateway-requests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `gateway_app_inflight_requests{app="app-1"} 0`) {
		t.Fatalf("scheduler scrape endpoint missing the in-flight gauge:\n%s", body)
	}
}

func TestAdjustAppInflightNilSafe(t *testing.T) {
	var m *Metrics
	m.AdjustAppInflight("app-1", 1)
	NewMetrics().AdjustAppInflight("", 1)
}
