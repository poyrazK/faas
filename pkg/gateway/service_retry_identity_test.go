// adr: 375
package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
	"github.com/onebox-faas/faas/pkg/wire"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestServiceRetryAttemptIdentity(t *testing.T) {
	for _, mode := range []string{"bodyless", "no_body", "replayable_body", "application_error", "retry_disabled", "missing_sibling_provenance", "security_refused_sibling"} {
		t.Run(mode, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()); otel.SetTracerProvider(previous) })
			first := ServiceEndpoint{InstanceID: "first", NodeID: "node-a", DeploymentID: "revision", Port: 8080, Region: "region-a", ImageDigest: "image-a"}
			second := ServiceEndpoint{InstanceID: "second", NodeID: "node-b", DeploymentID: "revision", Port: 8080, Region: "region-b", ImageDigest: "image-b"}
			if mode == "missing_sibling_provenance" {
				second.Region, second.ImageDigest = "", ""
			}
			store := &gatewaySecurityStore{}
			var registry *trafficrevocation.Registry
			if mode == "security_refused_sibling" {
				second.DeploymentID = "sibling-revision"
				registry = trafficrevocation.New(store)
				defer registry.Close()
			}
			endpoints := []ServiceEndpoint{first, second}
			var attempts []*http.Request
			var served []Target
			policy := RetryPolicy{Enabled: true, MaxAttempts: 2, BudgetPercent: 100, BudgetMinRetries: 1}
			if mode == "retry_disabled" {
				policy.MaxAttempts = 1
			}
			proxy := NewServiceProxy(ServiceProxyConfig{
				Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "orders", Endpoints: endpoints}},
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					return ServiceTarget{AppID: "orders"}, true, nil
				},
				Authorize: func(context.Context, string, string) (ServiceCaller, error) {
					return ServiceCaller{AppID: "client", AccountID: "verified-account"}, nil
				},
				RetryPolicy: policy, TrafficRevocations: registry,
				Forward: func(target Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						served = append(served, target)
						attempts = append(attempts, r)
						fields, ok := wire.FromContext(r.Context())
						if !ok || fields.AppID != target.AppID || fields.DeploymentID != target.DeploymentID || fields.InstanceID != target.InstanceID || fields.NodeID != target.NodeID || fields.TenantID != "verified-account" || fields.Region != target.Region || fields.ImageDigest != target.ImageDigest || fields.RequestID != "request" || fields.WakeID != "causal-wake" || fields.InvocationID != "invocation" {
							t.Errorf("attempt %d correlation=%+v target=%+v", len(attempts), fields, target)
						}
						if r.Header.Get(api.InstanceIDHeader) != target.InstanceID || r.Header.Get(api.TenantIDHeader) != "verified-account" || r.Header.Get(api.ImageDigestHeader) != target.ImageDigest {
							t.Error("guest identity disagrees with selected endpoint")
						}
						if mode == "replayable_body" {
							body, _ := io.ReadAll(r.Body)
							if string(body) != "payload" {
								t.Errorf("attempt body=%q", body)
							}
						}
						if target.InstanceID == "first" {
							if mode == "security_refused_sibling" {
								store.set(trafficrevocation.Scope{Kind: "deployment", ID: second.DeploymentID}, 1, true)
							}
							if mode != "application_error" {
								markStaleTarget(r.Context())
							}
							http.Error(w, "failed", http.StatusServiceUnavailable)
							return
						}
						w.WriteHeader(http.StatusOK)
					})
				},
			})
			request := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/work", nil)
			if mode == "bodyless" {
				request.Body = nil
			}
			if mode == "no_body" {
				request.Body = http.NoBody
			}
			if mode == "replayable_body" {
				request.Method = http.MethodPut
				request.Body = io.NopCloser(strings.NewReader("payload"))
				request.ContentLength = 7
				request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("payload")), nil }
			}
			request.Header.Set(ServiceProxyCallerAppHeader, "client")
			request.Header.Set(api.RequestIDHeader, "request")
			request.Header.Set(api.InstanceIDHeader, "guest-claim")
			request = request.WithContext(wire.WithContext(request.Context(), wire.CorrelationFields{AppID: "client", TenantID: "prior-account", InstanceID: "source", Region: "source-region", ImageDigest: "source-image", WakeID: "causal-wake", InvocationID: "invocation"}))
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, request)
			wantAttempts, wantStatus := 2, http.StatusOK
			if mode == "application_error" || mode == "retry_disabled" {
				wantAttempts, wantStatus = 1, http.StatusServiceUnavailable
			}
			if mode == "security_refused_sibling" {
				wantAttempts, wantStatus = 1, http.StatusForbidden
			}
			if len(attempts) != wantAttempts || response.Code != wantStatus {
				t.Fatalf("attempts=%d status=%d", len(attempts), response.Code)
			}
			if attempts[0].Header.Get(api.InstanceIDHeader) != "first" || request.Header.Get(api.InstanceIDHeader) != "guest-claim" {
				t.Error("replay mutated prior-attempt or ingress headers")
			}
			last := served[len(served)-1]
			span := findEndedSpan(t, recorder.Ended(), "service.orders")
			attrs := map[string]string{}
			for _, attr := range span.Attributes() {
				attrs[string(attr.Key)] = attr.Value.AsString()
			}
			if attrs["instance_id"] != last.InstanceID || attrs["node_id"] != last.NodeID || attrs["deployment_id"] != last.DeploymentID || attrs["region"] != last.Region || attrs["image_digest"] != last.ImageDigest {
				t.Errorf("completion span owner=%v want=%+v", attrs, last)
			}
		})
	}
}

