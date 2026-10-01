package gateway

// adr: 377 — Synchronous inherited decisions retain their origin in request traces.

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestServiceProxyTraceShowsPropagatedAndInheritedFlagProvenance(t *testing.T) {
	const (
		callerAppID = "11111111-1111-4111-8111-111111111111"
		targetAppID = "22222222-2222-4222-8222-222222222222"
		customerID  = "33333333-3333-4333-8333-333333333333"
		originEnvID = "44444444-4444-4444-8444-444444444444"
	)
	encoded, err := flags.EncodePropagationHeader(flags.PropagationContext{
		Version: flags.PropagationContextVersion, CustomerID: customerID,
		Decisions: []flags.PropagationDecision{{
			Decision: flags.Decision{Flag: "export", Value: true, ConfigVersion: 7, RuleID: "selected", Reason: "rule_match", Source: "configuration"},
			Origin:   flags.EvidenceOrigin{AppID: callerAppID, EnvironmentID: originEnvID},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	previousProvider := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	const evidence = `[{"flag":"export","value":true,"config_version":7,"rule_id":"selected","reason":"rule_match","source":"inherited","inherited_from":{"app_id":"11111111-1111-4111-8111-111111111111","environment_id":"44444444-4444-4444-8444-444444444444"},"used":true}]`
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
			AppID: targetAppID, Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}},
		}},
		Resolve: func(_ context.Context, _, _ string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: targetAppID}, true, nil
		},
		Authorize: func(_ context.Context, _, _ string) (ServiceCaller, error) {
			return ServiceCaller{AppID: callerAppID, AccountID: customerID}, nil
		},
		Forward: func(_ Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwardedResponseHeader(r.Context(), w.Header(), api.FlagEvidenceHeader, base64.RawURLEncoding.EncodeToString([]byte(evidence)))
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/exports", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, callerAppID)
	req.Header.Set(api.FlagContextHeader, encoded)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	span := findEndedSpan(t, recorder.Ended(), "service.orders")
	propagated, decision := false, false
	for _, event := range span.Events() {
		attrs := map[string]attribute.Value{}
		for _, attr := range event.Attributes {
			attrs[string(attr.Key)] = attr.Value
		}
		switch event.Name {
		case "gregale.flag.propagated":
			propagated = attrs["gregale.flag.config_version"].AsInt64() == 7 && attrs["gregale.flag.origin_app_id"].AsString() == callerAppID && attrs["gregale.flag.rule_id"].AsString() == "selected"
		case "gregale.flag.decision":
			decision = attrs["gregale.flag.source"].AsString() == "inherited" && attrs["gregale.flag.origin_environment_id"].AsString() == originEnvID && attrs["gregale.flag.used"].AsBool()
		}
	}
	if !propagated || !decision {
		t.Fatalf("service span lacks flag provenance events: propagated=%t decision=%t events=%+v", propagated, decision, span.Events())
	}
}
