// adr: 201
package gateway

// H4-68: the public path consults the shared instance-health breaker. Before
// this, kind=circuit_breaker rules were stored and listed as enabled but no
// request ever reached a breaker.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/circuit"
)

func circuitTestGroup() *circuit.Group {
	return circuit.NewGroup(circuit.Config{
		FailureThreshold: 0.5, MinRequests: 2, Window: 10 * time.Second,
		OpenDuration: 30 * time.Second, MaxOpenDuration: 30 * time.Second,
	}, nil)
}

func TestHandlerCircuitRecordsTransportFailuresAndSkipsTheOpenInstance(t *testing.T) {
	h, _, forwards := retryTestHandler(t)
	g := circuitTestGroup()
	h.WithCircuitBreaker(g)

	// Round-robin alternates dead, live: two transport failures open the
	// dead instance's circuit (MinRequests 2, ratio 1.0).
	for i := 0; i < 4; i++ {
		_ = retryRequest(t, h)
	}
	if got := g.State(circuitKey("app-1", "instance-dead")); got != circuit.StateOpen {
		t.Fatalf("dead instance circuit = %s, want open after two transport failures", got)
	}
	if got := g.State(circuitKey("app-1", "instance-live")); got != circuit.StateClosed {
		t.Fatalf("live instance circuit = %s, want closed", got)
	}
	before := forwards.Load()
	for i := 0; i < 6; i++ {
		if rec := retryRequest(t, h); rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 from the live sibling", i, rec.Code)
		}
	}
	if got := forwards.Load() - before; got != 6 {
		t.Fatalf("forwards = %d, want 6 (one per request, never to the open instance)", got)
	}
}

func TestHandlerCircuitOpenEverywhereFailsFast(t *testing.T) {
	h, _, forwards := retryTestHandler(t)
	g := circuitTestGroup()
	h.WithCircuitBreaker(g)
	for _, id := range []string{"instance-dead", "instance-live"} {
		g.Failure(circuitKey("app-1", id))
		g.Failure(circuitKey("app-1", id))
	}
	rec := retryRequest(t, h)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status = %d Retry-After = %q, want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != api.CodeCircuitOpen {
		t.Fatalf("problem code = %q (err %v), want %q", p.Code, err, api.CodeCircuitOpen)
	}
	if got := forwards.Load(); got != 0 {
		t.Fatalf("forwards = %d, want 0 while every circuit is open", got)
	}
}

func TestHandlerWithoutBreakerIsUnchanged(t *testing.T) {
	h, _, forwards := retryTestHandler(t)
	for i := 0; i < 4; i++ {
		_ = retryRequest(t, h)
	}
	if got := forwards.Load(); got != 4 {
		t.Fatalf("forwards = %d, want 4 with the breaker off", got)
	}
}

func TestCircuitConfigsTuneFromTheAppsFirstRule(t *testing.T) {
	loaded := make(chan struct{}, 4)
	fail := false
	c := NewCircuitConfigs(func(_ context.Context, appID string) ([]EdgeRuleCircuitBreakerResolved, error) {
		defer func() { loaded <- struct{}{} }()
		if fail {
			return nil, errors.New("store unavailable")
		}
		if appID != "tuned" {
			return nil, nil
		}
		return []EdgeRuleCircuitBreakerResolved{
			{Priority: 10, FailureThreshold: 0.25, MinRequests: 3, Window: 20 * time.Second, OpenDuration: 15 * time.Second, MaxOpenDuration: 120 * time.Second},
			{Priority: 20, FailureThreshold: 0.9, MinRequests: 9},
		}, nil
	}, nil)
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }
	if _, ok := c.ForKey("tuned\x00i1"); ok {
		t.Fatal("tuned before the first load finished; lookups must not block")
	}
	<-loaded
	// The source signals before CircuitConfigs.load stores its result. Wait
	// until the public lookup observes that store instead of racing that last
	// few instructions.
	var cfg circuit.Config
	var ok bool
	deadline := time.Now().Add(time.Second)
	for {
		cfg, ok = c.ForKey("tuned\x00i1")
		if ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ok || cfg.FailureThreshold != 0.25 || cfg.MinRequests != 3 || cfg.OpenDuration != 15*time.Second {
		t.Fatalf("config = %+v ok=%v, want the priority-10 rule", cfg, ok)
	}
	if _, ok := c.For("plain"); ok {
		t.Fatal("an app without rules was tuned")
	}
	<-loaded
	if _, ok := c.For("plain"); ok {
		t.Fatal("an app without rules was tuned after its load")
	}
	// A failed refresh keeps the last known tuning.
	fail = true
	now = now.Add(circuitConfigTTL)
	_, _ = c.For("tuned")
	<-loaded
	if cfg, ok := c.For("tuned"); !ok || cfg.MinRequests != 3 {
		t.Fatalf("after a failed refresh config = %+v ok=%v, want the previous tuning", cfg, ok)
	}
}
