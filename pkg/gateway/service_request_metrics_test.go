package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/onebox-faas/faas/pkg/api"
)

func deploymentSampleCount(t *testing.T, m *Metrics, app, class, deployment string) uint64 {
	t.Helper()
	metric := &dto.Metric{}
	if err := m.requestDurationByDeployment.WithLabelValues(app, class, deployment).(prometheus.Metric).Write(metric); err != nil {
		t.Fatalf("write histogram: %v", err)
	}
	return metric.GetHistogram().GetSampleCount()
}

// A failed RPC is HTTP 200 with a non-zero grpc-status trailer; the service
// call's evidence must carry the failure, not the 200.
func TestServiceProxyObservesGRPCStatusFromTrailers(t *testing.T) {
	observed := make(chan ServiceRequestObservation, 1)
	appID := "44444444-4444-4444-8444-444444444444"
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID, Endpoints: []ServiceEndpoint{{InstanceID: "grpc-instance", NodeID: "node-1", Port: 8080}}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: appID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller", AccountID: "55555555-5555-4555-8555-555555555555"}, nil
		},
		ObserveRequest: func(_ *http.Request, o ServiceRequestObservation) { observed <- o },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordForwardedFirstByte(r.Context())
				w.Header().Set("Content-Type", "application/grpc")
				w.WriteHeader(http.StatusOK)
				w.Header().Set(http.TrailerPrefix+"Grpc-Status", "14")
			})
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/services/cart/hipstershop.CartService/GetCart", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "caller")
	req.Header.Set("Content-Type", "application/grpc")
	proxy.ServeHTTP(httptest.NewRecorder(), req)
	select {
	case o := <-observed:
		if o.Status != http.StatusServiceUnavailable {
			t.Fatalf("observed status = %d, want 503 for grpc-status 14", o.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no observation")
	}
}

// A service call is recorded in the request series app health and SLOs read,
// under the target's deployment, so an internal-only service has request
// evidence (2026-11-16: every gRPC backend read request_evidence_unavailable).
func TestServiceRequestRecordsRequestMetrics(t *testing.T) {
	m := NewMetrics()
	h := NewHandlerWith(nil, m, slog.Default()).WithAppPlanResolver(func(context.Context, string) (api.Plan, bool) {
		return api.PlanScale, true
	})
	const app, deployment = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	req := httptest.NewRequest(http.MethodPost, "/hipstershop.CartService/GetCart", nil)
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusServiceUnavailable} {
		h.RecordServiceRequest(req, ServiceRequestObservation{
			AccountID:  "33333333-3333-4333-8333-333333333333",
			Target:     Target{AppID: app, DeploymentID: deployment, InstanceID: "i-1"},
			ReceivedAt: time.Now(), LatencyMS: 12, Status: status,
		})
	}
	if got := deploymentSampleCount(t, m, app, "2xx", deployment); got != 1 {
		t.Fatalf("2xx samples = %d, want 1", got)
	}
	if got := deploymentSampleCount(t, m, app, "5xx", deployment); got != 2 {
		t.Fatalf("5xx samples = %d, want 2", got)
	}
	// The SLO burn-rate rules and schedd scale-up read gateway_requests_total.
	for code, want := range map[string]float64{"200": 1, "503": 2} {
		metric := &dto.Metric{}
		if err := m.requests.WithLabelValues(app, string(api.PlanScale), code).Write(metric); err != nil {
			t.Fatalf("write requests counter: %v", err)
		}
		if got := metric.GetCounter().GetValue(); got != want {
			t.Fatalf("gateway_requests_total{code=%q} = %v, want %v", code, got, want)
		}
		// The platform availability figure subtracts the service part.
		service := &dto.Metric{}
		if err := m.serviceRequests.WithLabelValues(app, code).Write(service); err != nil {
			t.Fatalf("write service requests counter: %v", err)
		}
		if got := service.GetCounter().GetValue(); got != want {
			t.Fatalf("gateway_service_requests_total{code=%q} = %v, want %v", code, got, want)
		}
	}
}
