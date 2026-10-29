package outbound

// adr: 20261020202035 — AI calls belong to the trace named by
// X-Gregale-AI-Trace or a traceparent; settlement rolls them up with the
// tool calls the model requested, read from every metered format, and
// per-trace call, cost and tool-loop limits refuse a trace's next call.

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTraceparentTraceID(t *testing.T) {
	const id = "4bf92f3577b34da6a3ce929d0e0e4736"
	for _, tc := range []struct {
		values []string
		want   string
	}{
		{[]string{"00-" + id + "-00f067aa0ba902b7-01"}, id},
		{[]string{" 00-" + id + "-00f067aa0ba902b7-00 "}, id},
		{[]string{"01-" + id + "-00f067aa0ba902b7-01-future"}, id},
		{[]string{"00-" + id + "-00f067aa0ba902b7-01-extra"}, ""},
		{[]string{"ff-" + id + "-00f067aa0ba902b7-01"}, ""},
		{[]string{"00-" + strings.ToUpper(id) + "-00f067aa0ba902b7-01"}, ""},
		{[]string{"00-00000000000000000000000000000000-00f067aa0ba902b7-01"}, ""},
		{[]string{"00-" + id + "-0000000000000000-01"}, ""},
		{[]string{"00-" + id[:31] + "-00f067aa0ba902b7-01"}, ""},
		{[]string{"00-" + id + "-00f067aa0ba902b7-01", "00-" + id + "-00f067aa0ba902b7-01"}, ""},
		{nil, ""},
	} {
		if got := traceparentTraceID(tc.values); got != tc.want {
			t.Errorf("%q = %q, want %q", tc.values, got, tc.want)
		}
	}
}

func TestAICallTraceHeaderWinsOverTraceparent(t *testing.T) {
	ai := Integration{AIProvider: api.OutboundAIProviderOpenAI}
	request := func(headers ...string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Add(headers[i], headers[i+1])
		}
		return r
	}
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	for _, tc := range []struct {
		r     *http.Request
		want  string
		valid bool
	}{
		{request(api.OutboundAITraceHeader, "task:42", "Traceparent", parent), "task:42", true},
		{request("Traceparent", parent), "4bf92f3577b34da6a3ce929d0e0e4736", true},
		{request("Traceparent", "garbage"), "", true},
		{request(), "", true},
		{request(api.OutboundAITraceHeader, "has space"), "", false},
		{request(api.OutboundAITraceHeader, strings.Repeat("a", api.OutboundAIUserIDMaxBytes+1)), "", false},
		{request(api.OutboundAITraceHeader, "a", api.OutboundAITraceHeader, "b"), "", false},
	} {
		got, valid := aiCallTrace(tc.r, ai)
		if got != tc.want || valid != tc.valid {
			t.Errorf("%v = %q, %v; want %q, %v", tc.r.Header, got, valid, tc.want, tc.valid)
		}
	}
	if got, valid := aiCallTrace(request(api.OutboundAITraceHeader, "has space"), Integration{}); got != "" || !valid {
		t.Fatal("a non-AI integration read the trace header")
	}
}

// toolCallsOf feeds a response to a usage observer one byte at a time, as
// providers may split a stream anywhere, and returns its tool calls.
func toolCallsOf(observer *aiUsageObserver, body []byte) ([]string, []byte) {
	for i := range body {
		_, _ = observer.Write(body[i : i+1])
	}
	observer.Finish()
	return observer.toolCalls()
}

func sseEvents(events ...string) []byte {
	var b strings.Builder
	for _, event := range events {
		b.WriteString("data: " + event + "\n\n")
	}
	return []byte(b.String())
}

