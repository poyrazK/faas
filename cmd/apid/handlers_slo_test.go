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

func accountSLOPromResponse(query, appID string) string {
	switch {
	case strings.Contains(query, "gateway_request_duration_seconds_count"):
		return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s","class":"2xx"},"value":[0,"42"]}]}}`, appID)
	case strings.Contains(query, "gateway_request_duration_seconds_bucket"):
		return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s","le":"0.1"},"value":[0,"42"]},{"metric":{"app":"%s","le":"+Inf"},"value":[0,"42"]}]}}`, appID, appID)
	case strings.Contains(query, "gateway_cold_boot_total"):
		return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s"},"value":[0,"4"]}]}}`, appID)
	case strings.Contains(query, "gateway_rate_limited_total"):
		return `{"data":{"resultType":"vector","result":[]}}`
	default:
		return `{"data":{"resultType":"vector","result":[]}}`
	}
}

func TestSLOEndpoints_FreePlanReturn402(t *testing.T) {
	e := setup(t, api.PlanFree)
	createApp(t, e, "free-slo")
	for _, path := range []string{"/v1/apps/free-slo/slo", "/v1/account/slo"} {
		rec := e.do(t, http.MethodGet, path, nil, nil)
		assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanPerAppMetricsNotAllowed)
	}
}

func TestFetchAccountSLO_UsesHistogramPopulation(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "slo-population")
	installPromFixture(t, &e, func(query string) string {
		if strings.Contains(query, "gateway_requests_total") {
			t.Fatalf("account SLO used a different request population: %q", query)
		}
		if !strings.Contains(query, appID) {
			t.Errorf("query lacks owned app ID: %q", query)
		}
		return accountSLOPromResponse(query, appID)
	})
	_, source := e.s.fetchAccountSLO(context.Background(), e.acct, "24h")
	if source != appmetrics.SourcePrometheus {
		t.Fatalf("source = %q", source)
	}
}

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
			return `{"data":{"resultType":"vector","result":[]}}`
		}
		return accountSLOPromResponse(query, appID)
	})

	got, source := e.s.fetchAccountSLO(context.Background(), e.acct, "24h")
	if source != appmetrics.SourcePrometheus {
		t.Fatalf("source = %q, want %q", source, appmetrics.SourcePrometheus)
	}
	if got.RequestsTotal != 42 {
		t.Errorf("requests_total = %d, want 42", got.RequestsTotal)
	}
	if got.RequestDuration.P95MS != 95 {
		t.Errorf("p95_ms = %v, want 95", got.RequestDuration.P95MS)
	}
	if got.ThrottledTotal != 0 {
		t.Errorf("throttled_total = %d, want 0", got.ThrottledTotal)
	}
	if got.WakeQueueP95MS != nil || got.WakeQueueSampleStatus != api.SLOSampleStatusUnavailable {
		t.Errorf("wake queue = %v status=%q, want null/unavailable", got.WakeQueueP95MS, got.WakeQueueSampleStatus)
	}
}

func TestFetchAccountSLO_ThrottleFailurePreservesCollectedMetrics(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "slo-throttle")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "gateway_rate_limited_total") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, "rate limit query unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, accountSLOPromResponse(query, appID))
	}))
	t.Cleanup(srv.Close)
	e.s.WithStatusCache(srv.URL, "")

	got, source := e.s.fetchAccountSLO(context.Background(), e.acct, "24h")
	if !strings.HasPrefix(source, appmetrics.SourceDegradedPrefix) {
		t.Fatalf("source = %q, want degraded prefix", source)
	}
	if source != appmetrics.SourceDegradedPrefix+"telemetry unavailable" {
		t.Fatalf("source = %q, want redacted telemetry failure", source)
	}
	if got.RequestsTotal != 42 {
		t.Errorf("requests_total = %d, want preserved value 42", got.RequestsTotal)
	}
	if got.RequestDuration.P95MS != 95 {
		t.Errorf("p95_ms = %v, want preserved value 95", got.RequestDuration.P95MS)
	}
	if got.ThrottledTotal != 0 {
		t.Errorf("throttled_total = %d, want zero on failed optional query", got.ThrottledTotal)
	}
}

func TestTelemetryDegradedReasonRedactsPrometheusURLAndQuery(t *testing.T) {
	err := fmt.Errorf(`Get "http://127.0.0.1:9095/api/v1/query?query=app-secret": %w`, context.DeadlineExceeded)
	_, source := degradedAccountSLO(err, nil, "error_rate", "account-id", "24h")
	if source != appmetrics.SourceDegradedPrefix+"telemetry timeout" {
		t.Fatalf("source = %q, want stable timeout reason", source)
	}
	for _, forbidden := range []string{"127.0.0.1", "query=", "app-secret"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("customer-visible source leaked %q: %q", forbidden, source)
		}
	}
}
