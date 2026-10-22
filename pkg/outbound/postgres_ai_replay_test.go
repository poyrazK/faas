package outbound_test

// adr: 997 — apid records a replay of a captured call within the daily
// limit; outboundd claims it, writes its results, and prunes it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAIReplays(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-ai-replay-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	integration := func(name, provider string) string {
		t.Helper()
		offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
			ID: uuid.NewString(), AccountID: account.ID, Name: name,
			Origin: "https://api.example.com", AllowedMethods: []string{http.MethodPost},
			AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
			CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
			RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, state.OutboundAIPolicy{Provider: provider,
			RequestLogEnabled: true, RequestLogContent: true}); err != nil {
			t.Fatal(err)
		}
		return offer.ID
	}
	source, anthropic := integration("source", api.OutboundAIProviderOpenAI), integration("claude", api.OutboundAIProviderAnthropic)
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	settle := func(content *outbound.AIRequestContent) string {
		t.Helper()
		if err := backend.RecordAIUsage(ctx, outbound.AIUsageRecord{IntegrationID: source, Caller: outbound.AIUsageCallerRuns,
			Outcome: outbound.AIUsageOutcomeKnown, Usage: outbound.AIUsage{Model: "gpt-4o", Known: true, InputTokens: 10, OutputTokens: 2},
			Log: &outbound.AIRequestLog{Status: http.StatusOK, DurationMS: 40, Attempts: 1, Content: content}}); err != nil {
			t.Fatal(err)
		}
		log, err := store.ListOutboundAIRequestLog(ctx, account.ID, source, nil, 1)
		if err != nil || len(log) != 1 {
			t.Fatalf("request log = %+v, %v", log, err)
		}
		return log[0].ID
	}
	truncated := settle(&outbound.AIRequestContent{RequestPath: "/v1/chat/completions", Request: []byte(`{"model"`), RequestTruncated: true})
	logID := settle(&outbound.AIRequestContent{RequestPath: "/v1/chat/completions", Request: []byte(`{"model":"gpt-4o","messages":[]}`),
		Response: []byte(`{"choices":[{"message":{"content":"original"}}]}`), ResponseContentType: "application/json"})
	targets := []state.OutboundAIReplayTarget{{IntegrationID: anthropic, Model: "claude-sonnet-4-5"}, {IntegrationID: source}}
	for name, tc := range map[string]struct {
		replay state.OutboundAIReplay
		want   error
	}{
		"truncated capture": {state.OutboundAIReplay{AccountID: account.ID, IntegrationID: source, LogID: truncated, Targets: targets}, state.ErrOutboundAIReplayUnavailable},
		"unknown call":      {state.OutboundAIReplay{AccountID: account.ID, IntegrationID: source, LogID: uuid.NewString(), Targets: targets}, state.ErrNotFound},
		"model missing": {state.OutboundAIReplay{AccountID: account.ID, IntegrationID: source, LogID: logID,
			Targets: []state.OutboundAIReplayTarget{{IntegrationID: anthropic}}}, state.ErrInvalidArgument},
		"foreign target": {state.OutboundAIReplay{AccountID: account.ID, IntegrationID: source, LogID: logID,
			Targets: []state.OutboundAIReplayTarget{{IntegrationID: uuid.NewString()}}}, state.ErrInvalidArgument},
	} {
		if _, err := store.CreateOutboundAIReplay(ctx, tc.replay); !errors.Is(err, tc.want) {
			t.Errorf("%s = %v, want %v", name, err, tc.want)
		}
	}
	created, err := store.CreateOutboundAIReplay(ctx, state.OutboundAIReplay{AccountID: account.ID, IntegrationID: source, LogID: logID, Targets: targets})
	if err != nil || created.State != api.OutboundAIReplayPending {
		t.Fatalf("create = %+v, %v", created, err)
	}
	job, ok, err := backend.ClaimAIReplay(ctx)
	if err != nil || !ok || job.ID != created.ID || job.SourceModel != "gpt-4o" || job.RequestPath != "/v1/chat/completions" ||
		len(job.Targets) != 2 || job.Targets[0].Model != "claude-sonnet-4-5" || job.Targets[1].Model != "" {
		t.Fatalf("claim = %+v, %v, %v", job, ok, err)
	}
	if _, again, err := backend.ClaimAIReplay(ctx); again || err != nil {
		t.Fatalf("a claimed replay was claimed again: %v, %v", again, err)
	}
	status := http.StatusOK
	ttfb := int64(120)
	if err := backend.FinishAIReplay(ctx, job, "original", []outbound.AIReplayResult{
		{IntegrationID: anthropic, Model: "claude-sonnet-4-5", Status: status, Outcome: outbound.AIUsageOutcomeKnown,
			Usage: outbound.AIUsage{InputTokens: 12, OutputTokens: 5}, CostMillicents: 17, DurationMS: 800, TTFBMS: &ttfb,
			ResponseBody: []byte(`{"ok":true}`), ResponseContentType: "application/json", OutputText: "Bonjour"},
		{IntegrationID: source, ErrorCode: "outbound_budget_exhausted", Status: http.StatusTooManyRequests},
	}, ""); err != nil {
		t.Fatal(err)
	}
	replay, err := store.GetOutboundAIReplay(ctx, account.ID, created.ID)
	if err != nil || replay.State != api.OutboundAIReplayDone || replay.SourceOutputText != "original" || replay.Source.Model != "gpt-4o" ||
		len(replay.Results) != 2 || replay.Results[0].OutputText != "Bonjour" || replay.Results[0].CostMillicents != 17 ||
		*replay.Results[0].Status != status || replay.Results[1].ErrorCode != "outbound_budget_exhausted" || replay.FinishedAt == nil {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	if _, err := store.GetOutboundAIReplay(ctx, uuid.NewString(), created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account read the replay: %v", err)
	}
	// The daily limit counts every replay of the integration.
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbound_ai_replays (id, account_id, integration_id, log_id, target_integration_ids, target_models, state)
		SELECT gen_random_uuid(), $1, $2, $3, ARRAY[$2::uuid], ARRAY[''], 'done' FROM generate_series(2, $4::int)`,
		account.ID, source, logID, api.OutboundAIReplaysPerDay); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOutboundAIReplay(ctx, state.OutboundAIReplay{AccountID: account.ID, IntegrationID: source, LogID: logID,
		Targets: targets[1:]}); !errors.Is(err, state.ErrOutboundAIReplayLimit) {
		t.Fatalf("a replay past the daily limit = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_ai_replays SET created_at = now() - interval '4 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOutboundAIReplay(ctx, account.ID, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a replay past retention = %v", err)
	}
}
