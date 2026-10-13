package apidserver_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
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
	e := apidserver.Setup(t, api.PlanFree)
	apidserver.CreateApp(t, e, "free-slo")
	for _, path := range []string{"/v1/apps/free-slo/slo", "/v1/account/slo"} {
		rec := e.Do(t, http.MethodGet, path, nil, nil)
		apidserver.AssertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanPerAppMetricsNotAllowed)
	}
}

func TestFetchAccountSLO_UsesHistogramPopulation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "slo-population")
	apidserver.InstallPromFixture(t, &e, func(query string) string {
		if strings.Contains(query, "gateway_requests_total") {
			t.Fatalf("account SLO used a different request population: %q", query)
		}
		if !strings.Contains(query, appID) {
			t.Errorf("query lacks owned app ID: %q", query)
		}
		return accountSLOPromResponse(query, appID)
	})
	_, source := (*e.S()).FetchAccountSLO(context.Background(), (*e.Acct()), "24h")
	if source != appmetrics.SourcePrometheus {
		t.Fatalf("source = %q", source)
	}
}

func TestFetchAccountSLO_NoThrottlesPreservesMetrics(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "slo-owned")
	apidserver.InstallPromFixture(t, &e, func(query string) string {
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

	got, source := (*e.S()).FetchAccountSLO(context.Background(), (*e.Acct()), "24h")
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
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "slo-throttle")
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
	(*e.S()).WithStatusCache(srv.URL, "")

	got, source := (*e.S()).FetchAccountSLO(context.Background(), (*e.Acct()), "24h")
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
	_, source := apidserver.DegradedAccountSLO(err, nil, "error_rate", "account-id", "24h")
	if source != appmetrics.SourceDegradedPrefix+"telemetry timeout" {
		t.Fatalf("source = %q, want stable timeout reason", source)
	}
	for _, forbidden := range []string{"127.0.0.1", "query=", "app-secret"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("customer-visible source leaked %q: %q", forbidden, source)
		}
	}
}

// TestFetchAppSLO_IdleAppIsNotDegraded reproduces production-us hunt #4:
// `gregale slo e2e-probe` reported "degraded: telemetry unavailable" for an
// app that simply had no requests in the window. The error-rate and
// cold-boot ratios have no denominator then and return no sample; #4181
// guarded the metrics path but not this one.
func TestFetchAppSLO_IdleAppIsNotDegraded(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "slo-idle")
	apidserver.InstallPromFixture(t, &e, func(query string) string {
		switch {
		case strings.Contains(query, "* 100) and (("):
			// PercentRatioQuery over an idle window: no sample.
			return `{"data":{"resultType":"vector","result":[]}}`
		case strings.Contains(query, "gateway_rate_limited_total"):
			return `{"data":{"resultType":"vector","result":[]}}`
		default:
			return `{"data":{"resultType":"vector","result":[{"metric":{},"value":[0,"0"]}]}}`
		}
	})
	app, err := (*(*e.S()).Store()).AppByID(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	got, source := (*e.S()).FetchAppSLO(context.Background(), app, (*e.Acct()), "24h")
	if source != appmetrics.SourcePrometheus {
		t.Fatalf("idle app source = %q, want %q", source, appmetrics.SourcePrometheus)
	}
	if got.RequestsTotal != 0 || got.ErrorRatePct != 0 || got.ColdBootRatePct != 0 {
		t.Fatalf("idle app SLO = %+v, want zero requests and rates", got)
	}
}

// fixedUsageStore reports a known usage_minutes rollup; MemStore's is always
// zero.
type fixedUsageStore struct {
	Store                  state.Store
	instanceHours, gbHours float64
}

func (f fixedUsageStore) UsageSLOForAccount(context.Context, string, time.Time, time.Time) (float64, float64, error) {
	return f.instanceHours, f.gbHours, nil
}

// TestFetchAccountSLO_TelemetryTimeoutKeepsUsage pins H9-133: on
// production-us the account panel's 24h Prometheus scans exceed their budget,
// and the degraded response also zeroed instance-hours and GB-hours, which
// come from Postgres, so a slow query made a busy account look idle.
func TestFetchAccountSLO_TelemetryTimeoutKeepsUsage(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "slo-usage")
	store := (*e.S()).Store()
	*store = statetest.NewStore(fixedUsageStore{Store: *store, instanceHours: 12.5, gbHours: 6.25})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, "query timed out")
	}))
	t.Cleanup(srv.Close)
	(*e.S()).WithStatusCache(srv.URL, "")

	got, source := (*e.S()).FetchAccountSLO(context.Background(), (*e.Acct()), "24h")
	if !strings.HasPrefix(source, appmetrics.SourceDegradedPrefix) {
		t.Fatalf("source = %q, want degraded", source)
	}
	if got.InstanceHours != 12.5 || got.GBHours != 6.25 {
		t.Fatalf("usage = %v instance-h / %v GB-h, want the Postgres rollup 12.5 / 6.25", got.InstanceHours, got.GBHours)
	}
	if got.RequestsTotal != 0 {
		t.Fatalf("requests_total = %d, want 0 when its query failed", got.RequestsTotal)
	}
}
