package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryBackendRetryBudgetIsSharedAndRefills(t *testing.T) {
	ctx := context.Background()
	backend := NewMemoryBackend()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	backend.SetClock(func() time.Time { return now })
	spec := AdmissionSpec{
		IntegrationID: "integration-retry-budget", RatePerSecond: 100, Burst: 20, MaxInFlight: 20,
		RetryBudgetPerMinute: 2, LeaseTTL: time.Minute,
	}
	if decision, err := backend.Admit(ctx, spec); err != nil || !decision.Granted || decision.RetryBudgetPerMinute != 2 {
		t.Fatalf("initial admission = %+v, %v", decision, err)
	}
	for token := 0; token < 2; token++ {
		if allowed, err := backend.ConsumeRetryToken(ctx, spec.IntegrationID, spec.RetryBudgetPerMinute); err != nil || !allowed {
			t.Fatalf("consume initial token %d = %v, %v", token, allowed, err)
		}
	}
	if allowed, err := backend.ConsumeRetryToken(ctx, spec.IntegrationID, spec.RetryBudgetPerMinute); err != nil || allowed {
		t.Fatalf("consume exhausted bucket = %v, %v; want false", allowed, err)
	}

	now = now.Add(30 * time.Second)
	if allowed, err := backend.ConsumeRetryToken(ctx, spec.IntegrationID, spec.RetryBudgetPerMinute); err != nil || !allowed {
		t.Fatalf("consume refilled token = %v, %v; want true", allowed, err)
	}
	if allowed, err := backend.ConsumeRetryToken(ctx, spec.IntegrationID, spec.RetryBudgetPerMinute); err != nil || allowed {
		t.Fatalf("consume before next refill = %v, %v; want false", allowed, err)
	}

	// A policy change starts a fresh bucket at the new capacity, including when
	// the previous policy had already spent its available tokens.
	spec.RetryBudgetPerMinute = 1
	if decision, err := backend.Admit(ctx, spec); err != nil || !decision.Granted {
		t.Fatalf("admission after policy change = %+v, %v", decision, err)
	}
	if allowed, err := backend.ConsumeRetryToken(ctx, spec.IntegrationID, spec.RetryBudgetPerMinute); err != nil || !allowed {
		t.Fatalf("consume token after policy change = %v, %v; want true", allowed, err)
	}
}

func TestHandlerRetryBudgetCapsAggregateExtraAttempts(t *testing.T) {
	var providerCalls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "provider unavailable")
	}))
	defer provider.Close()

	integration := testIntegration(t, provider.URL, "secret", []string{"app-1"}, 100, 10, 10)
	integration.MaxRetries = 2
	integration.RetryBudgetPerMinute = 1
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}

	for request := 0; request < 2; request++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "provider unavailable") {
			t.Fatalf("request %d response = %d %q; want provider's last 503 response", request, response.Code, response.Body.String())
		}
	}
	if got := providerCalls.Load(); got != 3 {
		t.Fatalf("provider attempts = %d, want 3 (one extra attempt across two logical calls)", got)
	}
}
