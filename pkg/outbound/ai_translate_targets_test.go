package outbound

// adr: 995 — response_format maps to each provider's structured output,
// Bedrock and Vertex serve openai-format routes, and those routes accept
// embeddings.

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const schemaFormat = `{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"],"additionalProperties":false}}}`

func chatWithFormat(format string) string {
	return `{"model":"gpt-4o","messages":[{"role":"system","content":"Be exact."},{"role":"user","content":"Q"}],"response_format":` + format + `}`
}

func TestResponseFormatTranslation(t *testing.T) {
	anthropic, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderAnthropic}, "/v1/chat/completions", "", []byte(chatWithFormat(schemaFormat)), "claude-sonnet-4-5")
	if err != nil {
		t.Fatal(err)
	}
	format := decodeJSONMap(t, anthropic.body)["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"].(map[string]any)["additionalProperties"] != false {
		t.Fatalf("anthropic output_config = %s", anthropic.body)
	}
	gemini, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderGemini}, "/v1/chat/completions", "", []byte(chatWithFormat(schemaFormat)), "gemini-2.5-flash")
	if err != nil {
		t.Fatal(err)
	}
	generation := decodeJSONMap(t, gemini.body)["generationConfig"].(map[string]any)
	if generation["responseMimeType"] != "application/json" || generation["responseJsonSchema"].(map[string]any)["type"] != "object" {
		t.Fatalf("gemini generationConfig = %s", gemini.body)
	}
	jsonMode, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderGemini}, "/v1/chat/completions", "", []byte(chatWithFormat(`{"type":"json_object"}`)), "gemini-2.5-flash")
	if err != nil || decodeJSONMap(t, jsonMode.body)["generationConfig"].(map[string]any)["responseJsonSchema"] != nil ||
		!strings.Contains(string(jsonMode.body), `"responseMimeType":"application/json"`) {
		t.Fatalf("gemini json_object = %s, %v", jsonMode.body, err)
	}
	text, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderAnthropic}, "/v1/chat/completions", "", []byte(chatWithFormat(`{"type":"text"}`)), "c")
	if err != nil || strings.Contains(string(text.body), "output_config") {
		t.Fatalf("text format = %s, %v", text.body, err)
	}
	for name, tc := range map[string]struct{ provider, format, reason string }{
		"anthropic json_object": {api.OutboundAIProviderAnthropic, `{"type":"json_object"}`, "need response_format json_schema"},
		"bedrock json_object":   {api.OutboundAIProviderBedrock, `{"type":"json_object"}`, "need response_format json_schema"},
		"schema not an object":  {api.OutboundAIProviderGemini, `{"type":"json_schema","json_schema":{"name":"x","schema":[]}}`, "json_schema.schema"},
		"unknown type":          {api.OutboundAIProviderGemini, `{"type":"yaml"}`, `"yaml" is not supported`},
	} {
		_, err := translateRouteRequest(Integration{AIProvider: tc.provider}, "/v1/chat/completions", "", []byte(chatWithFormat(tc.format)), "m")
		if !errors.Is(err, errTranslationUnsupported) || !strings.Contains(translationRefusalDetail(err), tc.reason) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestBedrockConverseRequestTranslation(t *testing.T) {
	body := `{"model":"gpt-4o","stream":true,"max_tokens":64,"temperature":0.2,"stop":"END",
		"response_format":` + schemaFormat + `,
		"tools":[{"type":"function","function":{"name":"weather","description":"Weather","parameters":{"type":"object"}}}],"tool_choice":"required",
		"messages":[{"role":"system","content":"Sys"},
		{"role":"user","content":[{"type":"text","text":"Look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBO"}}]},
		{"role":"assistant","content":null,"tool_calls":[{"id":"t1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Rome\"}"}}]},
		{"role":"tool","tool_call_id":"t1","content":"sunny"}]}`
	arn := "arn:aws:bedrock:us-east-1:1:inference-profile/us.anthropic.claude-sonnet-4-5"
	translation, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderBedrock}, "/v1/chat/completions", "", []byte(body), arn)
	if err != nil {
		t.Fatal(err)
	}
	if translation.path != "/model/arn:aws:bedrock:us-east-1:1:inference-profile%2Fus.anthropic.claude-sonnet-4-5/converse-stream" {
		t.Fatalf("path = %s", translation.path)
	}
	converse := decodeJSONMap(t, translation.body)
	messages := converse["messages"].([]any)
	user := messages[0].(map[string]any)["content"].([]any)
	toolUse := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)["toolUse"].(map[string]any)
	toolResult := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)["toolResult"].(map[string]any)
	if len(messages) != 3 || user[1].(map[string]any)["image"].(map[string]any)["format"] != "png" ||
		toolUse["toolUseId"] != "t1" || toolUse["input"].(map[string]any)["city"] != "Rome" || toolResult["toolUseId"] != "t1" {
		t.Fatalf("converse messages = %s", translation.body)
	}
	inference := converse["inferenceConfig"].(map[string]any)
	tools := converse["toolConfig"].(map[string]any)
	jsonSchema := converse["outputConfig"].(map[string]any)["textFormat"].(map[string]any)["structure"].(map[string]any)["jsonSchema"].(map[string]any)
	if inference["maxTokens"] != float64(64) || inference["stopSequences"].([]any)[0] != "END" ||
		converse["system"].([]any)[0].(map[string]any)["text"] != "Sys" || tools["toolChoice"].(map[string]any)["any"] == nil ||
		jsonSchema["name"] != "answer" || !strings.HasPrefix(jsonSchema["schema"].(string), `{"type":"object"`) {
		t.Fatalf("converse config = %s", translation.body)
	}
	noneWithHistory := strings.Replace(body, `"tool_choice":"required"`, `"tool_choice":"none"`, 1)
	if _, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderBedrock}, "/v1/chat/completions", "", []byte(noneWithHistory), "m"); !errors.Is(err, errTranslationUnsupported) {
		t.Fatalf("tool_choice none with tool history = %v", err)
	}
}

