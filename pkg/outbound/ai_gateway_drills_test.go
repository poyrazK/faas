package outbound

// adr: 998 — preview failure drills: what an AI integration's callers and
// budgets see when the provider, Postgres or a replay fails, and when the
// operator closes the gate as the rollback.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// failingSettlement is Postgres admission with a settlement write that
// fails, as when Postgres goes away mid-call.
type failingSettlement struct{ *PostgresBackend }

func (failingSettlement) RecordAIUsage(context.Context, AIUsageRecord) error {
	return errors.New("postgres: connection refused")
}

func (failingSettlement) RecordAIUsageAlerts(context.Context, AIUsageRecord) ([]AIBudgetAlert, error) {
	return nil, errors.New("postgres: connection refused")
}

func TestAIGatewayFailureDrills(t *testing.T) {
	providerStatus := http.StatusOK
	forwarded := 0
	f := newAIPreviewFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		forwarded++
		w.Header().Set("Content-Type", "application/json")
		if providerStatus != http.StatusOK {
			w.WriteHeader(providerStatus)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`))
	}, state.OutboundAIPolicy{RequestLogEnabled: true, RequestLogContent: true})
	ctx := context.Background()
	body := `{"model":"gpt-4o","max_tokens":5,"messages":[{"role":"user","content":"hi"}]}`
	dailyTokens := func() int64 {
		t.Helper()
		usage, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		return usage.DailyTokenCount
	}

	t.Run("Postgres down at admission fails closed", func(t *testing.T) {
		unreachable, err := pgxpool.New(ctx, "postgres://faas@127.0.0.1:1/unreachable?connect_timeout=1")
		if err != nil {
			t.Fatal(err)
		}
		defer unreachable.Close()
		backend, err := NewPostgresBackend(unreachable)
		if err != nil {
			t.Fatal(err)
		}
		before := forwarded
		got := f.call(ctx, f.handler(nil, backend), body)
		if got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "outbound_admission_unavailable") || forwarded != before {
			t.Fatalf("admission without Postgres = %d %s, %d forwarded", got.Code, got.Body.String(), forwarded-before)
		}
	})

	t.Run("a failed settlement keeps the response and is counted", func(t *testing.T) {
		backend, err := NewPostgresBackend(f.pool)
		if err != nil {
			t.Fatal(err)
		}
		handler := f.handler(nil, failingSettlement{backend})
		if handler.Metrics, err = NewMetrics(prometheus.NewRegistry()); err != nil {
			t.Fatal(err)
		}
		got := f.call(ctx, handler, body)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"ok"`) {
			t.Fatalf("call with a failing settlement = %d %s", got.Code, got.Body.String())
		}
		if failed := testutil.ToFloat64(handler.Metrics.aiUsageRecordEvents.WithLabelValues("customer_managed", "state_error")); failed != 1 {
			t.Fatalf("outbound_ai_usage_record_events_total{event=state_error} = %v", failed)
		}
		if tokens := dailyTokens(); tokens != 0 {
			t.Fatalf("a failed settlement counted %d tokens", tokens)
		}
	})

	t.Run("closing the gate is the rollback", func(t *testing.T) {
		handler := f.handler(nil, nil)
		handler.AIGatewayEnabled = false
		before := forwarded
		got := f.call(ctx, handler, body)
		if got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "outbound_ai_gateway_disabled") || forwarded != before {
			t.Fatalf("a call with the gate closed = %d %s", got.Code, got.Body.String())
		}
	})

	t.Run("an interrupted replay fails instead of hanging", func(t *testing.T) {
		if got := f.call(ctx, f.handler(nil, nil), body); got.Code != http.StatusOK {
			t.Fatalf("captured call = %d", got.Code)
		}
		log, err := f.store.ListOutboundAIRequestLog(ctx, f.account.ID, f.offer.ID, nil, 1)
		if err != nil || len(log) != 1 {
			t.Fatalf("request log = %+v, %v", log, err)
		}
		replayID := uuid.NewString()
		if _, err := f.pool.Exec(ctx, `
			INSERT INTO outbound_ai_replays (id, account_id, integration_id, log_id, target_integration_ids, target_models, state, started_at)
			VALUES ($1, $2, $3, $4, ARRAY[$3::uuid], ARRAY[''], 'running', now() - interval '20 minutes')`,
			replayID, f.account.ID, f.offer.ID, log[0].ID); err != nil {
			t.Fatal(err)
		}
		backend, err := NewPostgresBackend(f.pool)
		if err != nil {
			t.Fatal(err)
		}
		if _, claimed, err := backend.ClaimAIReplay(ctx); claimed || err != nil {
			t.Fatalf("claim = %v, %v", claimed, err)
		}
		replay, err := f.store.GetOutboundAIReplay(ctx, f.account.ID, replayID)
		if err != nil || replay.State != api.OutboundAIReplayFailed || replay.ErrorCode != "outbound_replay_interrupted" {
			t.Fatalf("interrupted replay = %+v, %v", replay, err)
		}
	})

	t.Run("a provider outage opens the circuit and costs nothing", func(t *testing.T) {
		providerStatus = http.StatusServiceUnavailable
		defer func() { providerStatus = http.StatusOK }()
		handler := f.handler(nil, nil)
		tokensBefore := dailyTokens()
		for i := range 3 {
			if got := f.call(ctx, handler, body); got.Code != http.StatusServiceUnavailable {
				t.Fatalf("outage call %d = %d %s", i, got.Code, got.Body.String())
			}
		}
		before := forwarded
		got := f.call(ctx, handler, body)
		if got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "outbound_circuit_breaker_open") || forwarded != before {
			t.Fatalf("a call with the circuit open = %d %s, %d forwarded", got.Code, got.Body.String(), forwarded-before)
		}
		if tokens := dailyTokens(); tokens != tokensBefore {
			t.Fatalf("failed calls counted %d tokens", tokens-tokensBefore)
		}
		log, err := f.store.ListOutboundAIRequestLog(ctx, f.account.ID, f.offer.ID, nil, 1)
		if err != nil || len(log) != 1 || log[0].Status != http.StatusServiceUnavailable || log[0].Outcome != string(AIUsageOutcomeError) {
			t.Fatalf("last logged call = %+v, %v", log, err)
		}
	})
}