func TestToolCallsAreReadFromEveryFormat(t *testing.T) {
	// Each case requests lookup({"q":1}) and then send({"to":"a"}), so
	// every format yields the same names and the same signature.
	chatWhole := `{"model":"gpt-5","choices":[{"index":0,"message":{"tool_calls":[` +
		`{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}},` +
		`{"id":"c2","type":"function","function":{"name":"send","arguments":"{\"to\":\"a\"}"}}]}}],"usage":{"prompt_tokens":3}}`
	chatStream := sseEvents(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"lookup","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"c2","function":{"name":"send","arguments":"{\"to\":\"a\"}"}}]}}]}`,
		`[DONE]`)
	anthropicWhole := `{"model":"claude-sonnet-5-5","content":[{"type":"text","text":"hi"},` +
		`{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":1}},{"type":"tool_use","id":"toolu_2","name":"send","input":{"to":"a"}}],` +
		`"usage":{"input_tokens":3}}`
	anthropicStream := sseEvents(
		`{"type":"message_start","message":{"model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":3}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"1}"}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_2","name":"send","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"to\":\"a\"}"}}`,
		`{"type":"message_delta","usage":{"output_tokens":9}}`)
	gemini := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"q":1}}},` +
		`{"functionCall":{"name":"send","args":{"to":"a"}}}]}}],"usageMetadata":{"promptTokenCount":3}}`
	responsesItem := func(id, name, args string) string {
		return `{"type":"function_call","id":"fc_` + id + `","call_id":"call_` + id + `","name":"` + name + `","arguments":` + args + `}`
	}
	lookupItem, sendItem := responsesItem("1", "lookup", `"{\"q\":1}"`), responsesItem("2", "send", `"{\"to\":\"a\"}"`)
	responsesWhole := `{"model":"gpt-5","output":[{"type":"message","content":[]},` + lookupItem + `,` + sendItem + `],"usage":{"input_tokens":3}}`
	// A stream reports each item when done and again in the final
	// response; each call counts once.
	responsesStream := sseEvents(
		`{"type":"response.output_item.done","item":`+lookupItem+`}`,
		`{"type":"response.output_item.done","item":`+sendItem+`}`,
		`{"type":"response.completed","response":{"model":"gpt-5","output":[`+lookupItem+`,`+sendItem+`],"usage":{"input_tokens":3}}}`)
	converse := `{"output":{"message":{"role":"assistant","content":[{"text":"hi"},` +
		`{"toolUse":{"toolUseId":"t1","name":"lookup","input":{"q":1}}},{"toolUse":{"toolUseId":"t2","name":"send","input":{"to":"a"}}}]}},` +
		`"usage":{"inputTokens":3,"outputTokens":4}}`
	var converseStream []byte
	for _, payload := range []string{
		`{"contentBlockIndex":0,"delta":{"text":"hi"}}`,
		`{"contentBlockIndex":1,"start":{"toolUse":{"toolUseId":"t1","name":"lookup"}}}`,
		`{"contentBlockIndex":1,"delta":{"toolUse":{"input":"{\"q\":"}}}`,
		`{"contentBlockIndex":1,"delta":{"toolUse":{"input":"1}"}}}`,
		`{"contentBlockIndex":2,"start":{"toolUse":{"toolUseId":"t2","name":"send"}}}`,
		`{"contentBlockIndex":2,"delta":{"toolUse":{"input":"{\"to\":\"a\"}"}}}`,
		`{"usage":{"inputTokens":3,"outputTokens":4},"metrics":{"latencyMs":5}}`,
	} {
		converseStream = append(converseStream, awsEventFrame("event", []byte(payload))...)
	}
	var invokeStream []byte
	for _, event := range bytes.Split(bytes.TrimSpace(anthropicStream), []byte("\n\n")) {
		encoded := base64.StdEncoding.EncodeToString(bytes.TrimPrefix(event, []byte("data: ")))
		invokeStream = append(invokeStream, awsEventFrame("event", []byte(`{"bytes":"`+encoded+`"}`))...)
	}

	var want []byte
	for _, tc := range []struct {
		name     string
		observer *aiUsageObserver
		body     []byte
	}{
		{"chat completions", newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil), []byte(chatWhole)},
		{"chat completions stream", newAIUsageObserver(api.OutboundAIProviderOpenAI, "text/event-stream", nil), chatStream},
		{"anthropic messages", newAIUsageObserver(api.OutboundAIProviderAnthropic, "application/json", nil), []byte(anthropicWhole)},
		{"anthropic messages stream", newAIUsageObserver(api.OutboundAIProviderAnthropic, "text/event-stream", nil), anthropicStream},
		{"gemini", newAIUsageObserver(api.OutboundAIProviderGemini, "application/json", nil), []byte(gemini)},
		{"gemini stream", newAIUsageObserver(api.OutboundAIProviderGemini, "text/event-stream", nil), sseEvents(gemini)},
		{"responses", newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil), []byte(responsesWhole)},
		{"responses stream", newAIUsageObserver(api.OutboundAIProviderOpenAI, "text/event-stream", nil), responsesStream},
		{"bedrock converse", newAIUsageObserverFormat(aiFormatBedrockConverse, "application/json", nil), []byte(converse)},
		{"bedrock converse stream", newAIUsageObserverFormat(aiFormatBedrockConverse, awsEventStreamMediaType, nil), converseStream},
		{"bedrock invoke stream", newAIUsageObserverFormat(aiFormatBedrockInvoke, awsEventStreamMediaType, nil), invokeStream},
	} {
		names, signature := toolCallsOf(tc.observer, tc.body)
		if strings.Join(names, ",") != "lookup,send" || len(signature) != 16 {
			t.Errorf("%s: names %v, signature %x", tc.name, names, signature)
			continue
		}
		if want == nil {
			want = signature
		} else if !bytes.Equal(signature, want) {
			t.Errorf("%s: signature %x differs from %x", tc.name, signature, want)
		}
	}
}

func TestToolCallSignatureFollowsArguments(t *testing.T) {
	chat := func(args string) []byte {
		return []byte(`{"choices":[{"index":0,"message":{"tool_calls":[{"id":"c1","function":{"name":"lookup","arguments":"` + args + `"}}]}}]}`)
	}
	_, first := toolCallsOf(newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil), chat(`{\"q\":1}`))
	_, same := toolCallsOf(newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil), chat(`{\"q\":1}`))
	_, other := toolCallsOf(newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil), chat(`{\"q\":2}`))
	if !bytes.Equal(first, same) || bytes.Equal(first, other) {
		t.Fatalf("signatures %x %x %x", first, same, other)
	}
	names, none := toolCallsOf(newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil),
		[]byte(`{"choices":[{"message":{"content":"no tools"}}]}`))
	if names != nil || none != nil {
		t.Fatalf("a response without tool calls = %v %x", names, none)
	}
}