func TestConverseResponseTranslation(t *testing.T) {
	out := newConverseTranslator(false, false, "amazon.nova-pro-v1:0").json(http.StatusOK, []byte(`{"output":{"message":{"role":"assistant",
		"content":[{"text":"Checking."},{"toolUse":{"toolUseId":"t1","name":"weather","input":{"city":"Rome"}}}]}},
		"stopReason":"tool_use","usage":{"inputTokens":10,"outputTokens":5,"cacheReadInputTokens":4}}`))
	body := decodeJSONMap(t, out)
	choice := body["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	usage := body["usage"].(map[string]any)
	if choice["finish_reason"] != "tool_calls" || message["content"] != "Checking." ||
		message["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["arguments"] != `{"city":"Rome"}` ||
		usage["prompt_tokens"] != float64(14) || body["model"] != "amazon.nova-pro-v1:0" || !strings.HasPrefix(body["id"].(string), "chatcmpl-") {
		t.Fatalf("converse completion = %s", out)
	}
	if failed := decodeJSONMap(t, newConverseTranslator(false, false, "m").json(http.StatusBadRequest, []byte(`{"message":"bad schema"}`))); failed["error"].(map[string]any)["message"] != "bad schema" {
		t.Fatalf("bedrock error = %v", failed)
	}
}

// awsTypedEventFrame builds one event-stream message with :message-type
// and :event-type (or :exception-type) headers.
func awsTypedEventFrame(messageType, eventType string, payload []byte) []byte {
	var headers []byte
	add := func(name, value string) {
		headers = append(headers, byte(len(name)))
		headers = append(headers, name...)
		headers = append(headers, 7)
		headers = binary.BigEndian.AppendUint16(headers, uint16(len(value)))
		headers = append(headers, value...)
	}
	add(":message-type", messageType)
	if messageType == "exception" {
		add(":exception-type", eventType)
	} else {
		add(":event-type", eventType)
	}
	total := 12 + len(headers) + len(payload) + 4
	frame := binary.BigEndian.AppendUint32(nil, uint32(total))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(headers)))
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
	frame = append(frame, headers...)
	frame = append(frame, payload...)
	return binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
}

