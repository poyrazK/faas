package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestSchedulerMetricsCombinedScrapeDoesNotDuplicateInstrumentation(t *testing.T) {
	ops, dashboard := prometheus.NewRegistry(), prometheus.NewRegistry()
	for name, reg := range map[string]*prometheus.Registry{"ops_probe": ops, "dashboard_probe": dashboard} {
		reg.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: name}))
	}
	mux := http.NewServeMux()
	registerSchedulerMetrics(mux, ops, dashboard)
	for _, path := range []string{metricsPath + "/fcvm", metricsPath} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "dashboard_probe") {
			t.Fatal("dashboard metric missing")
		}
		if path == metricsPath && !strings.Contains(rec.Body.String(), "ops_probe") {
			t.Fatal("ops metric missing")
		}
		// FaasSnapshotDriftAuditStale joins on the process start time; the
		// process collector reports it on Linux, where schedd runs.
		if path == metricsPath && runtime.GOOS == "linux" && !strings.Contains(rec.Body.String(), "process_start_time_seconds") {
			t.Fatal("process_start_time_seconds missing from the schedd scrape")
		}
	}
}
