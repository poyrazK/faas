package outbound

// adr: 1120 — Postgres settlements are keyed by the admission lease and
// pruned after the retry window, admission reports the breaker and cooldown
// it read, and one AI call costs a bounded number of round trips.

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func okChatCompletion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":20,"completion_tokens":10}}`))
}

func TestPostgresAISettlementIsKeyedByItsLease(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := backend.Admit(ctx, f.spec())
	if err != nil || !decision.Granted {
		t.Fatalf("admission = %+v, %v", decision, err)
	}
	record := AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown,
		Usage:   AIUsage{Model: "gpt-4o", Known: true, InputTokens: 20, OutputTokens: 10},
		LeaseID: decision.LeaseID, Log: &AIRequestLog{Status: http.StatusOK, Attempts: 1}}
	// The second settlement is a retry whose first attempt committed.
	for range 2 {
		if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, "")
	if err != nil || len(usage.Items) != 1 || usage.Items[0].Requests != 1 || usage.DailyTokenCount != 30 {
		t.Fatalf("usage after a retried settlement = %+v, %v", usage, err)
	}
	var logged, leases, keys int
	if err := f.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM outbound_ai_request_log WHERE integration_id = $1),
		       (SELECT count(*) FROM outbound_admission_leases WHERE integration_id = $1),
		       (SELECT count(*) FROM outbound_ai_settlements WHERE integration_id = $1)`, f.offer.ID).
		Scan(&logged, &leases, &keys); err != nil {
		t.Fatal(err)
	}
	if logged != 1 || leases != 0 || keys != 1 {
		t.Fatalf("after a retried settlement: %d logged, %d live leases, %d keys", logged, leases, keys)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_settlements SET settled_at = now() - make_interval(mins => $1::int + 1)`,
		api.OutboundAISettlementKeyRetentionMinutes); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM outbound_ai_settlements`).Scan(&keys); err != nil || keys != 0 {
		t.Fatalf("%d settlement keys outlived the retention, %v", keys, err)
	}
}

func TestPostgresAdmissionReportsTheBreakerAndCooldown(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	if err := f.store.SetOutboundRequestPolicy(context.Background(), f.account.ID, f.offer.ID, api.OutboundRequestPolicy{
		RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000,
		CircuitBreakerFailureThreshold: 1, CircuitBreakerOpenSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	testAdmissionReportsTheBreakerAndCooldown(t, backend, f.spec())
}

func TestAIGatewayRoundTripBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		circuit bool
		// budget is the most round trips one call may take: one for the
		// resolver, five for admission, four for settlement, one for the
		// lease release, and one for a breaker outcome.
		budget float64
	}{
		{name: "without a circuit breaker", budget: 11},
		{name: "with a circuit breaker", circuit: true, budget: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true})
			ctx := context.Background()
			policy := api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000}
			if tc.circuit {
				policy.CircuitBreakerFailureThreshold, policy.CircuitBreakerOpenSeconds = 5, 30
			}
			if err := f.store.SetOutboundRequestPolicy(ctx, f.account.ID, f.offer.ID, policy); err != nil {
				t.Fatal(err)
			}
			pool, tracer := f.tracedPool(2)
			handler := f.handlerOn(pool, nil, nil)
			body := `{"model":"gpt-4o","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
			// The first call adopts the policy revision and prepares statements.
			if got := f.call(ctx, handler, body); got.Code != http.StatusOK {
				t.Fatalf("first call = %d %s", got.Code, got.Body.String())
			}
			const calls = 5
			tracer.roundTrips.Store(0)
			for i := range calls {
				if got := f.call(ctx, handler, body); got.Code != http.StatusOK {
					t.Fatalf("call %d = %d %s", i, got.Code, got.Body.String())
				}
			}
			perCall := float64(tracer.roundTrips.Load()) / calls
			t.Logf("%.1f Postgres round trips per call", perCall)
			if perCall > tc.budget {
				t.Fatalf("%.1f round trips per call, budget %.0f", perCall, tc.budget)
			}
		})
	}
}
