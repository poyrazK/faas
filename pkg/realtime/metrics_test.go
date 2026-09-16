package realtime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// TestStatsCollectorExposesFixedCardinalityMetrics pins ADR-015's realtime
// observability contract: manager counters are exported without endpoint,
// app, principal, or connection labels.
func TestStatsCollectorExposesFixedCardinalityMetrics(t *testing.T) {
	manager := NewManager(Config{}, NopHooks{})
	defer func() { _ = manager.Close() }()
	manager.acceptedConnections.Store(3)
	manager.receivedBytes.Store(128)
	manager.callbackErrors.Store(1)

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewStatsCollector(manager))
	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, metric := range []string{
		`realtimed_accepted_connections_total 3`,
		`realtimed_received_bytes_total 128`,
		`realtimed_callback_errors_total 1`,
	} {
		if !strings.Contains(text, metric) {
			t.Errorf("metric %q missing from:\n%s", metric, text)
		}
	}
}
