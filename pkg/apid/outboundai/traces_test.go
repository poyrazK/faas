package outboundai

// adr: 20261020202035 — apid stores per-trace limits in the AI policy,
// lists an integration's AI traces newest first with an app, end-user and
// cursor filter, and shows one trace with its logged calls in order.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPutPolicyStoresTraceLimits(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	path := "/v1/outbound/integrations/x/ai-policy"
	for _, body := range []string{
		`{"ai_provider":"","trace_max_calls":5}`,
		`{"ai_provider":"openai","trace_max_calls":0}`,
		`{"ai_provider":"openai","trace_max_calls":1000001}`,
		`{"ai_provider":"openai","trace_max_repeated_tool_calls":1001}`,
		`{"ai_provider":"openai","trace_max_cost_millicents":-1}`,
	} {
		if w := serve(service.PutPolicy, acct, http.MethodPut, path, integrationID, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", body, w.Code)
		}
	}
	if w := serve(service.PutPolicy, acct, http.MethodPut, path, integrationID,
		`{"ai_provider":"openai","trace_max_calls":50,"trace_max_cost_millicents":200000,"trace_max_repeated_tool_calls":3}`); w.Code != http.StatusNoContent {
		t.Fatalf("trace limits = %d %s", w.Code, w.Body.String())
	}
	offers, err := store.ListOutboundIntegrationOffers(context.Background(), acct.ID)
	if err != nil || len(offers) != 1 || offers[0].AITraceMaxCalls == nil || *offers[0].AITraceMaxCalls != 50 ||
		*offers[0].AITraceMaxCostMillicents != 200_000 || *offers[0].AITraceMaxRepeatedToolCalls != 3 {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	if w := serve(service.PutPolicy, acct, http.MethodPut, path, integrationID, `{"ai_provider":"openai"}`); w.Code != http.StatusNoContent {
		t.Fatalf("clearing = %d", w.Code)
	}
	if offers, _ := store.ListOutboundIntegrationOffers(context.Background(), acct.ID); offers[0].AITraceMaxCalls != nil {
		t.Fatal("an omitted trace limit was kept")
	}
}

func serveTrace(service *Service, acct state.Account, integrationID, traceID string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/v1/outbound/integrations/x/ai-traces/"+url.PathEscape(traceID), nil)
	r.SetPathValue("integration", integrationID)
	r.SetPathValue("trace", traceID)
	w := httptest.NewRecorder()
	service.GetTrace(w, r, acct)
	return w
}

func TestTraceEndpoints(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	app := uuid.NewString()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	store.SeedOutboundAITrace(integrationID,
		state.OutboundAITrace{TraceID: "task-1", Caller: app, EndUser: "u_1", FirstAt: base, LastAt: base.Add(3 * time.Second), Calls: 2,
			InputTokens: 30, CostMillicents: 400, ToolCalls: 1, RepeatedToolCalls: 1, Models: []string{"gpt-5"}},
		state.OutboundAITrace{TraceID: "task-2", Caller: "access_token", FirstAt: base, LastAt: base.Add(2 * time.Second), Calls: 1},
		state.OutboundAITrace{TraceID: "task-3", Caller: app, FirstAt: base, LastAt: base.Add(time.Second), Calls: 1})
	ttfb := int64(40)
	store.SeedOutboundAIRequestLog(integrationID,
		state.OutboundAIRequestLogEntry{ID: uuid.NewString(), CreatedAt: base.Add(3 * time.Second), Caller: app, Model: "gpt-5", Status: 200,
			Outcome: "known", DurationMS: 90, TraceID: "task-1"},
		state.OutboundAIRequestLogEntry{ID: uuid.NewString(), CreatedAt: base, Caller: app, Model: "gpt-5", Status: 200, Outcome: "known",
			InputTokens: 30, CostMillicents: 400, DurationMS: 120, TTFBMS: &ttfb, TraceID: "task-1", ToolCalls: []string{"lookup"}},
		state.OutboundAIRequestLogEntry{ID: uuid.NewString(), CreatedAt: base, Caller: app, Model: "gpt-5", Status: 200, Outcome: "known",
			TraceID: "task-3"})

	listPath := "/v1/outbound/integrations/x/ai-traces"
	w := serve(service.ListTraces, acct, http.MethodGet, listPath+"?limit=2", integrationID, "")
	var page api.OutboundAITraceList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 2 || page.Items[0].TraceID != "task-1" ||
		page.Items[0].AppID == nil || *page.Items[0].AppID != app || *page.Items[0].EndUser != "u_1" || page.Items[1].AppID != nil ||
		page.NextBefore == nil || !page.NextBefore.Equal(base.Add(2*time.Second)) {
		t.Fatalf("first page = %d %s", w.Code, w.Body.String())
	}
	w = serve(service.ListTraces, acct, http.MethodGet, listPath+"?limit=2&before="+url.QueryEscape(page.NextBefore.Format(time.RFC3339Nano)),
		integrationID, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].TraceID != "task-3" ||
		page.NextBefore != nil {
		t.Fatalf("second page = %d %s", w.Code, w.Body.String())
	}
	for query, want := range map[string]string{"?app_id=" + app: "task-1,task-3", "?end_user=u_1": "task-1"} {
		w := serve(service.ListTraces, acct, http.MethodGet, listPath+query, integrationID, "")
		var filtered api.OutboundAITraceList
		var got []string
		_ = json.Unmarshal(w.Body.Bytes(), &filtered)
		for _, item := range filtered.Items {
			got = append(got, item.TraceID)
		}
		if w.Code != http.StatusOK || strings.Join(got, ",") != want {
			t.Errorf("%s = %d %v", query, w.Code, got)
		}
	}
	for _, query := range []string{"?limit=0", "?limit=201", "?before=yesterday", "?app_id=nope", "?end_user=has%20space"} {
		if w := serve(service.ListTraces, acct, http.MethodGet, listPath+query, integrationID, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", query, w.Code)
		}
	}

	w = serveTrace(service, acct, integrationID, "task-1")
	var detail api.OutboundAITraceResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Trace.Calls != 2 || len(detail.Calls) != 2 ||
		detail.CallsTruncated || strings.Join(detail.Calls[0].ToolCalls, ",") != "lookup" || detail.Calls[0].TimeToFirstByteMS == nil ||
		*detail.Calls[0].TimeToFirstByteMS != 40 || detail.Calls[1].ToolCalls == nil || len(detail.Calls[1].ToolCalls) != 0 ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("trace = %d %s", w.Code, w.Body.String())
	}
	for _, traceID := range []string{"missing", "has space", strings.Repeat("a", api.OutboundAIUserIDMaxBytes+1)} {
		if w := serveTrace(service, acct, integrationID, traceID); w.Code != http.StatusNotFound {
			t.Errorf("%q = %d", traceID, w.Code)
		}
	}

	w = serve(service.ListRequestLog, acct, http.MethodGet, "/v1/outbound/integrations/x/ai-requests", integrationID, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"trace_id":"task-1"`) || !strings.Contains(w.Body.String(), `"tool_calls":["lookup"]`) {
		t.Fatalf("request log = %d %s", w.Code, w.Body.String())
	}

	other, err := store.CreateAccount(context.Background(), "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(service.ListTraces, other, http.MethodGet, listPath, integrationID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another account's traces = %d", w.Code)
	}
	if w := serveTrace(service, other, integrationID, "task-1"); w.Code != http.StatusNotFound {
		t.Fatalf("another account's trace = %d", w.Code)
	}
}