func TestConverseStreamTranslation(t *testing.T) {
	var stream []byte
	for _, frame := range [][]byte{
		awsTypedEventFrame("event", "messageStart", []byte(`{"role":"assistant"}`)),
		awsTypedEventFrame("event", "contentBlockDelta", []byte(`{"contentBlockIndex":0,"delta":{"text":"Hel"}}`)),
		awsTypedEventFrame("event", "contentBlockDelta", []byte(`{"contentBlockIndex":0,"delta":{"text":"lo"}}`)),
		awsTypedEventFrame("event", "contentBlockStart", []byte(`{"contentBlockIndex":1,"start":{"toolUse":{"toolUseId":"t1","name":"weather"}}}`)),
		awsTypedEventFrame("event", "contentBlockDelta", []byte(`{"contentBlockIndex":1,"delta":{"toolUse":{"input":"{\"city\":"}}}`)),
		awsTypedEventFrame("event", "messageStop", []byte(`{"stopReason":"tool_use"}`)),
		awsTypedEventFrame("event", "metadata", []byte(`{"usage":{"inputTokens":7,"outputTokens":3},"metrics":{"latencyMs":5}}`)),
	} {
		stream = append(stream, frame...)
	}
	recorder := httptest.NewRecorder()
	writer := newTranslatingWriter(recorder, newConverseTranslator(true, true, "amazon.nova-pro-v1:0"))
	writer.Header().Set("Content-Type", awsEventStreamMediaType)
	writer.WriteHeader(http.StatusOK)
	for i := 0; i < len(stream); i += 11 {
		_, _ = writer.Write(stream[i:min(i+11, len(stream))])
	}
	writer.finish()
	out := recorder.Body.String()
	if recorder.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(out, `"content":"Hel"`) ||
		!strings.Contains(out, `"content":"lo"`) || !strings.Contains(out, `"name":"weather"`) ||
		!strings.Contains(out, `"finish_reason":"tool_calls"`) || !strings.Contains(out, `"prompt_tokens":7`) ||
		!strings.HasSuffix(out, "data: [DONE]\n\n") {
		t.Fatalf("translated stream = %s", out)
	}

	failed := httptest.NewRecorder()
	failing := newTranslatingWriter(failed, newConverseTranslator(true, false, "m"))
	failing.Header().Set("Content-Type", awsEventStreamMediaType)
	_, _ = failing.Write(awsTypedEventFrame("exception", "throttlingException", []byte(`{"message":"slow down"}`)))
	failing.finish()
	if body := failed.Body.String(); !strings.Contains(body, `"message":"slow down"`) || strings.Count(body, "[DONE]") != 1 {
		t.Fatalf("exception stream = %s", body)
	}
}

func TestVertexTargetPaths(t *testing.T) {
	if base, ok := api.OutboundVertexBasePath([]string{"/v1/projects/p1/locations/europe-west4/publishers/", "/v1/projects/p1/locations/europe-west4"}); !ok || base != "/v1/projects/p1/locations/europe-west4" {
		t.Fatalf("base = %q %v", base, ok)
	}
	for _, prefixes := range [][]string{{"/v1/"}, {"/v1/projects/a/locations/x/", "/v1/projects/b/locations/x/"}} {
		if _, ok := api.OutboundVertexBasePath(prefixes); ok {
			t.Fatalf("ambiguous prefixes %v resolved", prefixes)
		}
	}
	vertex := Integration{AIProvider: api.OutboundAIProviderVertex, AllowedPathPrefixes: []string{"/v1/projects/p1/locations/global/"}}
	claude, err := translateRouteRequest(vertex, "/v1/chat/completions", "", []byte(`{"stream":true,"messages":[{"role":"user","content":"hi"}]}`), "claude-sonnet-4-5@20250929")
	if err != nil {
		t.Fatal(err)
	}
	fields := decodeJSONMap(t, claude.body)
	if claude.path != "/v1/projects/p1/locations/global/publishers/anthropic/models/claude-sonnet-4-5@20250929:streamRawPredict" ||
		fields["model"] != nil || fields["anthropic_version"] != api.OutboundVertexAnthropicVersion {
		t.Fatalf("vertex claude = %s %s", claude.path, claude.body)
	}
	gemini, err := translateRouteRequest(vertex, "/v1/chat/completions", "", []byte(`{"messages":[{"role":"user","content":"hi"}]}`), "google/gemini-2.5-pro")
	if err != nil || gemini.path != "/v1/projects/p1/locations/global/publishers/google/models/gemini-2.5-pro:generateContent" {
		t.Fatalf("vertex gemini = %+v, %v", gemini, err)
	}
	if _, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderVertex, AllowedPathPrefixes: []string{"/v1/"}},
		"/v1/chat/completions", "", []byte(`{"messages":[{"role":"user","content":"hi"}]}`), "gemini-2.5-pro"); !strings.Contains(translationRefusalDetail(err), "/v1/projects/{project}/locations/{location}") {
		t.Fatalf("vertex without a location = %v", err)
	}
}