func TestToolCallsAreBoundedAndSanitized(t *testing.T) {
	var calls []string
	for range api.OutboundAIToolCallsPerResponseMax + 4 {
		calls = append(calls, `{"type":"tool_use","id":"`+uuid.NewString()+`","name":"look\u0000up `+strings.Repeat("x", 200)+`","input":{}}`)
	}
	names, _ := toolCallsOf(newAIUsageObserver(api.OutboundAIProviderAnthropic, "application/json", nil),
		[]byte(`{"content":[`+strings.Join(calls, ",")+`]}`))
	if len(names) != api.OutboundAIToolCallsPerResponseMax {
		t.Fatalf("kept %d tool calls", len(names))
	}
	if name := names[0]; len(name) != api.OutboundAIToolNameMaxBytes || !strings.HasPrefix(name, "lookupxxx") {
		t.Fatalf("name = %q", name)
	}
}

// traceFixture serves a configurable response to an AI integration and
// records the headers the provider saw.
type traceFixture struct {
	handler *Handler
	backend *MemoryBackend
	token   AIAccessToken
	mu      sync.Mutex
	body    string
	headers []http.Header
}

const traceToolResponse = `{"model":"gpt-5","choices":[{"message":{"tool_calls":[{"id":"c1","function":{"name":"lookup","arguments":"{\"q\":1}"}}]}}],` +
	`"usage":{"prompt_tokens":1000,"completion_tokens":500}}`

