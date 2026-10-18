package outbound

// adr: 1433 — built-in tool uses are counted from OpenAI, Anthropic and
// Gemini responses, reserved from the tools a request declares, priced per
// use, and a reused code-interpreter container is charged once per session.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func observe(format aiWireFormat, contentType, body string, chatSearch bool) (*aiUsageObserver, AIUsage) {
	observer := newAIUsageObserverFormat(format, contentType, time.Now)
	observer.chatSearch = chatSearch
	half := len(body) / 2
	_, _ = observer.Write([]byte(body[:half]))
	_, _ = observer.Write([]byte(body[half:]))
	return observer, observer.Finish()
}

func TestToolUsesAreCounted(t *testing.T) {
	responses := `{"model":"gpt-5","output":[{"type":"web_search_call","id":"ws_1"},{"type":"web_search_call","id":"ws_2"},` +
		`{"type":"file_search_call","id":"fs_1"},{"type":"code_interpreter_call","id":"ci_1","container_id":"cntr_1"},` +
		`{"type":"code_interpreter_call","id":"ci_2","container_id":"cntr_1"},{"type":"message","id":"m_1"}],` +
		`"usage":{"input_tokens":10,"output_tokens":5}}`
	event := func(data string) string { return "data: " + data + "\n\n" }
	item := `{"type":"web_search_call","id":"ws_1"}`
	streamed := event(`{"type":"response.output_item.done","item":`+item+`}`) +
		event(`{"type":"response.completed","response":{"model":"gpt-5","output":[`+item+`],"usage":{"input_tokens":3,"output_tokens":1}}}`)
	aborted := event(`{"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_1"}}`) +
		event(`{"type":"response.output_item.done","item":{"type":"file_search_call","id":"fs_1"}}`) +
		event(`{"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_2"}}`)
	anthropicStream := event(`{"type":"message_start","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":5,"output_tokens":1}}}`) +
		event(`{"type":"message_delta","usage":{"output_tokens":4,"server_tool_use":{"web_search_requests":1}}}`) +
		event(`{"type":"message_delta","usage":{"output_tokens":9,"server_tool_use":{"web_search_requests":3}}}`)
	gemini := func(model string) string {
		return `{"modelVersion":"` + model + `","candidates":[{"content":{"parts":[{"text":"x"}]},"groundingMetadata":` +
			`{"webSearchQueries":["weather","news",""]}}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}`
	}
	for _, tc := range []struct {
		name               string
		format             aiWireFormat
		contentType, body  string
		chatSearch         bool
		web, files         int64
		containers         []string
		input, outputToken int64
	}{
		{"responses", aiFormatOpenAI, "application/json", responses, false, 2, 1, []string{"cntr_1"}, 10, 5},
		{"responses stream", aiFormatOpenAI, "text/event-stream", streamed, false, 1, 0, nil, 3, 1},
		{"aborted responses stream", aiFormatOpenAI, "text/event-stream", aborted, false, 2, 1, nil, 0, 0},
		{"chat search model", aiFormatOpenAI, "application/json", `{"model":"gpt-4o-search-preview","usage":{"prompt_tokens":3,"completion_tokens":2}}`, true, 1, 0, nil, 3, 2},
		{"anthropic", aiFormatAnthropic, "application/json", `{"model":"claude-sonnet-4-5","usage":{"input_tokens":5,"output_tokens":2,"server_tool_use":{"web_search_requests":2}}}`, false, 2, 0, nil, 5, 2},
		{"anthropic stream", aiFormatAnthropic, "text/event-stream", anthropicStream, false, 3, 0, nil, 5, 9},
		{"gemini 3 per query", aiFormatGemini, "application/json", gemini("gemini-3.1-pro-preview"), false, 2, 0, nil, 4, 2},
		{"gemini 2.5 per prompt", aiFormatGemini, "application/json", gemini("gemini-2.5-flash"), false, 1, 0, nil, 4, 2},
		{"bedrock converse output object", aiFormatBedrockConverse, "application/json",
			`{"output":{"message":{"role":"assistant","content":[{"text":"hi"}]}},"usage":{"inputTokens":7,"outputTokens":3}}`, false, 0, 0, nil, 7, 3},
	} {
		observer, usage := observe(tc.format, tc.contentType, tc.body, tc.chatSearch)
		if usage.WebSearches != tc.web || usage.FileSearches != tc.files || usage.InputTokens != tc.input || usage.OutputTokens != tc.outputToken ||
			strings.Join(observer.containerIDs(), ",") != strings.Join(tc.containers, ",") {
			t.Errorf("%s = %+v, containers %v", tc.name, usage, observer.containerIDs())
		}
	}
}