func TestEmbeddingsTranslation(t *testing.T) {
	body := []byte(`{"model":"text-embedding-3-small","input":["a","b"],"dimensions":3}`)
	gemini, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderGemini}, "/v1/embeddings", "", body, "gemini-embedding-001")
	if err != nil {
		t.Fatal(err)
	}
	requests := decodeJSONMap(t, gemini.body)["requests"].([]any)
	if gemini.path != "/v1beta/models/gemini-embedding-001:batchEmbedContents" || len(requests) != 2 ||
		requests[1].(map[string]any)["outputDimensionality"] != float64(3) || requests[0].(map[string]any)["model"] != "models/gemini-embedding-001" {
		t.Fatalf("gemini embeddings = %s %s", gemini.path, gemini.body)
	}
	out := decodeJSONMap(t, gemini.translator.json(http.StatusOK, []byte(`{"embeddings":[{"values":[0.5,-1]},{"values":[0.25,2.5e-3]}]}`)))
	data := out["data"].([]any)
	if out["object"] != "list" || len(data) != 2 || data[1].(map[string]any)["index"] != float64(1) ||
		data[1].(map[string]any)["embedding"].([]any)[1] != 2.5e-3 || out["model"] != "gemini-embedding-001" {
		t.Fatalf("gemini embeddings response = %v", out)
	}
	base64Request := []byte(`{"input":"a","encoding_format":"base64"}`)
	encoded, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderGemini}, "/v1/embeddings", "", base64Request, "gemini-embedding-001")
	if err != nil {
		t.Fatal(err)
	}
	vector := decodeJSONMap(t, encoded.translator.json(http.StatusOK, []byte(`{"embeddings":[{"values":[0.5,-1]}]}`)))["data"].([]any)[0].(map[string]any)["embedding"].(string)
	raw, err := base64.StdEncoding.DecodeString(vector)
	if err != nil || len(raw) != 8 || math.Float32frombits(binary.LittleEndian.Uint32(raw[4:])) != -1 {
		t.Fatalf("base64 embedding = %q %v", vector, err)
	}
	// The response is built with encoding/json: a quote in the target model
	// stays inside its string, and a malformed vector is a gateway error.
	quoted := (&embeddingTranslator{model: `x"},"injected":{"`}).json(http.StatusOK, []byte(`{"embeddings":[{"values":[1]}]}`))
	if parsed := decodeJSONMap(t, quoted); parsed["model"] != `x"},"injected":{"` || parsed["injected"] != nil {
		t.Fatalf("quoted model = %s", quoted)
	}
	if bad := (&embeddingTranslator{model: "m"}).json(http.StatusOK, []byte(`{"embeddings":[{"values":"nope"}]}`)); !strings.Contains(string(bad), `"error"`) {
		t.Fatalf("malformed vector = %s", bad)
	}
	for name, provider := range map[string]string{"anthropic": api.OutboundAIProviderAnthropic, "bedrock": api.OutboundAIProviderBedrock} {
		if _, err := translateRouteRequest(Integration{AIProvider: provider}, "/v1/embeddings", "", body, "m"); !errors.Is(err, errTranslationUnsupported) {
			t.Errorf("%s embeddings = %v", name, err)
		}
	}
	if _, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderGemini}, "/v1/embeddings", "", []byte(`{"input":[[1,2]]}`), "g"); !errors.Is(err, errTranslationUnsupported) {
		t.Fatalf("token-array input = %v", err)
	}
}

