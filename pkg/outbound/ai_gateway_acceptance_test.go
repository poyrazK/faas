package outbound

// adr: 998 — preview acceptance for the AI gateway: a Pro account's
// integration, stored in Postgres, serves an app's AI calls through the
// relay with redaction, settlement, cost, capture and a hard cap, and the
// staged rollout and plan entitlement close it for other accounts.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAIGatewayPreviewAcceptance(t *testing.T) {
	var forwarded []string
	dailyTokens := int64(100)
	f := newAIPreviewFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		forwarded = append(forwarded, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":20,"completion_tokens":10}}`))
	}, state.OutboundAIPolicy{DailyTokenLimit: &dailyTokens, RequestLogEnabled: true, RequestLogContent: true})
	ctx := context.Background()
	if err := f.store.SetOutboundAIContentPolicy(ctx, f.account.ID, f.offer.ID, api.OutboundAIContentPolicy{Email: api.OutboundAIContentRedact}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetOutboundAIPrices(ctx, f.account.ID, f.offer.ID, []api.OutboundAIPriceEntry{
		{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: 250_000, OutputMillicentsPerMTok: 1_000_000}}); err != nil {
		t.Fatal(err)
	}
	handler := f.handler(nil, nil)
	first := f.call(ctx, handler, `{"model":"gpt-4o","max_tokens":10,"messages":[{"role":"user","content":"mail ana@example.com"}]}`)
	if first.Code != http.StatusOK || first.Header().Get(AIContentRedactionsHeader) != "email=1" ||
		len(forwarded) != 1 || strings.Contains(forwarded[0], "ana@example.com") {
		t.Fatalf("first call = %d %q, forwarded %q", first.Code, first.Header().Get(AIContentRedactionsHeader), forwarded)
	}
	usage, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, "")
	if err != nil || len(usage.Items) != 1 || usage.Items[0].InputTokens != 20 || usage.Items[0].OutputTokens != 10 ||
		usage.DailyTokenCount != 30 || usage.DailyCostMillicents != 15 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	log, err := f.store.ListOutboundAIRequestLog(ctx, f.account.ID, f.offer.ID, nil, 10)
	if err != nil || len(log) != 1 || !log[0].ContentCaptured || log[0].Caller != f.app.ID {
		t.Fatalf("request log = %+v, %v", log, err)
	}
	content, err := f.store.GetOutboundAIRequestContent(ctx, f.account.ID, f.offer.ID, log[0].ID)
	if err != nil || strings.Contains(string(content.RequestBody), "ana@example.com") || content.RequestPath != "/v1/chat/completions" {
		t.Fatalf("captured content = %+v, %v", content, err)
	}
	// The daily cap is hard: a call whose reservation would pass it is
	// refused before it reaches the provider.
	if capped := f.call(ctx, handler, `{"model":"gpt-4o","max_tokens":500,"messages":[]}`); capped.Code != http.StatusTooManyRequests || len(forwarded) != 1 {
		t.Fatalf("a call past the daily cap = %d, %d forwarded", capped.Code, len(forwarded))
	}
	// The staged rollout and the plan close the gateway for other accounts.
	other, err := ParseAIGatewayRollout([]string{uuid.NewString()}, "")
	if err != nil {
		t.Fatal(err)
	}
	if refused := f.call(ctx, f.handler(other, nil), `{"model":"gpt-4o","max_tokens":1,"messages":[]}`); refused.Code != http.StatusForbidden ||
		!strings.Contains(refused.Body.String(), "outbound_ai_gateway_unavailable") {
		t.Fatalf("an account outside the rollout = %d %s", refused.Code, refused.Body.String())
	}
	if _, err := f.pool.Exec(ctx, `UPDATE accounts SET plan = 'free' WHERE id = $1`, f.account.ID); err != nil {
		t.Fatal(err)
	}
	if refused := f.call(ctx, f.handler(nil, nil), `{"model":"gpt-4o","max_tokens":1,"messages":[]}`); refused.Code != http.StatusForbidden {
		t.Fatalf("a Free account = %d %s", refused.Code, refused.Body.String())
	}
	if len(forwarded) != 1 {
		t.Fatalf("refused calls reached the provider: %d forwarded", len(forwarded))
	}
}
