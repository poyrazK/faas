package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
)

func TestFetchAccountSLO_NoThrottlesPreservesMetrics(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "slo-owned")
	installPromFixture(t, &e, func(query string) string {
		if !strings.Contains(query, appID) {
			t.Errorf("account SLO query lacks owned app ID: %q", query)
		}
		if strings.Contains(query, "gateway_wake_queue_wait_seconds") {
			t.Errorf("account SLO queried unlabeled fleet wake metric: %q", query)
		}
		if strings.Contains(query, "gateway_rate_limited_total") {
			if !strings.Contains(query, "or vector(0)") {
				t.Errorf("throttling query lacks zero fallback: %q", query)
			}
			return `{"data":{"resultType":"vector","result":[{"value":[0,"0"]}]}}`
		}
		return `{"data":{"resultType":"vector","result":[{"value":[0,"42"]}]}}`
	})

	got, source := e.s.fetchAccountSLO(context.Background(), e.acct, "24h")
	if source != appmetrics.SourcePrometheus {
		t.Fatalf("source = %q, want %q", source, appmetrics.SourcePrometheus)
	}
	if got.RequestsTotal != 42 {
		t.Errorf("requests_total = %d, want 42", got.RequestsTotal)
	}
	if got.RequestDuration.P95MS != 42 {
		t.Errorf("p95_ms = %v, want 42", got.RequestDuration.P95MS)
	}
	if got.ThrottledTotal != 0 {
		t.Errorf("throttled_total = %d, want 0", got.ThrottledTotal)
	}
}

func TestFetchAccountSLO_ThrottleFailurePreservesCollectedMetrics(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "slo-throttle")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "gateway_rate_limited_total") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, "rate limit query unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"resultType":"vector","result":[{"value":[0,"42"]}]}}`)
	}))
	t.Cleanup(srv.Close)
	e.s.WithStatusCache(srv.URL, "")

	got, source := e.s.fetchAccountSLO(context.Background(), e.acct, "24h")
	if !strings.HasPrefix(source, appmetrics.SourceDegradedPrefix) {
		t.Fatalf("source = %q, want degraded prefix", source)
	}
	if !strings.Contains(source, "500") {
		t.Fatalf("source = %q, want Prometheus failure detail", source)
	}
	if got.RequestsTotal != 42 {
		t.Errorf("requests_total = %d, want preserved value 42", got.RequestsTotal)
	}
	if got.RequestDuration.P95MS != 42 {
		t.Errorf("p95_ms = %v, want preserved value 42", got.RequestDuration.P95MS)
	}
	if got.ThrottledTotal != 0 {
		t.Errorf("throttled_total = %d, want zero on failed optional query", got.ThrottledTotal)
	}
}
