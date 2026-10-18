package outboundai

// adr: 1436 — apid lists a day's usage by tag with each tag's limit and
// replaces an integration's per-tag daily cost limits.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTagUsageAndLimitEndpoints(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	ctx := context.Background()
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, integrationID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/outbound/integrations/x/ai-tag-limits"
	for _, body := range []string{
		`{"limits":[{"tag":"feature","daily_cost_limit_millicents":5}]}`,
		`{"limits":[{"tag":"feature=search","daily_cost_limit_millicents":0}]}`,
		`{"limits":[{"tag":"feature=search","daily_cost_limit_millicents":5},{"tag":"feature=search","daily_cost_limit_millicents":6}]}`,
		`{"limits":[{"tag":"feature=~other","daily_cost_limit_millicents":5}]}`,
	} {
		if w := serve(service.PutTagLimits, acct, http.MethodPut, path, integrationID, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := serve(service.PutTagLimits, acct, http.MethodPut, path, integrationID,
		`{"limits":[{"tag":"tier=free","daily_cost_limit_millicents":900},{"tag":"feature=search","daily_cost_limit_millicents":500}]}`); w.Code != http.StatusNoContent {
		t.Fatalf("PUT limits = %d %s", w.Code, w.Body.String())
	}
	w := serve(service.GetTagLimits, acct, http.MethodGet, path, integrationID, "")
	var limits api.OutboundAITagLimits
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &limits) != nil || len(limits.Limits) != 2 || limits.Limits[0].Tag != "feature=search" {
		t.Fatalf("GET limits = %d %s", w.Code, w.Body.String())
	}

	today := time.Now().UTC().Format("2006-01-02")
	store.SeedOutboundAITagUsage(integrationID, today,
		state.OutboundAITagUsageItem{Tag: "tier=free", Requests: 3, InputTokens: 30, OutputTokens: 9, CostMillicents: 120},
		state.OutboundAITagUsageItem{Tag: "feature=search", Requests: 2, InputTokens: 10, OutputTokens: 4, CostMillicents: 400},
		state.OutboundAITagUsageItem{Tag: "feature=~other", Requests: 1, CostMillicents: 1})
	usagePath := "/v1/outbound/integrations/x/ai-usage/tags"
	w = serve(service.GetTagUsage, acct, http.MethodGet, usagePath, integrationID, "")
	var usage api.OutboundAITagUsageResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &usage) != nil || usage.TrackedTags != 3 || len(usage.Items) != 3 ||
		usage.Items[0].Tag != "feature=search" || *usage.Items[0].DailyCostLimitMillicents != 500 || usage.Items[1].Tokens != 39 ||
		usage.Items[2].DailyCostLimitMillicents != nil {
		t.Fatalf("GET tag usage = %d %s", w.Code, w.Body.String())
	}
	w = serve(service.GetTagUsage, acct, http.MethodGet, usagePath+"?key=feature&limit=1", integrationID, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &usage) != nil || len(usage.Items) != 1 || usage.Items[0].Tag != "feature=search" {
		t.Fatalf("one key = %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"?key=Feature", "?date=2001-01-01", "?limit=0"} {
		if w := serve(service.GetTagUsage, acct, http.MethodGet, usagePath+query, integrationID, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", query, w.Code)
		}
	}
	other, err := store.CreateAccount(ctx, "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(service.GetTagUsage, other, http.MethodGet, usagePath, integrationID, ""); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("another account's usage = %d", w.Code)
	}
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, integrationID, state.OutboundAIPolicy{}); err != nil {
		t.Fatal(err)
	}
	if w := serve(service.PutTagLimits, acct, http.MethodPut, path, integrationID,
		`{"limits":[{"tag":"tier=free","daily_cost_limit_millicents":900}]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("limits without an AI provider = %d", w.Code)
	}
}
