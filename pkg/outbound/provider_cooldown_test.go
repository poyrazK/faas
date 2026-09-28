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

func TestMemoryBackendProviderCooldownIsSharedMonotonicAndExpires(t *testing.T) {
	ctx := context.Background()
	backend := NewMemoryBackend()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	backend.SetClock(func() time.Time { return now })
	spec := AdmissionSpec{IntegrationID: "provider-cooldown", RatePerSecond: 100, Burst: 20, MaxInFlight: 20, LeaseTTL: time.Minute}
	policyRevision := int64(7)
	decision, err := backend.Admit(ctx, spec)
	if err != nil || !decision.Granted {
		t.Fatalf("admission = %+v, %v", decision, err)
	}
	if err := backend.RecordProviderCooldown(ctx, spec.IntegrationID, policyRevision, 10*time.Second); err != nil {
		t.Fatalf("record initial cooldown: %v", err)
	}
	if err := backend.RecordProviderCooldown(ctx, spec.IntegrationID, policyRevision, time.Second); err != nil {
		t.Fatalf("record shorter cooldown: %v", err)
	}
	blocked, err := backend.AllowProviderRequest(ctx, spec.IntegrationID, policyRevision)
	if err != nil || blocked.Allowed || blocked.RetryAfter != 10*time.Second {
		t.Fatalf("provider gate = %+v, %v; want a 10-second shared cooldown", blocked, err)
	}
	now = now.Add(10 * time.Second)
	allowed, err := backend.AllowProviderRequest(ctx, spec.IntegrationID, policyRevision)
	if err != nil || !allowed.Allowed {
		t.Fatalf("provider gate after expiry = %+v, %v; want allowed", allowed, err)
	}
	if err := backend.RecordProviderCooldown(ctx, spec.IntegrationID, policyRevision, 10*time.Second); err != nil {
		t.Fatalf("record cooldown before policy change: %v", err)
	}
	updatedPolicy, err := backend.AllowProviderRequest(ctx, spec.IntegrationID, policyRevision+1)
	if err != nil || !updatedPolicy.Allowed {
		t.Fatalf("provider gate after policy revision change = %+v, %v; want allowed", updatedPolicy, err)
	}
}

func TestHandlerSharesProviderRetryAfterAndPreserves429(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, "provider rate limit")
	}))
	defer provider.Close()

	integration := testIntegration(t, provider.URL, "secret", []string{"app-1"}, 100, 10, 10)
	integration.MaxRetries = 2
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handlerA, err := NewHandler(resolver, backend, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	handlerB, err := NewHandler(resolver, backend, provider.Client())
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	handlerA.ServeHTTP(first, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	if first.Code != http.StatusTooManyRequests || first.Body.String() != "provider rate limit" || first.Header().Get("Retry-After") != "5" {
		t.Fatalf("first response = %d headers=%v body=%q; want provider 429 unchanged", first.Code, first.Header(), first.Body.String())
	}
	if first.Header().Get("X-Gregale-Outbound-Rejection") != "" {
		t.Fatalf("provider response was marked as a gateway rejection: %q", first.Header().Get("X-Gregale-Outbound-Rejection"))
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider attempts after Retry-After=5s = %d, want 1", got)
	}

	second := httptest.NewRecorder()
	handlerB.ServeHTTP(second, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	if second.Code != http.StatusTooManyRequests || second.Header().Get("X-Gregale-Outbound-Rejection") != ReasonProviderCooldown {
		t.Fatalf("cooldown response = %d rejection=%q body=%q", second.Code, second.Header().Get("X-Gregale-Outbound-Rejection"), second.Body.String())
	}
	if got := second.Header().Get("Retry-After"); got == "" || got == "0" {
		t.Fatalf("cooldown response Retry-After = %q; want positive seconds", got)
	}
	if !strings.Contains(second.Body.String(), "outbound_provider_cooldown") {
		t.Fatalf("cooldown response body = %q; want provider-cooldown problem", second.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider attempts after shared cooldown = %d, want 1", got)
	}
}

func TestHandlerServesCachedResponseDuringProviderCooldown(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "cached provider response")
	}))
	defer provider.Close()

	integration := testIntegration(t, provider.URL, "secret", []string{"app-1"}, 100, 10, 10)
	integration.ResponseCacheTTLSeconds = 30
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
		return rr
	}

	first := request()
	if first.Code != http.StatusOK || first.Body.String() != "cached provider response" {
		t.Fatalf("initial response = %d %q", first.Code, first.Body.String())
	}
	if err := backend.RecordProviderCooldown(context.Background(), integration.ID, integration.PolicyRevision, 10*time.Second); err != nil {
		t.Fatalf("record provider cooldown: %v", err)
	}
	second := request()
	if second.Code != http.StatusOK || second.Body.String() != "cached provider response" {
		t.Fatalf("cached response during cooldown = %d %q", second.Code, second.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want cached second request to bypass provider", got)
	}
}

func TestProviderRetryAfterParserBoundsAndAcceptsDates(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		value string
		want  time.Duration
		ok    bool
	}{
		{name: "seconds", value: "30", want: 30 * time.Second, ok: true},
		{name: "date", value: now.Add(45 * time.Second).Format(http.TimeFormat), want: 45 * time.Second, ok: true},
		{name: "past date", value: now.Add(-time.Second).Format(http.TimeFormat), want: 0, ok: true},
		{name: "zero", value: "0", want: 0, ok: true},
		{name: "large numeric capped", value: "999999999999999999999999", want: maxProviderCooldown, ok: true},
		{name: "invalid", value: "tomorrow", want: 0, ok: false},
		{name: "missing", value: "", want: 0, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseRetryAfter(tc.value, now)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseRetryAfter(%q) = %s, %t; want %s, %t", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}
