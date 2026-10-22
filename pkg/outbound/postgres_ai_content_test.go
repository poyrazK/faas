package outbound_test

// adr: 996 — Postgres stores a logged call's captured bodies in the
// settlement, at most the capped number per integration and UTC day, and
// the prune drops them once capture is turned off.

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

func TestPostgresAIRequestContentCapture(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-ai-content-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "ai-content",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodPost},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, state.OutboundAIPolicy{
		Provider: api.OutboundAIProviderOpenAI, RequestLogContent: true,
	}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("content capture without the request log = %v", err)
	}
	policy := state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI, RequestLogEnabled: true, RequestLogContent: true}
	if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, policy); err != nil {
		t.Fatal(err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := resolver.Integration(ctx, offer.ID); err != nil || !resolved.AIRequestLogContent {
		t.Fatalf("resolved content capture = %v, %v", resolved.AIRequestLogContent, err)
	}
	if offers, err := store.ListOutboundIntegrationOffers(ctx, account.ID); err != nil || len(offers) != 1 || !offers[0].AIRequestLogContent {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	settle := func(content *outbound.AIRequestContent) {
		t.Helper()
		if err := backend.RecordAIUsage(ctx, outbound.AIUsageRecord{IntegrationID: offer.ID, Caller: outbound.AIUsageCallerRuns,
			Outcome: outbound.AIUsageOutcomeKnown, Usage: outbound.AIUsage{Model: "gpt-4o", Known: true, InputTokens: 3, OutputTokens: 1},
			Log: &outbound.AIRequestLog{Status: http.StatusOK, DurationMS: 5, Attempts: 1, Content: content}}); err != nil {
			t.Fatal(err)
		}
	}
	settle(&outbound.AIRequestContent{Request: []byte(`{"messages":[]}`), Response: []byte("data: {}\n\n"), ResponseTruncated: true,
		ResponseContentType: "text/event-stream"})
	settle(nil)
	log, err := store.ListOutboundAIRequestLog(ctx, account.ID, offer.ID, nil, 10)
	if err != nil || len(log) != 2 {
		t.Fatalf("request log = %+v, %v", log, err)
	}
	captured, plain := log[1], log[0]
	if !captured.ContentCaptured || plain.ContentCaptured {
		captured, plain = log[0], log[1]
	}
	if !captured.ContentCaptured || plain.ContentCaptured {
		t.Fatalf("content_captured flags = %v, %v", log[0].ContentCaptured, log[1].ContentCaptured)
	}
	content, err := store.GetOutboundAIRequestContent(ctx, account.ID, offer.ID, captured.ID)
	if err != nil || string(content.RequestBody) != `{"messages":[]}` || content.RequestTruncated || string(content.ResponseBody) != "data: {}\n\n" ||
		!content.ResponseTruncated || content.ResponseContentType != "text/event-stream" || content.LogID != captured.ID {
		t.Fatalf("content = %+v, %v", content, err)
	}
	if _, err := store.GetOutboundAIRequestContent(ctx, account.ID, offer.ID, plain.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("an uncaptured call = %v", err)
	}
	if _, err := store.GetOutboundAIRequestContent(ctx, uuid.NewString(), offer.ID, captured.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account read the content: %v", err)
	}
	// The day's cap: fill it, then a further capture keeps only metadata.
	if _, err := pool.Exec(ctx, `
		WITH logs AS (
		    INSERT INTO outbound_ai_request_log (integration_id, caller, model, status, outcome, duration_ms, attempts)
		    SELECT $1, 'runs', 'gpt-4o', 200, 'known', 1, 1 FROM generate_series(2, $2::int)
		    RETURNING id)
		INSERT INTO outbound_ai_request_content (log_id, integration_id, request_body, request_truncated, response_body, response_truncated)
		SELECT id, $1, '\x7b7d'::bytea, false, ''::bytea, false FROM logs`, offer.ID, api.OutboundAIContentCapturesPerDay); err != nil {
		t.Fatal(err)
	}
	settle(&outbound.AIRequestContent{Request: []byte("{}")})
	var captures int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_ai_request_content WHERE integration_id = $1`, offer.ID).Scan(&captures); err != nil ||
		captures != api.OutboundAIContentCapturesPerDay {
		t.Fatalf("captures past the daily cap = %d, %v", captures, err)
	}
	// Turning capture off drops what was captured at the next prune.
	policy.RequestLogContent = false
	if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOutboundAIRequestContent(ctx, account.ID, offer.ID, captured.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("content after capture was turned off = %v", err)
	}
	if log, err := store.ListOutboundAIRequestLog(ctx, account.ID, offer.ID, nil, 1); err != nil || len(log) != 1 {
		t.Fatalf("the prune dropped request-log metadata: %+v, %v", log, err)
	}
}