func newTraceFixture(t *testing.T, configure func(*Integration)) *traceFixture {
	t.Helper()
	f := &traceFixture{body: traceToolResponse}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		body := f.body
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "llm", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	if configure != nil {
		configure(&integration)
	}
	f.handler, f.backend = phase9Handler(t, upstream.Client(), integration)
	f.token = AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	return f
}

func (f *traceFixture) respond(body string) {
	f.mu.Lock()
	f.body = body
	f.mu.Unlock()
}

func (f *traceFixture) call(headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/i/llm/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","max_tokens":100,"messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Add(headers[i], headers[i+1])
	}
	request = request.WithContext(withAccessToken(request.Context(), f.token))
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func limit(value int64) *int64 { return &value }

func TestTracedCallsRollUpWithTheirToolCalls(t *testing.T) {
	f := newTraceFixture(t, func(i *Integration) { i.AIRequestLogEnabled, i.AILogExport = true, true })
	for range 2 {
		if got := f.call(api.OutboundAITraceHeader, "task-1"); got.Code != http.StatusOK {
			t.Fatalf("traced call = %d %s", got.Code, got.Body.String())
		}
	}
	f.respond(`{"model":"gpt-5-mini","choices":[{"message":{"content":"done"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	if got := f.call(api.OutboundAITraceHeader, "task-1"); got.Code != http.StatusOK {
		t.Fatalf("final call = %d", got.Code)
	}
	trace, ok := f.backend.AITrace("llm", "task-1")
	if !ok || trace.Calls != 3 || trace.ToolCalls != 2 || trace.RepeatedToolCalls != 0 || trace.InputTokens != 2010 ||
		strings.Join(trace.Models, ",") != "gpt-5,gpt-5-mini" || trace.Caller == "" || trace.CostMillicents <= 0 {
		t.Fatalf("trace = %+v, %v", trace, ok)
	}
	for _, header := range f.headers {
		if header.Get(api.OutboundAITraceHeader) != "" {
			t.Fatal("the trace header reached the provider")
		}
	}
	logged := f.backend.AIRequestLog("llm")
	if len(logged) != 3 || logged[0].TraceID != "task-1" || strings.Join(logged[0].ToolCalls, ",") != "lookup" || len(logged[2].ToolCalls) != 0 {
		t.Fatalf("request log = %+v", logged)
	}
	exports := f.backend.AILogExports()
	if len(exports) != 3 || !strings.Contains(exports[0].Line, `"trace_id":"task-1"`) || !strings.Contains(exports[0].Line, `"tool_calls":["lookup"]`) ||
		exports[0].Attributes["gregale.ai.trace_id"] != "task-1" {
		t.Fatalf("export = %+v", exports)
	}
}

func TestTraceparentNamesTheTraceAndIsForwarded(t *testing.T) {
	f := newTraceFixture(t, func(i *Integration) { i.AIRequestLogEnabled = true })
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if got := f.call("Traceparent", parent); got.Code != http.StatusOK {
		t.Fatalf("call = %d", got.Code)
	}
	if _, ok := f.backend.AITrace("llm", "4bf92f3577b34da6a3ce929d0e0e4736"); !ok {
		t.Fatal("the traceparent's trace was not recorded")
	}
	if len(f.headers) != 1 || f.headers[0].Get("Traceparent") != parent {
		t.Fatalf("provider headers = %v", f.headers)
	}
}

func TestMalformedTraceHeaderIsRefused(t *testing.T) {
	f := newTraceFixture(t, nil)
	got := f.call(api.OutboundAITraceHeader, "not valid")
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "outbound_ai_trace_invalid") || len(f.headers) != 0 {
		t.Fatalf("malformed trace = %d %s, %d provider calls", got.Code, got.Body.String(), len(f.headers))
	}
}

func TestTracesNeedARequestLogOrLimits(t *testing.T) {
	f := newTraceFixture(t, nil)
	if got := f.call(api.OutboundAITraceHeader, "task-1"); got.Code != http.StatusOK {
		t.Fatalf("call = %d", got.Code)
	}
	if _, ok := f.backend.AITrace("llm", "task-1"); ok {
		t.Fatal("a trace was recorded with no request log and no limits")
	}
}

func TestTraceLimitsRefuseATracesNextCall(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*Integration)
		allowed   int
		reason    string
	}{
		{"calls", func(i *Integration) { i.AITraceMaxCalls = limit(2) }, 2, ReasonTraceCallLimit},
		// A call settles 2,000 millicents.
		{"cost", func(i *Integration) { i.AITraceMaxCostMillicents = limit(3_000) }, 2, ReasonTraceCostLimit},
		{"tool loop", func(i *Integration) { i.AITraceMaxRepeatedToolCalls = limit(3) }, 3, ReasonTraceToolLoop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTraceFixture(t, tc.configure)
			for index := range tc.allowed {
				if got := f.call(api.OutboundAITraceHeader, "task-1"); got.Code != http.StatusOK {
					t.Fatalf("call %d = %d %v", index+1, got.Code, got.Header())
				}
			}
			got := f.call(api.OutboundAITraceHeader, "task-1")
			if got.Code != http.StatusTooManyRequests || got.Header().Get("X-Gregale-Outbound-Rejection") != tc.reason ||
				got.Header().Get("Retry-After") != "" || !strings.Contains(got.Body.String(), "outbound_budget_exhausted") {
				t.Fatalf("over the limit = %d %v %s", got.Code, got.Header(), got.Body.String())
			}
			if got := f.call(api.OutboundAITraceHeader, "task-2"); got.Code != http.StatusOK {
				t.Fatalf("another trace = %d", got.Code)
			}
			if got := f.call(); got.Code != http.StatusOK {
				t.Fatalf("an untraced call = %d", got.Code)
			}
		})
	}
}

func TestToolLoopStreakEndsWithoutToolCalls(t *testing.T) {
	f := newTraceFixture(t, func(i *Integration) { i.AITraceMaxRepeatedToolCalls = limit(2) })
	steps := []string{traceToolResponse,
		`{"model":"gpt-5","choices":[{"message":{"content":"thinking"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		traceToolResponse,
		strings.Replace(traceToolResponse, `{\"q\":1}`, `{\"q\":2}`, 1),
		traceToolResponse}
	for index, body := range steps {
		f.respond(body)
		if got := f.call(api.OutboundAITraceHeader, "task-1"); got.Code != http.StatusOK {
			t.Fatalf("step %d = %d %v", index+1, got.Code, got.Header())
		}
	}
	if trace, _ := f.backend.AITrace("llm", "task-1"); trace.RepeatedToolCalls != 1 {
		t.Fatalf("streak = %d", trace.RepeatedToolCalls)
	}
	f.respond(traceToolResponse)
	_ = f.call(api.OutboundAITraceHeader, "task-1")
	if got := f.call(api.OutboundAITraceHeader, "task-1"); got.Header().Get("X-Gregale-Outbound-Rejection") != ReasonTraceToolLoop {
		t.Fatalf("a repeated tool call was admitted: %d", got.Code)
	}
}

func TestNewTracesPastTheDayAreRefusedOnlyWithLimits(t *testing.T) {
	for _, limited := range []bool{false, true} {
		f := newTraceFixture(t, func(i *Integration) {
			i.AIRequestLogEnabled = true
			if limited {
				i.AITraceMaxCalls = limit(100)
			}
		})
		if got := f.call(api.OutboundAITraceHeader, "known"); got.Code != http.StatusOK {
			t.Fatalf("first call = %d", got.Code)
		}
		f.backend.mu.Lock()
		f.backend.states["llm"].traceDayCount = api.OutboundAITracesPerDay
		f.backend.mu.Unlock()
		if got := f.call(api.OutboundAITraceHeader, "known"); got.Code != http.StatusOK {
			t.Fatalf("limited=%v: a recorded trace = %d", limited, got.Code)
		}
		got := f.call(api.OutboundAITraceHeader, "new")
		if limited {
			if got.Header().Get("X-Gregale-Outbound-Rejection") != ReasonTraceUntracked {
				t.Fatalf("an untracked trace with limits = %d %v", got.Code, got.Header())
			}
			continue
		}
		if got.Code != http.StatusOK {
			t.Fatalf("an untracked trace without limits = %d", got.Code)
		}
		if _, ok := f.backend.AITrace("llm", "new"); ok {
			t.Fatal("a trace past the day's cap was recorded")
		}
		if logged := f.backend.AIRequestLog("llm"); len(logged) != 3 || logged[2].TraceID != "new" {
			t.Fatalf("request log = %+v", logged)
		}
	}
}

// A traceparent can come from the app's own clients: past the day's traces
// a new one is let through untracked, so whoever reaches the app cannot
// fill the day and stop the integration's AI calls. A trace the app named
// is still refused.
func TestNewTraceparentTracesPastTheDayGoUntracked(t *testing.T) {
	f := newTraceFixture(t, func(i *Integration) {
		i.AIRequestLogEnabled = true
		i.AITraceMaxCalls = limit(100)
	})
	if got := f.call(api.OutboundAITraceHeader, "known"); got.Code != http.StatusOK {
		t.Fatalf("first call = %d", got.Code)
	}
	f.backend.mu.Lock()
	f.backend.states["llm"].traceDayCount = api.OutboundAITracesPerDay
	f.backend.mu.Unlock()
	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if got := f.call("Traceparent", traceparent); got.Code != http.StatusOK {
		t.Fatalf("a new traceparent trace past the day = %d %v", got.Code, got.Header())
	}
	if _, ok := f.backend.AITrace("llm", "4bf92f3577b34da6a3ce929d0e0e4736"); ok {
		t.Fatal("a trace past the day's cap was recorded")
	}
	if got := f.call(api.OutboundAITraceHeader, "named", "Traceparent", traceparent); got.Header().Get("X-Gregale-Outbound-Rejection") != ReasonTraceUntracked {
		t.Fatalf("a new named trace past the day = %d %v", got.Code, got.Header())
	}
}

func TestAITraceRejectionCountsInflightCalls(t *testing.T) {
	limits := aiTraceLimits{calls: 3, cost: 1_000}
	for _, tc := range []struct {
		state   aiTraceState
		reserve int64
		want    string
	}{
		{aiTraceState{known: true, calls: 1, inflightCalls: 1}, 0, ""},
		{aiTraceState{known: true, calls: 1, inflightCalls: 2}, 0, ReasonTraceCallLimit},
		{aiTraceState{known: true, cost: 600, inflightCost: 300}, 100, ""},
		{aiTraceState{known: true, cost: 600, inflightCost: 300}, 101, ReasonTraceCostLimit},
		{aiTraceState{known: true, cost: 1_000}, 0, ReasonTraceCostLimit},
		{aiTraceState{dayTraces: api.OutboundAITracesPerDay, named: true}, 0, ReasonTraceUntracked},
		{aiTraceState{dayTraces: api.OutboundAITracesPerDay}, 0, ""},
		{aiTraceState{known: true, dayTraces: api.OutboundAITracesPerDay}, 0, ""},
	} {
		if got := aiTraceRejection(tc.state, limits, aiReservation{cost: tc.reserve}); got != tc.want {
			t.Errorf("%+v reserve %d = %q, want %q", tc.state, tc.reserve, got, tc.want)
		}
	}
	if got := aiTraceRejection(aiTraceState{dayTraces: api.OutboundAITracesPerDay}, aiTraceLimits{}, aiReservation{}); got != "" {
		t.Fatalf("no limits = %q", got)
	}
}
