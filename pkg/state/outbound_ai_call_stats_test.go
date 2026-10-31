package state_test

// adr: 20261022145950 — the dashboard's latency read summarises an
// integration's recent logged calls by model, busiest first, in Postgres
// and in memory.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type callStatsStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateOutboundIntegration(context.Context, state.OutboundIntegrationOffer) (state.OutboundIntegrationOffer, error)
	SetOutboundAIPolicy(context.Context, string, string, state.OutboundAIPolicy) error
	GetOutboundAICallStats(context.Context, string, string) ([]state.OutboundAICallStats, int64, error)
}

func callStatsIntegration(t *testing.T, store callStatsStore) (state.Account, string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "stats-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: acct.ID, Name: "llm", Origin: "https://api.openai.com",
		AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer",
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 10, Burst: 10, MaxInFlight: 10, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, offer.ID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI,
		RequestLogEnabled: true}); err != nil {
		t.Fatal(err)
	}
	return acct, offer.ID
}

// checkCallStats checks ten gpt-5 calls (TTFB 100..1000 ms, durations
// 1..10 s, one error) and one gpt-5-mini call without a first byte.
func checkCallStats(t *testing.T, store callStatsStore, acct state.Account, integrationID string) {
	t.Helper()
	stats, calls, err := store.GetOutboundAICallStats(context.Background(), acct.ID, integrationID)
	if err != nil || calls != 11 || len(stats) != 2 {
		t.Fatalf("stats = %+v, %d, %v", stats, calls, err)
	}
	busy, quiet := stats[0], stats[1]
	if busy.Model != "gpt-5" || busy.Calls != 10 || busy.ErrorCalls != 1 || busy.TTFBP50MS == nil || *busy.TTFBP50MS < 500 ||
		*busy.TTFBP50MS > 600 || *busy.TTFBP95MS < 900 || *busy.DurationP95MS < 9_000 {
		t.Fatalf("gpt-5 = %+v", busy)
	}
	if quiet.Model != "gpt-5-mini" || quiet.TTFBP50MS != nil || quiet.DurationP50MS == nil {
		t.Fatalf("gpt-5-mini = %+v", quiet)
	}
	if _, _, err := store.GetOutboundAICallStats(context.Background(), uuid.NewString(), integrationID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account = %v", err)
	}
}

func TestMemOutboundAICallStats(t *testing.T) {
	store := state.NewMemStore()
	acct, integrationID := callStatsIntegration(t, store)
	now := time.Now().UTC()
	for index := range 10 {
		ttfb := int64(index+1) * 100
		status := 200
		if index == 0 {
			status = 502
		}
		store.SeedOutboundAIRequestLog(integrationID, state.OutboundAIRequestLogEntry{ID: uuid.NewString(),
			CreatedAt: now.Add(-time.Duration(index) * time.Minute), Model: "gpt-5", Status: status, Outcome: "known",
			DurationMS: int64(index+1) * 1_000, TTFBMS: &ttfb})
	}
	store.SeedOutboundAIRequestLog(integrationID,
		state.OutboundAIRequestLogEntry{ID: uuid.NewString(), CreatedAt: now, Model: "gpt-5-mini", Status: 200, Outcome: "known", DurationMS: 50},
		state.OutboundAIRequestLogEntry{ID: uuid.NewString(), CreatedAt: now.Add(-48 * time.Hour), Model: "old", Status: 200, Outcome: "known"})
	checkCallStats(t, store, acct, integrationID)
}

func TestPgOutboundAICallStats(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	acct, integrationID := callStatsIntegration(t, s)
	for index := range 10 {
		status, outcome := 200, "known"
		if index == 0 {
			status, outcome = 502, "error"
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO outbound_ai_request_log
			    (integration_id, caller, model, status, outcome, input_tokens, cached_input_tokens, cache_write_tokens, output_tokens,
			     cost_millicents, duration_ms, ttfb_ms, attempts, created_at)
			VALUES ($1, 'runs', 'gpt-5', $2, $3, 0, 0, 0, 0, 0, $4, $5, 1, now() - make_interval(mins => $6::int))`,
			integrationID, status, outcome, (index+1)*1_000, (index+1)*100, index); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO outbound_ai_request_log (integration_id, caller, model, status, outcome, input_tokens, cached_input_tokens,
		     cache_write_tokens, output_tokens, cost_millicents, duration_ms, attempts)
		 VALUES ($1, 'runs', 'gpt-5-mini', 200, 'known', 0, 0, 0, 0, 0, 50, 1)`,
		`INSERT INTO outbound_ai_request_log (integration_id, caller, model, status, outcome, input_tokens, cached_input_tokens,
		     cache_write_tokens, output_tokens, cost_millicents, duration_ms, attempts, created_at)
		 VALUES ($1, 'runs', 'old', 200, 'known', 0, 0, 0, 0, 0, 50, 1, now() - interval '48 hours')`,
	} {
		if _, err := pool.Exec(ctx, statement, integrationID); err != nil {
			t.Fatal(err)
		}
	}
	checkCallStats(t, s, acct, integrationID)
}
