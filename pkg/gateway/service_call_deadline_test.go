// adr: 20261102082606
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCallerDeadline(t *testing.T) {
	started := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	inferred := started.Add(5 * time.Second)
	for _, tc := range []struct {
		name      string
		forwarded []string
		inferred  time.Time
		want      time.Time
		wantKnown bool
	}{
		{name: "forwarded wins", forwarded: []string{"1500"}, inferred: inferred, want: started.Add(1500 * time.Millisecond), wantKnown: true},
		{name: "inferred", inferred: inferred, want: inferred, wantKnown: true},
		{name: "invalid forwarded falls back", forwarded: []string{"soon"}, inferred: inferred, want: inferred, wantKnown: true},
		{name: "repeated forwarded falls back", forwarded: []string{"100", "200"}, inferred: inferred, want: inferred, wantKnown: true},
		{name: "past the longest plan budget", forwarded: []string{"999999999"}},
		{name: "nothing known"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/internal/services/worker/", nil)
			for _, v := range tc.forwarded {
				r.Header.Add(api.DeadlineHeader, v)
			}
			got, known := callerDeadline(r, tc.inferred, started)
			if known != tc.wantKnown || !got.Equal(tc.want) {
				t.Fatalf("callerDeadline = %v, %v; want %v, %v", got, known, tc.want, tc.wantKnown)
			}
		})
	}
}

// vmmd is asked at most once, and only for what the call lacks.
func TestWithInferredCallerAsksOnlyForWhatIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name            string
		callerDeadlines bool
		traceparent     bool
		forwarded       bool
		wantAsked       bool
		wantDeadline    bool
	}{
		{name: "nothing forwarded", callerDeadlines: true, wantAsked: true, wantDeadline: true},
		{name: "trace forwarded, deadline wanted", callerDeadlines: true, traceparent: true, wantAsked: true, wantDeadline: true},
		{name: "both forwarded", callerDeadlines: true, traceparent: true, forwarded: true},
		{name: "deadlines off, trace forwarded", traceparent: true},
		{name: "deadlines off, trace missing", wantAsked: true, wantDeadline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asks := 0
			deadline := time.Now().Add(time.Minute)
			p := &ServiceProxy{callerDeadlines: tc.callerDeadlines, inferCallerRequest: func(context.Context, string) InferredCallerRequest {
				asks++
				return InferredCallerRequest{Traceparent: inferredTraceparent, Deadline: deadline}
			}}
			r := httptest.NewRequest(http.MethodGet, "/v1/internal/services/worker/", nil)
			if tc.traceparent {
				r.Header.Set("Traceparent", callerTraceparent)
			}
			if tc.forwarded {
				r.Header.Set(api.DeadlineHeader, "1000")
			}
			_, got := p.withInferredCaller(r)
			if asks > 1 || (asks == 1) != tc.wantAsked || got.IsZero() == tc.wantDeadline {
				t.Fatalf("asked %d times, deadline %v", asks, got)
			}
		})
	}
}

// A service call is bounded by the calling request's remaining budget, and is
// not made at all once that budget has run out (ADR-20261102082606).
func TestServiceCallInheritsCallerDeadline(t *testing.T) {
	accountID, appID := uuid.NewString(), uuid.NewString()
	newProxy := func(callerDeadlines bool, deadline time.Time, forwarded *[]time.Duration) *ServiceProxy {
		return NewServiceProxy(ServiceProxyConfig{
			Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID,
				Endpoints: []ServiceEndpoint{{InstanceID: "worker-1", NodeID: "node-1", Port: 8080}}}},
			Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
				return ServiceTarget{AppID: appID}, true, nil
			},
			Authorize: func(context.Context, string, string) (ServiceCaller, error) {
				return ServiceCaller{AppID: "caller", AccountID: accountID}, nil
			},
			InferCallerRequest: func(context.Context, string) InferredCallerRequest {
				return InferredCallerRequest{Deadline: deadline}
			},
			CallerDeadlines: callerDeadlines,
			Forward: func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					left := time.Duration(-1)
					if d, ok := r.Context().Deadline(); ok {
						left = time.Until(d)
					}
					*forwarded = append(*forwarded, left)
					recordForwardedFirstByte(r.Context())
					w.WriteHeader(http.StatusOK)
				})
			},
		})
	}
	call := func(p *ServiceProxy) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/internal/services/worker/", nil)
		req.Header.Set(ServiceProxyCallerAppHeader, "caller")
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		return rec
	}

	var forwarded []time.Duration
	if rec := call(newProxy(true, time.Now().Add(2*time.Second), &forwarded)); rec.Code != http.StatusOK {
		t.Fatalf("live caller deadline: %d %s", rec.Code, rec.Body)
	}
	if len(forwarded) != 1 || forwarded[0] <= time.Second || forwarded[0] > 2*time.Second-api.ServiceCallDeadlineHopOverhead {
		t.Fatalf("the call was not bounded by the caller's 2 s: %v", forwarded)
	}

	forwarded = nil
	rec := call(newProxy(true, time.Now().Add(-time.Second), &forwarded))
	if rec.Code != http.StatusGatewayTimeout || rec.Header().Get(api.ErrorCodeHeader) != api.CodeRequestBudgetExceeded || len(forwarded) != 0 {
		t.Fatalf("expired caller: %d %q, forwarded %v", rec.Code, rec.Header().Get(api.ErrorCodeHeader), forwarded)
	}

	forwarded = nil
	if rec := call(newProxy(false, time.Now().Add(-time.Second), &forwarded)); rec.Code != http.StatusOK || len(forwarded) != 1 {
		t.Fatalf("switch off: %d, forwarded %v", rec.Code, forwarded)
	}
	if forwarded[0] >= 0 && forwarded[0] <= time.Second {
		t.Fatalf("switch off still bounded the call: %v", forwarded[0])
	}
}
