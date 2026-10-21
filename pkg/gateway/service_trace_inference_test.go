// adr: 1138
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

const (
	inferredTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	inferredTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	callerTraceparent   = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
)

func TestWithInferredCallerTrace(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sent       string // traceparent the caller sent
		inferred   string // what the local vmmd answers
		nilInfer   bool
		wantHeader string
		wantMarked bool
		wantAsked  bool
	}{
		{name: "caller forwarded context", sent: callerTraceparent, inferred: inferredTraceparent, wantHeader: callerTraceparent},
		{name: "no context, one request in flight", inferred: inferredTraceparent, wantHeader: inferredTraceparent, wantMarked: true, wantAsked: true},
		{name: "malformed context is replaced", sent: "00-garbage", inferred: inferredTraceparent, wantHeader: inferredTraceparent, wantMarked: true, wantAsked: true},
		{name: "vmmd names no request", wantAsked: true},
		{name: "vmmd answers garbage", inferred: "00-not-a-trace", wantAsked: true},
		{name: "linking disabled", nilInfer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := false
			p := &ServiceProxy{}
			if !tc.nilInfer {
				p.inferCallerTrace = func(_ context.Context, remoteAddr string) string {
					asked = true
					if remoteAddr != "10.100.0.7:41000" {
						t.Fatalf("remoteAddr = %q", remoteAddr)
					}
					return tc.inferred
				}
			}
			req := httptest.NewRequest("GET", "/v1/internal/services/worker/", nil)
			req.RemoteAddr = "10.100.0.7:41000"
			req.Header.Set("Tracestate", "vendor=1")
			if tc.sent != "" {
				req.Header.Set("Traceparent", tc.sent)
			}
			got := p.withInferredCallerTrace(req)
			wantHeader := tc.wantHeader
			if wantHeader == "" {
				wantHeader = tc.sent
			}
			if asked != tc.wantAsked || got.Header.Get("Traceparent") != wantHeader || traceInferredOf(got) != tc.wantMarked {
				t.Fatalf("asked=%v traceparent=%q marked=%v", asked, got.Header.Get("Traceparent"), traceInferredOf(got))
			}
			if tc.wantMarked && got.Header.Get("Tracestate") != "" {
				t.Fatal("the caller's tracestate survived an inferred parent")
			}
		})
	}
}

// A linked call continues the caller's trace end to end: the telemetry row
// carries the inferred trace id and the mark.
func TestServiceCallJoinsInferredTrace(t *testing.T) {
	accountID, appID := uuid.NewString(), uuid.NewString()
	recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true}, nopLog())
	handler := &Handler{requestTelemetry: recorder}
	var forwarded []string
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID,
			Endpoints: []ServiceEndpoint{{InstanceID: "worker-1", NodeID: "node-1", Port: 8080}}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: appID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller", AccountID: accountID}, nil
		},
		InferCallerTrace: func(context.Context, string) string { return inferredTraceparent },
		ObserveRequest:   handler.RecordServiceRequest,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded = append(forwarded, r.Header.Get("Traceparent"))
				recordForwardedFirstByte(r.Context())
				w.WriteHeader(200)
			})
		},
	})
	for _, sent := range []string{"", callerTraceparent} {
		req := httptest.NewRequest("GET", "/v1/internal/services/worker/", nil)
		req.Header.Set(ServiceProxyCallerAppHeader, "caller")
		if sent != "" {
			req.Header.Set("Traceparent", sent)
		}
		proxy.ServeHTTP(httptest.NewRecorder(), req)
	}
	rows := recorder.DrainBatch(10)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].TraceID != inferredTraceID || !rows[0].TraceInferred {
		t.Fatalf("linked call row = trace %q inferred %v", rows[0].TraceID, rows[0].TraceInferred)
	}
	if rows[1].TraceID != "0af7651916cd43dd8448eb211c80319c" || rows[1].TraceInferred {
		t.Fatalf("propagated call row = trace %q inferred %v", rows[1].TraceID, rows[1].TraceInferred)
	}
	if len(forwarded) != 2 || len(forwarded[0]) != len(inferredTraceparent) || forwarded[0][3:35] != inferredTraceID {
		t.Fatalf("the target did not receive the inferred trace: %q", forwarded)
	}
}