func TestToolReservations(t *testing.T) {
	responses := aiEndpoint{format: aiFormatOpenAI, generation: true, openAIOutputField: "max_output_tokens"}
	chat := aiEndpoint{format: aiFormatOpenAI, generation: true, openAIOutputField: "max_completion_tokens"}
	anthropic := aiEndpoint{format: aiFormatAnthropic, generation: true}
	gemini := func(model string) aiEndpoint {
		return aiEndpoint{format: aiFormatGemini, generation: true, pathModel: model}
	}
	for _, tc := range []struct {
		name     string
		endpoint aiEndpoint
		body     string
		want     aiToolReserve
	}{
		{"responses tools", responses, `{"model":"gpt-5","tools":[{"type":"web_search"},{"type":"file_search","vector_store_ids":["vs"]},` +
			`{"type":"code_interpreter","container":{"type":"auto","memory_limit":"4g"}}]}`,
			aiToolReserve{webSearches: api.OutboundAIToolReserveWebSearches, fileSearches: api.OutboundAIToolReserveFileSearches, codeSessionGB: 4, codeGB: 4}},
		{"responses auto container", responses, `{"model":"gpt-5","tools":[{"type":"code_interpreter","container":"auto"}]}`,
			aiToolReserve{codeSessionGB: 1, codeGB: 1}},
		{"preview web search", responses, `{"model":"gpt-4.1","tools":[{"type":"web_search_preview"}]}`,
			aiToolReserve{webSearches: api.OutboundAIToolReserveWebSearches, codeGB: 1}},
		{"chat search options", chat, `{"model":"gpt-5-search-api","web_search_options":{}}`, aiToolReserve{webSearches: 1, chatSearch: true, codeGB: 1}},
		{"chat search model", chat, `{"model":"gpt-4o-mini-search-preview"}`, aiToolReserve{webSearches: 1, chatSearch: true, codeGB: 1}},
		{"plain chat", chat, `{"model":"gpt-5"}`, aiToolReserve{codeGB: 1}},
		{"anthropic max uses", anthropic, `{"model":"claude-sonnet-4-5","tools":[{"type":"web_search_20250305","name":"web_search","max_uses":3}]}`,
			aiToolReserve{webSearches: 3, codeGB: 1}},
		{"anthropic web fetch is free", anthropic, `{"model":"claude-sonnet-4-5","tools":[{"type":"web_fetch_20250910","name":"web_fetch"}]}`,
			aiToolReserve{codeGB: 1}},
		{"gemini 2.5 grounding", gemini("gemini-2.5-flash"), `{"tools":[{"google_search":{}}]}`, aiToolReserve{webSearches: 1, codeGB: 1}},
		{"gemini 3 grounding", gemini("gemini-3.1-pro-preview"), `{"tools":[{"googleSearch":{}}]}`,
			aiToolReserve{webSearches: api.OutboundAIToolReserveWebSearches, codeGB: 1}},
		{"oversized memory", responses, `{"tools":[{"type":"code_interpreter","container":{"type":"auto","memory_limit":"512g"}}]}`,
			aiToolReserve{codeSessionGB: 1, codeGB: 1}},
	} {
		if got := readAICallFacts(tc.endpoint, []byte(tc.body)).tools; got != tc.want {
			t.Errorf("%s = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestContainerSessionsChargeOncePerWindow(t *testing.T) {
	var sessions aiContainerSessions
	now := time.Now()
	if got := sessions.charge("a", []string{"cntr_1", "cntr_2"}, now); got != 2 {
		t.Fatalf("first use = %d", got)
	}
	if got := sessions.charge("a", []string{"cntr_1"}, now.Add(5*time.Minute)); got != 0 {
		t.Fatalf("a reused container was charged again: %d", got)
	}
	if got := sessions.charge("b", []string{"cntr_1"}, now.Add(5*time.Minute)); got != 1 {
		t.Fatalf("another integration's container = %d", got)
	}
	if got := sessions.charge("a", []string{"cntr_1"}, now.Add(api.OutboundAIContainerSessionMinutes*time.Minute)); got != 1 {
		t.Fatalf("a container after its session window = %d", got)
	}
	if got := sessions.charge("a", nil, now); got != 0 {
		t.Fatalf("no containers = %d", got)
	}
}

func TestToolFeesAreReservedAndCharged(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"`+body.Model+`","output":[{"type":"web_search_call","id":"ws_1"},{"type":"web_search_call","id":"ws_2"},`+
			`{"type":"code_interpreter_call","id":"ci_1","container_id":"cntr_1"}],"usage":{"input_tokens":10,"output_tokens":5}}`)
	}))
	defer upstream.Close()
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "openai", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	webSearch, fileSearch, session := int64(1000), int64(250), int64(3000)
	integration.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "gpt", InputMillicentsPerMTok: 1_000_000,
		OutputMillicentsPerMTok: 2_000_000, MillicentsPerWebSearch: &webSearch, MillicentsPerFileSearch: &fileSearch,
		MillicentsPerCodeSessionGB: &session}})
	capped := integration
	capped.ID = "capped"
	limit := int64(4_000)
	capped.DailyAICostLimit = &limit
	handler, backend := phase9Handler(t, upstream.Client(), integration, capped)
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	request := `{"model":"gpt-5","input":"hi","max_output_tokens":10,"tools":[{"type":"web_search"},` +
		`{"type":"code_interpreter","container":{"type":"auto","memory_limit":"4g"}}]}`
	for range 2 {
		if got := asApp(handler, http.MethodPost, "/i/openai/v1/responses", request, token); got.Code != http.StatusOK {
			t.Fatalf("a call with tools = %d %s", got.Code, got.Body.String())
		}
	}
	row, ok := backend.AIUsageRollup("openai", app, "gpt-5")
	tokens := aiCallCostMillicents(integration, AIUsage{Model: "gpt-5", Known: true, InputTokens: 10, OutputTokens: 5})
	if want := 2*tokens + 4*webSearch + 4*session; !ok || row.WebSearches != 4 || row.CodeSessionGB != 4 || row.CostMillicents != want {
		t.Fatalf("usage = %+v, %v (want cost %d)", row, ok, want)
	}
	// Five reserved searches and one 4 GB session pass a 4,000 millicent
	// cap before the provider is called.
	if got := asApp(handler, http.MethodPost, "/i/capped/v1/responses", request, token); got.Code != http.StatusTooManyRequests {
		t.Fatalf("a call whose tools pass the cap = %d %s", got.Code, got.Body.String())
	}
	if cost := priceCostMillicents(AIPrice{WebSearch: 7, FileSearch: 3, CodeSessionGB: 11},
		AIUsage{Known: true, WebSearches: 2, FileSearches: 3, CodeSessionGB: 4}); cost != 2*7+3*3+4*11 {
		t.Fatalf("tool cost = %d", cost)
	}
}
