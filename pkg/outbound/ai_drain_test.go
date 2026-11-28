package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt 2026-11-28: a stopping outboundd waited 25 s and exited under AI
// calls that may run 30 minutes; their leases expired unsettled and their
// spend never reached the caps. DrainAICalls ends the calls still running
// before the grace ends, and each settles at its reservation.
func TestDrainAICallsSettlesCallsStillRunning(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer upstream.Close()
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
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
	defer gateway.Close()

	// Nothing in flight: the drain returns at once, not at the cut.
	idle, cancelIdle := context.WithTimeout(context.Background(), time.Minute)
	started := time.Now()
	handler.DrainAICalls(idle)
	cancelIdle()
	if time.Since(started) > time.Second {
		t.Fatalf("idle drain took %s", time.Since(started))
	}

	streamed := make(chan struct{})
	go func() {
		defer close(streamed)
		resp, err := postAIBoundsCall(t, gateway, `{"model":"gpt-4o","stream":true,"max_tokens":100}`)
		if err == nil {
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for handler.aiDrain.inflight.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the call never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A grace of the settle window plus a little: the cut comes almost at
	// once and the drain returns once the call has settled.
	ctx, cancel := context.WithTimeout(context.Background(), aiDrainSettleWindow+300*time.Millisecond)
	defer cancel()
	started = time.Now()
	handler.DrainAICalls(ctx)
	if handler.aiDrain.inflight.Load() != 0 || time.Since(started) > aiDrainSettleWindow {
		t.Fatalf("drain returned after %s with %d calls in flight", time.Since(started), handler.aiDrain.inflight.Load())
	}
	totals := waitForAIUsage(t, backend, func(totals AIUsageTotals) bool { return totals.UsageUnknownRequests == 1 })
	if totals.OutputTokens < 100 {
		t.Fatalf("a call cut at shutdown settled at %+v; want its reservation", totals)
	}
	<-streamed
}
