package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestServiceProxyRecordsOnlyGuestHandledRequestTelemetry(t *testing.T) {
	const (
		accountID    = "11111111-1111-4111-8111-111111111111"
		targetAppID  = "22222222-2222-4222-8222-222222222222"
		deploymentID = "33333333-3333-4333-8333-333333333333"
		requestID    = "0123456789abcdef0123456789abcdef"
	)
	endpoint := ServiceEndpoint{
		InstanceID: "instance-worker-1", NodeID: "node-a", DeploymentID: deploymentID,
		Region: "eu-central", CommitSHA: "abc123", DeploymentTag: "stable",
		DeploymentCreatedAt: "2026-10-01T12:00:00Z", ImageDigest: "sha256:abc", Port: 8080,
	}
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
		AppID: targetAppID, Endpoints: []ServiceEndpoint{endpoint},
	}}
	var rows []RequestTelemetryRow
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: targetAppID, ScenarioTestRunID: "scenario-run"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller-app", AccountID: accountID}, nil
		},
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordForwardedHTTPResponse(r.Context(), http.StatusServiceUnavailable, true)
				w.WriteHeader(http.StatusServiceUnavailable)
			})
		},
		RecordRequestTelemetry: func(row RequestTelemetryRow) { rows = append(rows, row) },
		Metrics:                NewMetrics(),
	})

	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/worker/healthz", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "caller-app")
	req.Header.Set(api.RequestIDHeader, requestID)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("service response status = %d, want 503", rec.Code)
	}
	if len(rows) != 1 {
		t.Fatalf("recorded telemetry rows = %d, want one", len(rows))
	}
	row := rows[0]
	if row.AccountID.String() != accountID || row.AppID.String() != targetAppID || row.DeploymentID.String() != deploymentID {
		t.Fatalf("telemetry identity = account %s app %s deployment %s", row.AccountID, row.AppID, row.DeploymentID)
	}
	if row.Route != api.RequestTelemetryRouteServiceProxy || row.Method != http.MethodGet || row.Status != http.StatusServiceUnavailable {
		t.Fatalf("telemetry route/method/status = %q %q %d", row.Route, row.Method, row.Status)
	}
	if row.InstanceID != endpoint.InstanceID || row.NodeID != endpoint.NodeID || row.Region != endpoint.Region || row.CommitSHA != endpoint.CommitSHA || row.DeploymentTag != endpoint.DeploymentTag || row.ImageDigest != endpoint.ImageDigest {
		t.Fatalf("telemetry deployment provenance = %+v", row)
	}
	if row.TraceID != requestID || row.UsageOutboxed != true || row.ColdBoot || row.Count != 1 || row.ReceivedAt.IsZero() {
		t.Fatalf("telemetry correlation/accounting fields = %+v", row)
	}
}

func TestServiceProxyOmitsSyntheticBridgeResponseFromRequestTelemetry(t *testing.T) {
	const (
		accountID    = "11111111-1111-4111-8111-111111111111"
		targetAppID  = "22222222-2222-4222-8222-222222222222"
		deploymentID = "33333333-3333-4333-8333-333333333333"
	)
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
		AppID: targetAppID, Endpoints: []ServiceEndpoint{{
			InstanceID: "instance-worker-1", NodeID: "node-a", DeploymentID: deploymentID, Port: 8080,
		}},
	}}
	var rows []RequestTelemetryRow
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: targetAppID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller-app", AccountID: accountID}, nil
		},
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// A bridge error frame is platform-generated and does not prove
				// that the selected guest handled the request.
				recordForwardedHTTPResponse(r.Context(), http.StatusBadGateway, false)
				w.WriteHeader(http.StatusBadGateway)
			})
		},
		RecordRequestTelemetry: func(row RequestTelemetryRow) { rows = append(rows, row) },
		Metrics:                NewMetrics(),
	})
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/worker/healthz", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "caller-app")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("service response status = %d, want 502", rec.Code)
	}
	if len(rows) != 0 {
		t.Fatalf("synthetic bridge response recorded %d request telemetry rows", len(rows))
	}
}

func TestServiceProxyTelemetryLatencyUsesForwardAttemptDuration(t *testing.T) {
	started := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	clock := started
	const (
		accountID    = "11111111-1111-4111-8111-111111111111"
		targetAppID  = "22222222-2222-4222-8222-222222222222"
		deploymentID = "33333333-3333-4333-8333-333333333333"
	)
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
		AppID: targetAppID, Endpoints: []ServiceEndpoint{{
			InstanceID: "instance-worker-1", NodeID: "node-a", DeploymentID: deploymentID, Port: 8080,
		}},
	}}
	var rows []RequestTelemetryRow
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: targetAppID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller-app", AccountID: accountID}, nil
		},
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				clock = clock.Add(250 * time.Millisecond)
				recordForwardedHTTPResponse(r.Context(), http.StatusOK, true)
				w.WriteHeader(http.StatusOK)
			})
		},
		RecordRequestTelemetry: func(row RequestTelemetryRow) { rows = append(rows, row) },
		Metrics:                NewMetrics(),
		Now:                    func() time.Time { return clock },
	})
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/worker/healthz", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "caller-app")
	proxy.ServeHTTP(httptest.NewRecorder(), req)
	if len(rows) != 1 || rows[0].LatencyMS != 250 {
		t.Fatalf("request telemetry = %+v, want one 250ms app response", rows)
	}
}