func TestServiceUpgradeAttemptIdentity(t *testing.T) {
	for _, status := range []int{http.StatusSwitchingProtocols, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			endpoint := ServiceEndpoint{InstanceID: "upgraded", NodeID: "node", DeploymentID: "revision", Port: 8080}
			calls := 0
			proxy := NewServiceProxy(ServiceProxyConfig{
				Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "orders", Endpoints: []ServiceEndpoint{endpoint}}},
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					return ServiceTarget{AppID: "orders", WebSocketEnabled: true}, true, nil
				},
				Authorize: func(context.Context, string, string) (ServiceCaller, error) {
					return ServiceCaller{AppID: "client", AccountID: "verified-account"}, nil
				},
				MintCallerAssertion: func(ServiceCallerMintInput) (string, error) { return "signed-fixture", nil },
				Forward:             func(Target) http.Handler { t.Fatal("Upgrade used ordinary transport"); return nil },
				RawForward: func(target Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						fields, ok := wire.FromContext(r.Context())
						if !ok || fields.InstanceID != target.InstanceID || fields.DeploymentID != target.DeploymentID || fields.TenantID != "verified-account" || fields.ImageDigest != "" {
							t.Errorf("Upgrade correlation=%+v", fields)
						}
						if r.Header.Get(ServiceCallerAssertionHeader) != "signed-fixture" || !isTrustedServiceCallerAssertion(r.Context(), ServiceCallerAssertionHeader) {
							t.Error("Upgrade identity preparation lost the platform-minted assertion state")
						}
						w.WriteHeader(status)
					})
				},
			})
			request := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/work", nil)
			request.Header.Set(ServiceProxyCallerAppHeader, "client")
			request.Header.Set("Connection", "Upgrade")
			request.Header.Set("Upgrade", "websocket")
			request.Header.Set(ServiceCallerAssertionHeader, "guest-forgery")
			request = request.WithContext(wire.WithContext(request.Context(), wire.CorrelationFields{InstanceID: "source", ImageDigest: "source-image"}))
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, request)
			if calls != 1 || response.Code != status {
				t.Fatalf("calls=%d status=%d", calls, response.Code)
			}
		})
	}
}