func TestRouteAttemptKeepsEscapedModelPaths(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:2774/r/x/v1/chat/completions", nil)
	attempt := routeAttemptRequest(r, "integration-1", "/model/arn:aws:bedrock:us-east-1:1:inference-profile%2Fx/converse", "", nil, true)
	if attempt.URL.EscapedPath() != "/i/integration-1/model/arn:aws:bedrock:us-east-1:1:inference-profile%2Fx/converse" ||
		attempt.URL.Path != "/i/integration-1/model/arn:aws:bedrock:us-east-1:1:inference-profile/x/converse" {
		t.Fatalf("attempt path = %q raw %q", attempt.URL.Path, attempt.URL.EscapedPath())
	}
}

// TestOpenAIRouteToVertex runs a translated chat call and an embeddings
// call through Vertex AI, settling usage from the native responses.
func TestOpenAIRouteToVertex(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("Authorization") + " " + string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, ":predict") {
			_, _ = io.WriteString(w, `{"predictions":[{"embeddings":{"values":[0.1,0.2],"statistics":{"token_count":4,"truncated":false}}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"msg_1","model":"claude-sonnet-4-5","stop_reason":"end_turn","content":[{"type":"text","text":"{\"a\":\"b\"}"}],"usage":{"input_tokens":6,"output_tokens":4}}`)
	}))
	defer upstream.Close()
	vertex, err := NewIntegration("vertex", upstream.URL, "unused", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	vertex.ProviderAuthMode, vertex.AIProvider = ProviderAuthManaged, api.OutboundAIProviderVertex
	vertex.AllowedMethods, vertex.AllowedPathPrefixes = []string{http.MethodPost}, []string{"/v1/projects/p1/locations/us-east5"}
	resolver, err := NewStaticResolver([]Integration{vertex})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.SetManagedAuthorizations(map[string]string{"vertex": "Bearer ya29.token"}); err != nil {
		t.Fatal(err)
	}
	call := func(routeID, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:2774/r/"+routeID+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(withRelayIdentity(context.Background(), WorkloadIdentity{AccountID: "account-1", AppID: "app-1", InstanceID: "i-1"}))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	handler.Routes = StaticRouteResolver{
		"chat":  {ID: "chat", AccountID: "account-1", Format: api.OutboundAIRouteFormatOpenAI, Targets: []AIRouteTarget{{IntegrationID: "vertex", Model: "claude-sonnet-4-5"}}},
		"embed": {ID: "embed", AccountID: "account-1", Format: api.OutboundAIRouteFormatOpenAI, Targets: []AIRouteTarget{{IntegrationID: "vertex", Model: "text-embedding-005"}}},
	}
	chat := call("chat", "/v1/chat/completions", chatWithFormat(schemaFormat))
	message := decodeJSONMap(t, chat.Body.Bytes())["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if chat.Code != http.StatusOK || message["content"] != `{"a":"b"}` {
		t.Fatalf("vertex chat = %d %s", chat.Code, chat.Body.String())
	}
	embed := call("embed", "/v1/embeddings", `{"model":"x","input":"hello"}`)
	var list struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int64 `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if embed.Code != http.StatusOK || json.Unmarshal(embed.Body.Bytes(), &list) != nil || len(list.Data) != 1 || list.Data[0].Embedding[1] != 0.2 || list.Usage.PromptTokens != 4 {
		t.Fatalf("vertex embeddings = %d %s", embed.Code, embed.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	claude := seen["/v1/projects/p1/locations/us-east5/publishers/anthropic/models/claude-sonnet-4-5:rawPredict"]
	if !strings.HasPrefix(claude, "Bearer ya29.token ") || !strings.Contains(claude, `"output_config"`) || !strings.Contains(claude, `"anthropic_version":"vertex-2023-10-16"`) {
		t.Fatalf("vertex claude attempt = %q (seen %v)", claude, seen)
	}
	if predict := seen["/v1/projects/p1/locations/us-east5/publishers/google/models/text-embedding-005:predict"]; !strings.Contains(predict, `"instances":[{"content":"hello"}]`) {
		t.Fatalf("vertex predict attempt = %q", predict)
	}
	if got := backend.DailyAITokenCount("vertex"); got != 14 {
		t.Fatalf("settled tokens = %d, want 10 chat + 4 embedding", got)
	}
}
