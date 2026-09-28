package outbound

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryBackendCircuitBreakerTripsAndAllowsOneProbe(t *testing.T) {
	ctx := context.Background()
	backend := NewMemoryBackend()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	backend.SetClock(func() time.Time { return now })
	spec := AdmissionSpec{
		IntegrationID: "integration-circuit", RatePerSecond: 100, Burst: 20, MaxInFlight: 20,
		CircuitBreakerFailureThreshold: 2, CircuitBreakerOpenSeconds: 10, LeaseTTL: time.Minute,
	}

	for attempt := 0; attempt < 2; attempt++ {
		decision, err := backend.Admit(ctx, spec)
		if err != nil || !decision.Granted {
			t.Fatalf("admission %d = %+v, %v", attempt, decision, err)
		}
		gate, err := backend.AllowCircuit(ctx, spec.IntegrationID, decision.LeaseID, 2, 10)
		if err != nil || !gate.Allowed || gate.Probe {
			t.Fatalf("closed gate %d = %+v, %v", attempt, gate, err)
		}
		if err := backend.RecordCircuitOutcome(ctx, spec.IntegrationID, decision.LeaseID, 2, 10, CircuitOutcomeFailure); err != nil {
			t.Fatalf("record failure %d: %v", attempt, err)
		}
		if err := backend.Release(ctx, spec.IntegrationID, decision.LeaseID); err != nil {
			t.Fatalf("release %d: %v", attempt, err)
		}
	}

	blockedLease, err := backend.Admit(ctx, spec)
	if err != nil || !blockedLease.Granted {
		t.Fatalf("open-circuit request admission = %+v, %v", blockedLease, err)
	}
	blocked, err := backend.AllowCircuit(ctx, spec.IntegrationID, blockedLease.LeaseID, 2, 10)
	if err != nil || blocked.Allowed || blocked.Probe || blocked.RetryAfter != 10*time.Second {
		t.Fatalf("open gate = %+v, %v; want 10-second rejection", blocked, err)
	}
	if err := backend.Release(ctx, spec.IntegrationID, blockedLease.LeaseID); err != nil {
		t.Fatal(err)
	}

	now = now.Add(10 * time.Second)
	probeLease, err := backend.Admit(ctx, spec)
	if err != nil || !probeLease.Granted {
		t.Fatalf("half-open probe admission = %+v, %v", probeLease, err)
	}
	probe, err := backend.AllowCircuit(ctx, spec.IntegrationID, probeLease.LeaseID, 2, 10)
	if err != nil || !probe.Allowed || !probe.Probe {
		t.Fatalf("half-open gate = %+v, %v; want one probe", probe, err)
	}

	competingLease, err := backend.Admit(ctx, spec)
	if err != nil || !competingLease.Granted {
		t.Fatalf("competing admission = %+v, %v", competingLease, err)
	}
	competing, err := backend.AllowCircuit(ctx, spec.IntegrationID, competingLease.LeaseID, 2, 10)
	if err != nil || competing.Allowed || competing.Probe || competing.RetryAfter <= 0 {
		t.Fatalf("competing half-open gate = %+v, %v; want rejection until probe completes", competing, err)
	}

	if err := backend.RecordCircuitOutcome(ctx, spec.IntegrationID, probeLease.LeaseID, 2, 10, CircuitOutcomeSuccess); err != nil {
		t.Fatal(err)
	}
	if err := backend.Release(ctx, spec.IntegrationID, probeLease.LeaseID); err != nil {
		t.Fatal(err)
	}
	if err := backend.Release(ctx, spec.IntegrationID, competingLease.LeaseID); err != nil {
		t.Fatal(err)
	}

	closedLease, err := backend.Admit(ctx, spec)
	if err != nil || !closedLease.Granted {
		t.Fatalf("post-probe admission = %+v, %v", closedLease, err)
	}
	closed, err := backend.AllowCircuit(ctx, spec.IntegrationID, closedLease.LeaseID, 2, 10)
	if err != nil || !closed.Allowed || closed.Probe {
		t.Fatalf("successful probe did not close circuit: %+v, %v", closed, err)
	}
}

func TestOutboundCircuitOutcomeUsesFinalLogicalResponse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   CircuitBreakerOutcome
	}{
		{name: "transient status", status: http.StatusServiceUnavailable, want: CircuitOutcomeFailure},
		{name: "rate limited", status: http.StatusTooManyRequests, want: CircuitOutcomeFailure},
		{name: "provider validation", status: http.StatusBadRequest, want: CircuitOutcomeSuccess},
		{name: "provider authorization", status: http.StatusUnauthorized, want: CircuitOutcomeSuccess},
		{name: "success", status: http.StatusOK, want: CircuitOutcomeSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outboundCircuitOutcome(&http.Response{StatusCode: tc.status}, nil); got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
	if got := outboundCircuitOutcome(nil, context.Canceled); got != CircuitOutcomeNeutral {
		t.Fatalf("canceled request outcome = %q, want neutral", got)
	}
}

func TestHandlerCircuitBreakerStopsProviderCallsAfterThreshold(t *testing.T) {
	var providerCalls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer provider.Close()

	integration := testIntegration(t, provider.URL, "secret", []string{"app-1"}, 100, 10, 10)
	integration.CircuitBreakerFailureThreshold = 1
	integration.CircuitBreakerOpenSeconds = 10
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	if first.Code != http.StatusServiceUnavailable || first.Header().Get("X-Gregale-Outbound-Rejection") != "" {
		t.Fatalf("first provider response = %d, rejection=%q", first.Code, first.Header().Get("X-Gregale-Outbound-Rejection"))
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	if second.Code != http.StatusServiceUnavailable || second.Header().Get("X-Gregale-Outbound-Rejection") != ReasonCircuitOpen {
		t.Fatalf("open-circuit response = %d, rejection=%q, body=%s", second.Code, second.Header().Get("X-Gregale-Outbound-Rejection"), second.Body.String())
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Fatal("open-circuit response has no Retry-After")
	}
	if got := providerCalls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want only the initial failing call", got)
	}
}
