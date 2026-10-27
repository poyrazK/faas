package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// aiBoundsHandler is an OpenAI integration whose request timeout is short,
// for the call bounds of ADR 20261027132936.
func aiBoundsHandler(t *testing.T, upstream *httptest.Server, timeout time.Duration) (*httptest.Server, *MemoryBackend) {
	t.Helper()
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, timeout)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = api.OutboundAIProviderOpenAI
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	return gateway, backend
}

func postAIBoundsCall(t *testing.T, gateway *httptest.Server, body string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, gateway.URL+"/i/integration-1/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(TokenHeader, "token")
	req.Header.Set(AppHeader, "app-1")
	return gateway.Client().Do(req)
}

// waitForAIUsage waits for the call's settlement to reach the rollup.
func waitForAIUsage(t *testing.T, backend *MemoryBackend, want func(AIUsageTotals) bool) AIUsageTotals {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		totals, _ := backend.AIUsageRollup("integration-1", "app-1", "gpt-4o")
		if want(totals) {
			return totals
		}
		if time.Now().After(deadline) {
			t.Fatalf("usage was not settled as expected: %+v", totals)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAICallStreamOutlivesRequestTimeout(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(700 * time.Millisecond)
		_, _ = io.WriteString(w, "data: {\"model\":\"gpt-4o\",\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	gateway, backend := aiBoundsHandler(t, upstream, 300*time.Millisecond)

	resp, err := postAIBoundsCall(t, gateway, `{"model":"gpt-4o","stream":true}`)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("stream past the request timeout = %q, %v; want the whole answer", body, err)
	}
	waitForAIUsage(t, backend, func(totals AIUsageTotals) bool { return totals.InputTokens == 2 && totals.OutputTokens == 3 })
}

func TestAICallHeaderWaitTimesOutAndSettlesReservation(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer upstream.Close()
	gateway, backend := aiBoundsHandler(t, upstream, 200*time.Millisecond)

	started := time.Now()
	resp, err := postAIBoundsCall(t, gateway, `{"model":"gpt-4o","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || time.Since(started) > 2*time.Second {
		t.Fatalf("status = %d after %s; want 502 at the header timeout", resp.StatusCode, time.Since(started))
	}
	totals := waitForAIUsage(t, backend, func(totals AIUsageTotals) bool { return totals.UsageUnknownRequests == 1 })
	if totals.OutputTokens < 100 {
		t.Fatalf("unanswered call settled at %+v; want its reservation", totals)
	}
}

func TestAICallSilentStreamEndsAtIdleBound(t *testing.T) {
	previous := aiStreamIdleBound
	aiStreamIdleBound = 200 * time.Millisecond
	t.Cleanup(func() { aiStreamIdleBound = previous })
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer upstream.Close()
	gateway, backend := aiBoundsHandler(t, upstream, 5*time.Second)

	started := time.Now()
	resp, err := postAIBoundsCall(t, gateway, `{"model":"gpt-4o","stream":true,"max_tokens":50}`)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(body), "[DONE]") || time.Since(started) > 2*time.Second {
		t.Fatalf("silent stream = %q after %s; want it cut at the idle bound", body, time.Since(started))
	}
	waitForAIUsage(t, backend, func(totals AIUsageTotals) bool { return totals.UsageUnknownRequests == 1 })
}

func TestMemoryBackendRenewLeaseKeepsLiveLease(t *testing.T) {
	backend := NewMemoryBackend()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	backend.SetClock(func() time.Time { return now })
	spec := AdmissionSpec{IntegrationID: "integration-1", RatePerSecond: 100, Burst: 10, MaxInFlight: 1, LeaseTTL: time.Second}
	ctx := context.Background()
	first, err := backend.Admit(ctx, spec)
	if err != nil || !first.Granted {
		t.Fatalf("first admission = %+v, %v", first, err)
	}
	now = now.Add(900 * time.Millisecond)
	if err := backend.RenewLease(ctx, "integration-1", first.LeaseID, time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(900 * time.Millisecond)
	if second, err := backend.Admit(ctx, spec); err != nil || second.Granted {
		t.Fatalf("admission past the original expiry = %+v, %v; want the renewed lease to hold the slot", second, err)
	}
	now = now.Add(2 * time.Second)
	if err := backend.RenewLease(ctx, "integration-1", first.LeaseID, time.Second); err != nil {
		t.Fatal(err)
	}
	if third, err := backend.Admit(ctx, spec); err != nil || !third.Granted {
		t.Fatalf("admission after the lease expired = %+v, %v; an expired lease must not come back", third, err)
	}
}
