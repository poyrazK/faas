package apidserver_test

// adr: 996 — content capture is opt-in on top of the request log, and a
// captured call's bodies read back as text or base64.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOutboundAIRequestContent(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := seedAIIntegration(t, e, "llm", api.OutboundAIProviderOpenAI)
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, "/v1/outbound/integrations/"+id+"/ai", api.PutOutboundAIPolicyRequest{
		AIProvider: api.OutboundAIProviderOpenAI, RequestLogContent: true,
	}, nil), http.StatusBadRequest, api.CodeValidation)
	on := e.Do(t, http.MethodPut, "/v1/outbound/integrations/"+id+"/ai", api.PutOutboundAIPolicyRequest{
		AIProvider: api.OutboundAIProviderOpenAI, RequestLogEnabled: true, RequestLogContent: true,
	}, nil)
	if on.Code != http.StatusNoContent || !firstOutboundOffer(t, e).AIRequestLogContent {
		t.Fatalf("content capture policy = %d %s", on.Code, on.Body.String())
	}
	logID, plainID := uuid.NewString(), uuid.NewString()
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	(*e.Store()).SeedOutboundAIRequestLog(id,
		state.OutboundAIRequestLogEntry{ID: logID, CreatedAt: at, Caller: "runs", Model: "gpt-4o", Status: 200, Outcome: "known", Attempts: 1},
		state.OutboundAIRequestLogEntry{ID: plainID, CreatedAt: at.Add(-time.Minute), Caller: "runs", Model: "gpt-4o", Status: 200, Outcome: "known", Attempts: 1})
	// "é" is cut in half by the capture; the text keeps the whole runes.
	(*e.Store()).SeedOutboundAIRequestContent(id, state.OutboundAIRequestContent{LogID: logID, CreatedAt: at,
		RequestBody: []byte(`{"messages":[{"content":"caf` + "\xc3"), RequestTruncated: true,
		ResponseBody: []byte{0xff, 0x00}, ResponseContentType: "application/octet-stream"})
	var page api.OutboundAIRequestLogList
	listed := e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-requests", nil, nil)
	if json.Unmarshal(listed.Body.Bytes(), &page) != nil || len(page.Items) != 2 || !page.Items[0].ContentCaptured || page.Items[1].ContentCaptured {
		t.Fatalf("request log = %s", listed.Body.String())
	}
	got := e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-requests/"+logID+"/content", nil, nil)
	var content api.OutboundAIRequestContent
	if got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(got.Body.Bytes(), &content) != nil {
		t.Fatalf("content = %d %s", got.Code, got.Body.String())
	}
	if content.RequestID != logID || content.Request.Body != `{"messages":[{"content":"caf` || content.Request.Encoding != api.OutboundAIContentEncodingUTF8 ||
		!content.Request.Truncated || content.Request.ContentType != nil ||
		content.Response.Encoding != api.OutboundAIContentEncodingBase64 || content.Response.Body != base64.StdEncoding.EncodeToString([]byte{0xff, 0x00}) ||
		content.Response.ContentType == nil || *content.Response.ContentType != "application/octet-stream" {
		t.Fatalf("content = %+v", content)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-requests/"+plainID+"/content", nil, nil),
		http.StatusNotFound, api.CodeNotFound)
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-requests/not-a-uuid/content", nil, nil),
		http.StatusBadRequest, api.CodeValidation)
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+uuid.NewString()+"/ai-requests/"+logID+"/content", nil, nil),
		http.StatusNotFound, api.CodeNotFound)
}

// adr: 997 — an integration's content policy is set through apid and shown
// on its offer; an active policy needs an AI provider.
func TestOutboundAIContentPolicy(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := seedAIIntegration(t, e, "llm", api.OutboundAIProviderOpenAI)
	path := "/v1/outbound/integrations/" + id + "/ai-content-policy"
	for name, policy := range map[string]api.OutboundAIContentPolicy{
		"action":    {Email: "mask"},
		"duplicate": {BlockedTerms: []string{"a", "A"}},
	} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodPut, path, policy, nil), http.StatusBadRequest, api.CodeValidation)
		_ = name
	}
	set := e.Do(t, http.MethodPut, path, api.OutboundAIContentPolicy{Email: api.OutboundAIContentRedact, BlockedTerms: []string{"Falcon"}}, nil)
	if set.Code != http.StatusNoContent {
		t.Fatalf("set = %d %s", set.Code, set.Body.String())
	}
	if offer := firstOutboundOffer(t, e); offer.AIContentPolicy.Email != api.OutboundAIContentRedact || offer.AIContentPolicy.Secret != api.OutboundAIContentOff ||
		len(offer.AIContentPolicy.BlockedTerms) != 1 {
		t.Fatalf("offer policy = %+v", offer.AIContentPolicy)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, "/v1/outbound/integrations/"+uuid.NewString()+"/ai-content-policy",
		api.OutboundAIContentPolicy{}, nil), http.StatusNotFound, api.CodeNotFound)
	if off := e.Do(t, http.MethodPut, "/v1/outbound/integrations/"+id+"/ai", api.PutOutboundAIPolicyRequest{}, nil); off.Code != http.StatusNoContent {
		t.Fatalf("AI off = %d %s", off.Code, off.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, path, api.OutboundAIContentPolicy{Secret: api.OutboundAIContentBlock}, nil),
		http.StatusBadRequest, api.CodeValidation)
}

