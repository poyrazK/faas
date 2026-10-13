package outbound

// adr: 991 — OpenAI-format routes translate to Anthropic and Gemini.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const translateChatBody = `{
	"model": "gpt-4o",
	"messages": [
		{"role": "system", "content": "Be brief."},
		{"role": "developer", "content": [{"type": "text", "text": "Use tools."}]},
		{"role": "user", "content": [{"type": "text", "text": "Weather?"}, {"type": "image_url", "image_url": {"url": "data:image/png;base64,AAAA"}}]},
		{"role": "assistant", "content": null, "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "weather", "arguments": "{\"city\":\"Rome\"}"}}]},
		{"role": "tool", "tool_call_id": "call_1", "content": "{\"temp\":21}"}
	],
	"tools": [{"type": "function", "function": {"name": "weather", "description": "Look up", "parameters": {"type": "object"}}}],
	"tool_choice": "required",
	"parallel_tool_calls": false,
	"stop": "END",
	"temperature": 0.5
}`

func decodeJSONMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

func TestOpenAIToAnthropicRequest(t *testing.T) {
	translation, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderAnthropic}, "/v1/chat/completions", "", []byte(translateChatBody), "claude-sonnet-4")
	if err != nil {
		t.Fatal(err)
	}
	body := decodeJSONMap(t, translation.body)
	if translation.path != "/v1/messages" || body["model"] != "claude-sonnet-4" || body["system"] != "Be brief.\n\nUse tools." ||
		body["max_tokens"] != float64(api.OutboundAIDefaultOutputReserveTokens) || body["temperature"] != 0.5 {
		t.Fatalf("anthropic request = %s", translation.body)
	}
	messages := body["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %v", messages)
	}
	user := messages[0].(map[string]any)["content"].([]any)
	image := user[1].(map[string]any)["source"].(map[string]any)
	toolUse := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	toolResult := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if image["media_type"] != "image/png" || image["data"] != "AAAA" || toolUse["type"] != "tool_use" ||
		toolUse["input"].(map[string]any)["city"] != "Rome" || toolResult["tool_use_id"] != "call_1" || toolResult["content"] != `{"temp":21}` {
		t.Fatalf("blocks = %s", translation.body)
	}
	choice := body["tool_choice"].(map[string]any)
	if choice["type"] != "any" || choice["disable_parallel_tool_use"] != true || body["stop_sequences"].([]any)[0] != "END" {
		t.Fatalf("tool choice / stops = %s", translation.body)
	}
	if tools := body["tools"].([]any); tools[0].(map[string]any)["input_schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("tools = %v", tools)
	}
}

func TestOpenAIToGeminiRequest(t *testing.T) {
	streaming := strings.Replace(translateChatBody, `"temperature": 0.5`, `"temperature": 0.5, "stream": true, "max_tokens": 50`, 1)
	translation, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderGemini}, "/v1/chat/completions", "", []byte(streaming), "gemini-2.5-flash")
	if err != nil {
		t.Fatal(err)
	}
	if translation.path != "/v1beta/models/gemini-2.5-flash:streamGenerateContent" || translation.rawQuery != "alt=sse" {
		t.Fatalf("gemini target = %s?%s", translation.path, translation.rawQuery)
	}
	body := decodeJSONMap(t, translation.body)
	contents := body["contents"].([]any)
	model := contents[1].(map[string]any)
	call := model["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)
	response := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if model["role"] != "model" || call["name"] != "weather" || response["name"] != "weather" ||
		response["response"].(map[string]any)["temp"] != float64(21) {
		t.Fatalf("contents = %s", translation.body)
	}
	config := body["generationConfig"].(map[string]any)
	calling := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
	if config["maxOutputTokens"] != float64(50) || calling["mode"] != "ANY" ||
		body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"] != "Be brief.\n\nUse tools." {
		t.Fatalf("gemini config = %s", translation.body)
	}
}

func TestTranslationRefusesWhatItCannotExpress(t *testing.T) {
	for name, tc := range map[string]struct{ provider, body, model string }{
		"n above one":        {api.OutboundAIProviderAnthropic, `{"n":2,"messages":[{"role":"user","content":"x"}]}`, "c"},
		"unknown role":       {api.OutboundAIProviderAnthropic, `{"messages":[{"role":"function","content":"x"}]}`, "c"},
		"no model":           {api.OutboundAIProviderGemini, `{"messages":[{"role":"user","content":"x"}]}`, ""},
		"remote gemini img":  {api.OutboundAIProviderGemini, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://x/y.png"}}]}]}`, "g"},
		"bedrock remote img": {api.OutboundAIProviderBedrock, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://x/y.png"}}]}]}`, "b"},
	} {
		if _, err := translateRouteRequest(Integration{AIProvider: tc.provider}, "/v1/chat/completions", "", []byte(tc.body), tc.model); !errors.Is(err, errTranslationUnsupported) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	azure, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderAzureOpenAI}, "/v1/chat/completions", "", []byte(`{"model":"gpt-4o","messages":[]}`), "prod-deployment")
	if err != nil || azure.path != "/openai/v1/chat/completions" || !strings.Contains(string(azure.body), `"model":"prod-deployment"`) || azure.translator != nil {
		t.Fatalf("azure target = %+v, %v", azure, err)
	}
}