// adr: 997 — a captured call is replayed through apid: the replay is
// recorded for outboundd and read back with its results.
func TestOutboundAIReplay(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	source := seedAIIntegration(t, e, "llm", api.OutboundAIProviderOpenAI)
	claude := seedAIIntegration(t, e, "claude", api.OutboundAIProviderAnthropic)
	logID, bare := uuid.NewString(), uuid.NewString()
	at := time.Now().UTC()
	(*e.Store()).SeedOutboundAIRequestLog(source,
		state.OutboundAIRequestLogEntry{ID: logID, CreatedAt: at, Caller: "runs", Model: "gpt-4o", Status: 200, Outcome: "known", Attempts: 1,
			InputTokens: 10, OutputTokens: 2, CostMillicents: 3, DurationMS: 400},
		state.OutboundAIRequestLogEntry{ID: bare, CreatedAt: at, Caller: "runs", Model: "gpt-4o", Status: 200, Outcome: "known", Attempts: 1})
	(*e.Store()).SeedOutboundAIRequestContent(source, state.OutboundAIRequestContent{LogID: logID, CreatedAt: at,
		RequestPath: "/v1/chat/completions", RequestBody: []byte(`{"model":"gpt-4o","messages":[]}`)})
	path := "/v1/outbound/integrations/" + source + "/ai-requests/" + logID + "/replays"
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, path, api.CreateOutboundAIReplayRequest{}, nil), http.StatusBadRequest, api.CodeValidation)
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, path, api.CreateOutboundAIReplayRequest{
		Targets: []api.OutboundAIReplayTarget{{IntegrationID: claude}}}, nil), http.StatusBadRequest, api.CodeValidation)
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/outbound/integrations/"+source+"/ai-requests/"+bare+"/replays",
		api.CreateOutboundAIReplayRequest{Targets: []api.OutboundAIReplayTarget{{IntegrationID: source}}}, nil),
		http.StatusConflict, "outbound_ai_replay_unavailable")
	created := e.Do(t, http.MethodPost, path, api.CreateOutboundAIReplayRequest{Targets: []api.OutboundAIReplayTarget{
		{IntegrationID: claude, Model: "claude-sonnet-4-5"}, {IntegrationID: source}}}, nil)
	var replay api.OutboundAIReplay
	if created.Code != http.StatusAccepted || json.Unmarshal(created.Body.Bytes(), &replay) != nil || replay.State != api.OutboundAIReplayPending ||
		replay.RequestID != logID || len(replay.Targets) != 2 || replay.Source.Model != "gpt-4o" {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	status := 200
	(*e.Store()).SeedOutboundAIReplayResults(replay.ID, "original text",
		state.OutboundAIReplayResult{IntegrationID: claude, Model: "claude-sonnet-4-5", Status: &status, Outcome: "known",
			InputTokens: 12, OutputTokens: 5, CostMillicents: 17, DurationMS: 800, OutputText: "Bonjour",
			ResponseBody: []byte(`{"ok":true}`), ResponseContentType: "application/json"})
	got := e.Do(t, http.MethodGet, "/v1/outbound/ai-replays/"+replay.ID, nil, nil)
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &replay) != nil || replay.State != api.OutboundAIReplayDone ||
		len(replay.Results) != 1 || replay.Results[0].OutputText != "Bonjour" || replay.Source.OutputText == nil ||
		replay.Results[0].Response.Body != `{"ok":true}` || replay.Results[0].Status == nil {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/ai-replays/"+uuid.NewString(), nil, nil), http.StatusNotFound, api.CodeNotFound)
}