func TestAnthropicJSONResponseTranslation(t *testing.T) {
	out := newAnthropicTranslator(false, false).json(http.StatusOK, []byte(`{"id":"msg_1","model":"claude-sonnet-4","stop_reason":"tool_use",
		"content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"Rome"}}],
		"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":4}}`))
	body := decodeJSONMap(t, out)
	choice := body["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	call := message["tool_calls"].([]any)[0].(map[string]any)
	usage := body["usage"].(map[string]any)
	if body["object"] != "chat.completion" || message["content"] != "Checking." || choice["finish_reason"] != "tool_calls" ||
		call["function"].(map[string]any)["arguments"] != `{"city":"Rome"}` || usage["prompt_tokens"] != float64(14) ||
		usage["completion_tokens"] != float64(5) || usage["prompt_tokens_details"].(map[string]any)["cached_tokens"] != float64(4) {
		t.Fatalf("translated = %s", out)
	}
	errorBody := decodeJSONMap(t, newAnthropicTranslator(false, false).json(http.StatusBadRequest,
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`)))
	if errorBody["error"].(map[string]any)["message"] != "bad" || errorBody["error"].(map[string]any)["type"] != "invalid_request_error" {
		t.Fatalf("error = %v", errorBody)
	}
}

// translateStream runs provider SSE through a translating writer.
func translateStream(translator aiResponseTranslator, providerSSE string) string {
	recorder := httptest.NewRecorder()
	writer := newTranslatingWriter(recorder, translator)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	for i := 0; i < len(providerSSE); i += 9 {
		_, _ = writer.Write([]byte(providerSSE[i:min(i+9, len(providerSSE))]))
	}
	writer.finish()
	return recorder.Body.String()
}

func TestAnthropicStreamTranslation(t *testing.T) {
	out := translateStream(newAnthropicTranslator(true, true), "event: message_start\n"+
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4","usage":{"input_tokens":10,"output_tokens":1}}}`+"\n\n"+
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n"+
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`+"\n\n"+
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`+"\n\n"+
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"weather"}}`+"\n\n"+
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`+"\n\n"+
		`data: {"type":"ping"}`+"\n\n"+
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`+"\n\n"+
		`data: {"type":"message_stop"}`+"\n\n")
	for _, want := range []string{`"role":"assistant"`, `"content":"Hel"`, `"content":"lo"`, `"id":"toolu_1"`, `"arguments":"{\"city\":"`,
		`"finish_reason":"tool_calls"`, `"completion_tokens":12`, `"prompt_tokens":10`, "data: [DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stream lacks %s:\n%s", want, out)
		}
	}
	if strings.Count(out, "[DONE]") != 1 || strings.Contains(out, "ping") {
		t.Fatalf("stream = %s", out)
	}
}

func TestGeminiStreamAndJSONTranslation(t *testing.T) {
	out := translateStream(newGeminiTranslator(true, false, "gemini-2.5-flash"),
		`data: {"responseId":"r1","candidates":[{"content":{"parts":[{"text":"thinking","thought":true},{"text":"Hi"}]}}]}`+"\n\n"+
			`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"weather","args":{"city":"Rome"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}`+"\n\n")
	for _, want := range []string{`"content":"Hi"`, `"name":"weather"`, `"arguments":"{\"city\":\"Rome\"}"`, `"finish_reason":"tool_calls"`, "data: [DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("gemini stream lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "thinking") || strings.Contains(out, `"usage"`) {
		t.Fatalf("gemini stream leaked thoughts or unrequested usage: %s", out)
	}
	body := decodeJSONMap(t, newGeminiTranslator(false, false, "g").json(http.StatusOK, []byte(
		`{"responseId":"r2","modelVersion":"gemini-2.5-pro","candidates":[{"content":{"parts":[{"text":"Done"}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"thoughtsTokenCount":3}}`)))
	choice := body["choices"].([]any)[0].(map[string]any)
	if body["model"] != "gemini-2.5-pro" || choice["finish_reason"] != "length" || choice["message"].(map[string]any)["content"] != "Done" ||
		body["usage"].(map[string]any)["completion_tokens"] != float64(5) {
		t.Fatalf("gemini json = %v", body)
	}
}

func TestOpenAIRouteFallsBackAcrossProvidersWithTranslation(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("X-Api-Key") + r.Header.Get("X-Goog-Api-Key") + " " + string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			w.WriteHeader(529)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"responseId":"r","modelVersion":"gemini-2.5-flash","candidates":[{"content":{"parts":[{"text":"From Gemini"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2}}`)
	}))
	defer upstream.Close()
	integration := func(id, provider string) Integration {
		i, err := NewIntegration(id, upstream.URL, "unused", []string{"app-1"}, 100, 10, 10, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		i.ProviderAuthMode, i.AIProvider = ProviderAuthManaged, provider
		i.AllowedMethods, i.AllowedPathPrefixes = []string{http.MethodPost}, []string{"/v1", "/v1beta"}
		return i
	}
	resolver, err := NewStaticResolver([]Integration{integration("claude", api.OutboundAIProviderAnthropic), integration("gem", api.OutboundAIProviderGemini)})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.SetManagedAuthorizations(map[string]string{"claude": "Bearer ant-key", "gem": "goog-key"}); err != nil {
		t.Fatal(err)
	}
	handler.Routes = StaticRouteResolver{"route-1": {ID: "route-1", AccountID: "account-1", Format: api.OutboundAIRouteFormatOpenAI,
		Targets: []AIRouteTarget{{IntegrationID: "claude", Model: "claude-sonnet-4"}, {IntegrationID: "gem", Model: "gemini-2.5-flash"}}}}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:2774/r/route-1/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":20}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withRelayIdentity(context.Background(), WorkloadIdentity{AccountID: "account-1", AppID: "app-1", InstanceID: "i-1"}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	body := decodeJSONMap(t, recorder.Body.Bytes())
	if recorder.Code != http.StatusOK || recorder.Header().Get(RouteTargetHeader) != "gem" ||
		body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"] != "From Gemini" {
		t.Fatalf("route = %d %q %s", recorder.Code, recorder.Header().Get(RouteTargetHeader), recorder.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if anthropic := seen["/v1/messages"]; !strings.HasPrefix(anthropic, "ant-key ") || !strings.Contains(anthropic, `"model":"claude-sonnet-4"`) {
		t.Fatalf("anthropic attempt = %q", anthropic)
	}
	if gemini := seen["/v1beta/models/gemini-2.5-flash:generateContent"]; !strings.HasPrefix(gemini, "goog-key ") || !strings.Contains(gemini, `"contents"`) {
		t.Fatalf("gemini attempt = %q (seen %v)", gemini, seen)
	}
	if got := backend.DailyAITokenCount("gem"); got != 9 {
		t.Fatalf("gemini usage settled from native bytes = %d", got)
	}
}
