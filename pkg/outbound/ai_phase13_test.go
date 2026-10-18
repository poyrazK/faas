package outbound

// adr: 1435 — response checks redact or block the generated text of whole
// responses and streams, end a blocked stream with an error event that
// OpenAI-format routes translate, run the moderation check on answers, and
// read provider bytes uncompressed.

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

const testSecret = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"

func responsePolicy(response api.OutboundAIResponsePolicy, terms ...string) api.OutboundAIContentPolicy {
	return api.OutboundAIContentPolicy{BlockedTerms: terms, Response: response}
}

func TestResponseTextsFindGeneratedText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format aiWireFormat
		body   string
		stream bool
		want   []string
	}{
		{"chat completion, not tool arguments", aiFormatOpenAI,
			`{"choices":[{"index":0,"message":{"content":"hi","refusal":null,"tool_calls":[{"function":{"arguments":"{\"to\":\"a@b.co\"}"}}]}}]}`,
			false, []string{"hi"}},
		{"chat chunk", aiFormatOpenAI, `{"choices":[{"index":1,"delta":{"content":"he"}}]}`, true, []string{"choice:1:content=he"}},
		{"responses body, not reasoning", aiFormatOpenAI,
			`{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"think"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}],"text":{"format":{"type":"text"}}}`,
			false, []string{"answer"}},
		{"responses delta", aiFormatOpenAI, `{"type":"response.output_text.delta","item_id":"m1","output_index":0,"content_index":0,"delta":"an"}`,
			true, []string{"item:m1:0:0:response.output_text.delta=an"}},
		{"responses tool arguments delta", aiFormatOpenAI, `{"type":"response.function_call_arguments.delta","delta":"{\"a\":1}"}`, true, nil},
		{"responses done", aiFormatOpenAI, `{"type":"response.output_text.done","text":"answer"}`, true, []string{"answer"}},
		{"transcription", aiFormatOpenAI, `{"text":"spoken words"}`, false, []string{"spoken words"}},
		{"anthropic message, not thinking", aiFormatAnthropic,
			`{"content":[{"type":"thinking","thinking":"hmm","signature":"sig"},{"type":"text","text":"hello"},{"type":"tool_use","input":{"q":"x"}}]}`,
			false, []string{"hello"}},
		{"anthropic delta", aiFormatAnthropic, `{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"lo"}}`,
			true, []string{"block:2=lo"}},
		{"gemini array, not thoughts", aiFormatGemini,
			`[{"candidates":[{"content":{"parts":[{"text":"plan","thought":true},{"text":"one"}]}}]},{"candidates":[{"index":0,"content":{"parts":[{"text":"two"}]}}]}]`,
			false, []string{"one", "two"}},
		{"converse body", aiFormatBedrockConverse, `{"output":{"message":{"content":[{"text":"hey"},{"toolUse":{"input":{}}}]}}}`,
			false, []string{"hey"}},
		{"llama invoke chunk", aiFormatBedrockInvoke, `{"generation":"tok"}`, true, []string{"generation=tok"}},
	} {
		value, ok := decodeResponseJSON([]byte(tc.body))
		if !ok {
			t.Fatalf("%s: undecodable", tc.name)
		}
		var got []string
		for _, loc := range responseTexts(tc.format, value, tc.stream) {
			if loc.channel != "" {
				got = append(got, loc.channel+"="+loc.text())
			} else {
				got = append(got, loc.text())
			}
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: texts = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// responseFixture serves fixed provider replies to an integration with a
// response policy.
type responseFixture struct {
	handler        *Handler
	backend        *MemoryBackend
	token          AIAccessToken
	mu             sync.Mutex
	contentType    string
	body           string
	status         int
	acceptEncoding []string
}

func newResponseFixture(t *testing.T, policy api.OutboundAIContentPolicy) *responseFixture {
	t.Helper()
	f := &responseFixture{contentType: "application/json", status: http.StatusOK}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		contentType, body, status := f.contentType, f.body, f.status
		f.acceptEncoding = append(f.acceptEncoding, r.Header.Get("Accept-Encoding"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", contentType)
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && contentType == "application/json" {
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(status)
			zipped := gzip.NewWriter(w)
			_, _ = io.WriteString(zipped, body)
			_ = zipped.Close()
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "llm", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	integration.AIContentPolicy = policy
	integration.AILogExport = true
	f.handler, f.backend = phase9Handler(t, upstream.Client(), integration)
	f.token = AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	return f
}

func (f *responseFixture) reply(contentType, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contentType, f.body = contentType, body
}

func (f *responseFixture) chat(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return asApp(f.handler, http.MethodPost, "/i/llm/v1/chat/completions", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`, f.token)
}

func chatReply(content string) string {
	reply, _ := json.Marshal(map[string]any{"id": "c1", "object": "chat.completion", "model": "gpt-5",
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content,
			"tool_calls": []any{map[string]any{"type": "function", "function": map[string]any{"name": "mail", "arguments": `{"to":"ann@example.com"}`}}}}}},
		"usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 5}})
	return string(reply)
}

func TestWholeResponsesAreRedactedOrBlocked(t *testing.T) {
	f := newResponseFixture(t, responsePolicy(api.OutboundAIResponsePolicy{Email: api.OutboundAIContentRedact,
		Secret: api.OutboundAIContentBlock, BlockedTerms: api.OutboundAIContentBlock}, "Project Falcon"))
	f.reply("application/json", chatReply("Write to bob@example.com <now> & then"))
	got := f.chat(t)
	var reply struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &reply) != nil ||
		reply.Choices[0].Message.Content != "Write to [REDACTED:email] <now> & then" ||
		reply.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"to":"ann@example.com"}` ||
		got.Header().Get(AIResponseRedactionsHeader) != "email=1" || got.Header().Get("Content-Length") != strconv.Itoa(got.Body.Len()) {
		t.Fatalf("redacted reply = %d %v %s", got.Code, got.Header(), got.Body.String())
	}
	exports := f.backend.AILogExports()
	if len(exports) != 1 || !strings.Contains(exports[0].Line, `"response_check":"redacted","response_findings":["email"]`) {
		t.Fatalf("export lines = %+v", exports)
	}

	for _, content := range []string{"Here is a key: " + testSecret, "About project falcon, sure."} {
		f.reply("application/json", chatReply(content))
		got = f.chat(t)
		if got.Code != http.StatusUnprocessableEntity || !strings.Contains(got.Body.String(), aiResponseBlockedCode) ||
			strings.Contains(got.Body.String(), "falcon") || strings.Contains(got.Body.String(), "sk-proj") {
			t.Fatalf("blocked reply = %d %s", got.Code, got.Body.String())
		}
	}
	if row, ok := f.backend.AIUsageRollup("llm", f.token.AppID, "gpt-5"); !ok || row.Requests != 3 || row.OutputTokens != 15 {
		t.Fatalf("blocked replies still settle their usage: %+v, %v", row, ok)
	}

	f.reply("application/json", `{"choices":[`)
	if got = f.chat(t); got.Code != http.StatusBadGateway || !strings.Contains(got.Body.String(), aiResponseUncheckedCode) {
		t.Fatalf("unreadable reply = %d %s", got.Code, got.Body.String())
	}
	f.reply("text/plain", "call me at bob@example.com")
	got = asApp(f.handler, http.MethodPost, "/i/llm/v1/audio/transcriptions", `{"model":"gpt-4o-transcribe"}`, f.token)
	if got.Code != http.StatusOK || got.Body.String() != "call me at [REDACTED:email]" {
		t.Fatalf("text transcription = %d %s", got.Code, got.Body.String())
	}
	f.reply("application/json", `{"data":[{"embedding":[0.1]}],"model":"text-embedding-3-small","usage":{"prompt_tokens":2}}`)
	if got = asApp(f.handler, http.MethodPost, "/i/llm/v1/embeddings", `{"model":"text-embedding-3-small","input":"x"}`, f.token); got.Code != http.StatusOK ||
		got.Header().Get(AIResponseRedactionsHeader) != "" {
		t.Fatalf("embeddings are not checked: %d %v", got.Code, got.Header())
	}
}

func TestResponseChecksLeaveOtherCallsAlone(t *testing.T) {
	f := newResponseFixture(t, api.OutboundAIContentPolicy{Email: api.OutboundAIContentRedact})
	body := chatReply("Write to bob@example.com")
	f.reply("application/json", body)
	if got := f.chat(t); got.Code != http.StatusOK || got.Body.String() != body || got.Header().Get(AIResponseRedactionsHeader) != "" {
		t.Fatalf("a request-only policy changed the reply: %d %s", got.Code, got.Body.String())
	}
	f = newResponseFixture(t, responsePolicy(api.OutboundAIResponsePolicy{Secret: api.OutboundAIContentBlock}))
	f.mu.Lock()
	f.status, f.body = http.StatusBadRequest, `{"error":{"message":"bad key `+testSecret+`"}}`
	f.mu.Unlock()
	if got := f.chat(t); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), testSecret) {
		t.Fatalf("a provider error was checked: %d %s", got.Code, got.Body.String())
	}
}

func TestAIProviderBytesArriveUncompressed(t *testing.T) {
	f := newResponseFixture(t, api.OutboundAIContentPolicy{})
	f.reply("application/json", chatReply("hello"))
	request := httptest.NewRequest(http.MethodPost, "/i/llm/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "br")
	request = request.WithContext(withAccessToken(request.Context(), f.token))
	got := httptest.NewRecorder()
	f.handler.ServeHTTP(got, request)
	if got.Code != http.StatusOK || got.Header().Get("Content-Encoding") != "" || !strings.Contains(got.Body.String(), `"hello"`) {
		t.Fatalf("reply = %d %v %q", got.Code, got.Header(), got.Body.String())
	}
	if f.acceptEncoding[0] != "gzip" {
		t.Fatalf("the provider saw Accept-Encoding %q", f.acceptEncoding[0])
	}
	if row, ok := f.backend.AIUsageRollup("llm", f.token.AppID, "gpt-5"); !ok || row.InputTokens != 7 || row.OutputTokens != 5 {
		t.Fatalf("usage of a compressed reply = %+v, %v", row, ok)
	}
}

func TestResponseModerationOnWholeResponses(t *testing.T) {
	for _, tc := range []struct {
		action, verdict string
		wantCode        int
		wantHeader      string
	}{
		{api.OutboundAIModerationBlock, `{"flagged": ["hate"]}`, http.StatusUnprocessableEntity, "flagged=hate"},
		{api.OutboundAIModerationFlag, `{"flagged": ["hate"]}`, http.StatusOK, "flagged=hate"},
		{api.OutboundAIModerationBlock, `{"flagged": []}`, http.StatusOK, "passed"},
	} {
		f := newPhase8Fixture(t, func(integrations map[string]*Integration) {
			policy := moderationPolicy(api.OutboundAIModerationKindClassifier, api.OutboundAIModerationOff,
				api.OutboundAIModerationOnErrorAllow, api.OutboundAIModerationPromptInjection, api.OutboundAIModerationHate)
			policy.Response.Moderation = tc.action
			integrations["primary"].AIContentPolicy = policy
		})
		f.verdict = tc.verdict
		got := f.post("/i/primary/v1/chat/completions", `{"model":"gpt-current","messages":[{"role":"user","content":"hi"}]}`, nil)
		if got.Code != tc.wantCode || got.Header().Get(AIResponseModerationHeader) != tc.wantHeader || got.Header().Get(AIModerationHeader) != "" {
			t.Fatalf("%s %s: %d %v %s", tc.action, tc.verdict, got.Code, got.Header(), got.Body.String())
		}
		if calls := f.calls(); len(calls) != 2 || calls[0] != "/v1/chat/completions gpt-current" || calls[1] != "/v1/chat/completions judge" {
			t.Fatalf("%s: provider calls = %q", tc.action, calls)
		}
	}
}

// pieceReader returns its bytes a few at a time, as a stream arrives.
type pieceReader struct {
	data  []byte
	piece int
}

func (r *pieceReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data[:min(len(r.data), r.piece, len(p))])
	r.data = r.data[n:]
	return n, nil
}

func runGuard(t *testing.T, stream []byte, format aiWireFormat, aws bool, policy api.OutboundAIContentPolicy,
	moderate func(string) ([]string, error)) ([]byte, *aiResponseCheck) {
	t.Helper()
	check := &aiResponseCheck{}
	guard := newResponseStreamGuard(io.NopCloser(&pieceReader{data: stream, piece: 37}), format, aws, policy, moderate, check,
		func() { check.observe(nil, "test") })
	out, err := io.ReadAll(guard)
	if err != nil {
		t.Fatal(err)
	}
	_ = guard.Close()
	return out, check
}

func sseEvent(value any) string {
	encoded, _ := json.Marshal(value)
	return "data: " + string(encoded) + "\n\n"
}

func streamChunk(index int, content string) string {
	return sseEvent(map[string]any{"object": "chat.completion.chunk", "choices": []any{
		map[string]any{"index": index, "delta": map[string]any{"content": content}}}})
}

// streamedChoices joins each choice's streamed content.
func streamedChoices(t *testing.T, out []byte) map[float64]string {
	t.Helper()
	texts := map[float64]string{}
	for _, event := range strings.Split(strings.TrimSpace(string(out)), "\n\n") {
		data, ok := strings.CutPrefix(event, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Index float64 `json:"index"`
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("event %q: %v", event, err)
		}
		for _, choice := range chunk.Choices {
			texts[choice.Index] += choice.Delta.Content
		}
	}
	return texts
}

func TestStreamRedactsFindingsSplitAcrossEvents(t *testing.T) {
	filler := strings.Repeat("lorem ipsum ", 80)
	var stream strings.Builder
	stream.WriteString(sseEvent(map[string]any{"object": "chat.completion.chunk", "choices": []any{
		map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}}}}))
	// Two choices interleave; the second's address is split in three.
	for _, piece := range [][2]string{{"Hello ", "Mail bob.sm"}, {"there, ", "ith@exam"}, {"friend. ", "ple.com now. "}} {
		stream.WriteString(streamChunk(0, piece[0]))
		stream.WriteString(streamChunk(1, piece[1]))
	}
	for index := 0; index < len(filler); index += 40 {
		stream.WriteString(streamChunk(0, filler[index:min(index+40, len(filler))]))
		stream.WriteString(streamChunk(1, filler[index:min(index+40, len(filler))]))
	}
	stream.WriteString("data: [DONE]\n\n")
	out, check := runGuard(t, []byte(stream.String()), aiFormatOpenAI, false,
		responsePolicy(api.OutboundAIResponsePolicy{Email: api.OutboundAIContentRedact}), nil)
	texts := streamedChoices(t, out)
	if texts[0] != "Hello there, friend. "+filler || texts[1] != "Mail [REDACTED:email] now. "+filler {
		t.Fatalf("streamed texts = %v", texts)
	}
	if !strings.HasSuffix(string(out), "data: [DONE]\n\n") || check.result != aiResponseRedacted || check.redacted["email"] != 1 {
		t.Fatalf("check = %+v; tail %q", check, string(out[len(out)-20:]))
	}
}

func TestStreamBlockEndsWithAnErrorEvent(t *testing.T) {
	filler := strings.Repeat("abcdefghij", 100)
	var stream strings.Builder
	for index := 0; index < len(filler); index += 50 {
		stream.WriteString(streamChunk(0, filler[index:index+50]))
	}
	stream.WriteString(streamChunk(0, " key "+testSecret[:12]))
	stream.WriteString(streamChunk(0, testSecret[12:]+" more"))
	stream.WriteString(streamChunk(0, filler))
	stream.WriteString("data: [DONE]\n\n")
	out, check := runGuard(t, []byte(stream.String()), aiFormatOpenAI, false,
		responsePolicy(api.OutboundAIResponsePolicy{Secret: api.OutboundAIContentBlock}), nil)
	events := strings.Split(strings.TrimSuffix(string(out), "\n\n"), "\n\n")
	last := events[len(events)-1]
	if !strings.HasPrefix(last, "event: error\ndata: ") || !strings.Contains(last, `"code":"outbound_ai_response_blocked"`) ||
		strings.Contains(string(out), "sk-proj") || strings.Contains(string(out), "[DONE]") {
		t.Fatalf("blocked stream = %q", out)
	}
	if released := streamedChoices(t, []byte(strings.Join(events[:len(events)-1], "\n\n")+"\n\n"))[0]; !strings.HasPrefix(filler, released) ||
		len(released) < len(filler)-api.OutboundAIResponseHoldBackBytes-50 {
		t.Fatalf("released %d bytes of text before the block", len(released))
	}
	if check.result != aiResponseBlocked || check.blocked != "secret" {
		t.Fatalf("check = %+v", check)
	}
	if code, _ := gatewayErrorCode([]byte(strings.TrimPrefix(last, "event: error\ndata: "))); code != aiResponseBlockedCode {
		t.Fatalf("gateway error code = %q", code)
	}
}

func TestStreamHoldsBackRecentText(t *testing.T) {
	check := &aiResponseCheck{}
	guard := newResponseStreamGuard(io.NopCloser(strings.NewReader("")), aiFormatOpenAI, false,
		responsePolicy(api.OutboundAIResponsePolicy{Email: api.OutboundAIContentRedact}), nil, check, nil)
	guard.accept([]byte(streamChunk(0, strings.Repeat("a", 100))))
	guard.accept([]byte(": keep-alive\n\n"))
	if len(guard.out) != 0 {
		t.Fatalf("released early: %q", guard.out)
	}
	guard.accept([]byte(streamChunk(0, strings.Repeat("b", api.OutboundAIResponseHoldBackBytes))))
	if !strings.Contains(string(guard.out), strings.Repeat("a", 100)) || !strings.HasSuffix(string(guard.out), ": keep-alive\n\n") ||
		strings.Contains(string(guard.out), "bbb") {
		t.Fatalf("released = %q", guard.out)
	}
}

func TestStreamReleasesEndedBlocks(t *testing.T) {
	check := &aiResponseCheck{}
	guard := newResponseStreamGuard(io.NopCloser(strings.NewReader("")), aiFormatAnthropic, false,
		responsePolicy(api.OutboundAIResponsePolicy{Email: api.OutboundAIContentRedact}), nil, check, nil)
	guard.accept([]byte("event: content_block_delta\n" + sseEvent(map[string]any{"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": "Let me look that up."}})))
	if len(guard.out) != 0 {
		t.Fatalf("released early: %q", guard.out)
	}
	guard.accept([]byte("event: content_block_stop\n" + sseEvent(map[string]any{"type": "content_block_stop", "index": 0})))
	guard.accept([]byte("event: content_block_start\n" + sseEvent(map[string]any{"type": "content_block_start", "index": 1,
		"content_block": map[string]any{"type": "tool_use", "name": "search"}})))
	if !strings.Contains(string(guard.out), "Let me look that up.") || !strings.Contains(string(guard.out), "tool_use") {
		t.Fatalf("an ended block was held: %q", guard.out)
	}
}

func TestResponsesStreamRedactsDeltasAndDoneEvents(t *testing.T) {
	full := "Reach me at bob@example.com, thanks."
	var stream strings.Builder
	stream.WriteString("event: response.created\n" + sseEvent(map[string]any{"type": "response.created", "response": map[string]any{"output": []any{}}}))
	for _, piece := range []string{"Reach me at bo", "b@exa", "mple.com, thanks."} {
		stream.WriteString("event: response.output_text.delta\n" + sseEvent(map[string]any{"type": "response.output_text.delta",
			"item_id": "m1", "output_index": 0, "content_index": 0, "delta": piece}))
	}
	stream.WriteString("event: response.output_text.done\n" + sseEvent(map[string]any{"type": "response.output_text.done", "item_id": "m1", "text": full}))
	stream.WriteString("event: response.completed\n" + sseEvent(map[string]any{"type": "response.completed", "response": map[string]any{
		"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": full}}}}}}))
	out, check := runGuard(t, []byte(stream.String()), aiFormatOpenAI, false,
		responsePolicy(api.OutboundAIResponsePolicy{Email: api.OutboundAIContentRedact}), nil)
	redacted := "Reach me at [REDACTED:email], thanks."
	var deltas string
	for _, event := range strings.Split(strings.TrimSpace(string(out)), "\n\n") {
		lines := strings.SplitN(event, "\n", 2)
		if !strings.HasPrefix(lines[0], "event: response.") || len(lines) != 2 {
			t.Fatalf("event lost its name: %q", event)
		}
		var data struct {
			Type, Delta, Text string
			Response          struct {
				Output []struct {
					Content []struct{ Text string } `json:"content"`
				} `json:"output"`
			} `json:"response"`
		}
		_ = json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &data)
		switch data.Type {
		case "response.output_text.delta":
			deltas += data.Delta
		case "response.output_text.done":
			if data.Text != redacted {
				t.Fatalf("done text = %q", data.Text)
			}
		case "response.completed":
			if data.Response.Output[0].Content[0].Text != redacted {
				t.Fatalf("completed text = %q", data.Response.Output[0].Content[0].Text)
			}
		}
	}
	if deltas != redacted || check.redacted["email"] != 1 {
		t.Fatalf("deltas = %q, check = %+v", deltas, check)
	}
}

func TestAnthropicStreamRedacts(t *testing.T) {
	var stream strings.Builder
	stream.WriteString("event: message_start\n" + sseEvent(map[string]any{"type": "message_start", "message": map[string]any{"content": []any{}}}))
	for _, piece := range []string{"card 4111 1111 ", "1111 1111 ok"} {
		stream.WriteString("event: content_block_delta\n" + sseEvent(map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": piece}}))
	}
	stream.WriteString("event: message_stop\n" + sseEvent(map[string]any{"type": "message_stop"}))
	out, _ := runGuard(t, []byte(stream.String()), aiFormatAnthropic, false,
		responsePolicy(api.OutboundAIResponsePolicy{PaymentCard: api.OutboundAIContentRedact}), nil)
	if strings.Contains(string(out), "4111") || !strings.Contains(string(out), "[REDACTED:payment_card]") ||
		!strings.Contains(string(out), "event: message_stop") {
		t.Fatalf("anthropic stream = %q", out)
	}
}

func awsFrame(eventType string, payload any) []byte {
	var headers []byte
	headers = append(headers, awsEventStreamStringHeader(":message-type", "event")...)
	headers = append(headers, awsEventStreamStringHeader(":event-type", eventType)...)
	body, _ := json.Marshal(payload)
	return encodeAWSEventMessage(headers, body)
}

// awsFrames checks each frame's CRCs and returns its types and payloads.
func awsFrames(t *testing.T, out []byte) [][3]string {
	t.Helper()
	var frames [][3]string
	for len(out) > 0 {
		total := int(binary.BigEndian.Uint32(out[0:4]))
		frame := out[:total]
		if crc32.ChecksumIEEE(frame[:8]) != binary.BigEndian.Uint32(frame[8:12]) ||
			crc32.ChecksumIEEE(frame[:total-4]) != binary.BigEndian.Uint32(frame[total-4:]) {
			t.Fatalf("frame CRC mismatch: %x", frame)
		}
		headerLen := int(binary.BigEndian.Uint32(frame[4:8]))
		messageType, eventType := awsEventStreamTypes(frame[12 : 12+headerLen])
		frames = append(frames, [3]string{messageType, eventType, string(frame[12+headerLen : total-4])})
		out = out[total:]
	}
	return frames
}

func TestBedrockStreamsAreRewrittenAndEnded(t *testing.T) {
	var stream []byte
	stream = append(stream, awsFrame("messageStart", map[string]any{"role": "assistant"})...)
	for _, piece := range []string{"mail ann@", "example.com please"} {
		stream = append(stream, awsFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": piece}})...)
	}
	stream = append(stream, awsFrame("messageStop", map[string]any{"stopReason": "end_turn"})...)
	out, _ := runGuard(t, stream, aiFormatBedrockConverse, true, responsePolicy(api.OutboundAIResponsePolicy{Email: api.OutboundAIContentRedact}), nil)
	var text string
	frames := awsFrames(t, out)
	for _, frame := range frames {
		var payload struct {
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(frame[2]), &payload)
		text += payload.Delta.Text
	}
	if len(frames) != 4 || frames[3][1] != "messageStop" || text != "mail [REDACTED:email] please" {
		t.Fatalf("converse frames = %q, text %q", frames, text)
	}

	inner, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "token " + testSecret}})
	chunk := awsFrame("chunk", map[string]any{"bytes": base64.StdEncoding.EncodeToString(inner)})
	out, check := runGuard(t, chunk, aiFormatBedrockInvoke, true, responsePolicy(api.OutboundAIResponsePolicy{Secret: api.OutboundAIContentBlock}), nil)
	frames = awsFrames(t, out)
	if len(frames) != 1 || frames[0][0] != "exception" || !strings.Contains(frames[0][2], aiResponseBlockedCode) || check.blocked != "secret" {
		t.Fatalf("invoke frames = %q", frames)
	}
}

func TestStreamModeration(t *testing.T) {
	stream := streamChunk(0, "some ") + streamChunk(0, "answer") + "data: [DONE]\n\n"
	policy := func(action string) api.OutboundAIContentPolicy {
		policy := responsePolicy(api.OutboundAIResponsePolicy{Moderation: action})
		policy.Moderation.OnError = api.OutboundAIModerationOnErrorBlock
		return policy
	}
	var checked string
	flagged := func(text string) ([]string, error) { checked = text; return []string{"hate"}, nil }
	out, check := runGuard(t, []byte(stream), aiFormatOpenAI, false, policy(api.OutboundAIModerationFlag), flagged)
	if string(out) != stream || checked != "some answer" || check.result != aiResponseFlagged || check.moderation.categories[0] != "hate" {
		t.Fatalf("flagged stream = %q, check %+v, checked %q", out, check, checked)
	}
	out, check = runGuard(t, []byte(stream), aiFormatOpenAI, false, policy(api.OutboundAIModerationBlock), flagged)
	if !strings.HasPrefix(string(out), "event: error\n") || !strings.Contains(string(out), aiResponseFlaggedCode) || strings.Contains(string(out), "answer") ||
		check.result != aiResponseBlocked {
		t.Fatalf("blocked stream = %q, check %+v", out, check)
	}
	passed := func(string) ([]string, error) { return nil, nil }
	if out, check = runGuard(t, []byte(stream), aiFormatOpenAI, false, policy(api.OutboundAIModerationBlock), passed); string(out) != stream ||
		check.result != aiResponsePassed {
		t.Fatalf("passed stream = %q, check %+v", out, check)
	}
	failed := func(string) ([]string, error) { return nil, errors.New("down") }
	out, check = runGuard(t, []byte(stream), aiFormatOpenAI, false, policy(api.OutboundAIModerationBlock), failed)
	if !strings.Contains(string(out), aiModerationUnavailable) || check.result != aiResponseError {
		t.Fatalf("failed check stream = %q, check %+v", out, check)
	}
}

// echoTranslator passes provider events through, as a stand-in for a
// route translator.
type echoTranslator struct{}

func (echoTranslator) json(_ int, body []byte) []byte { return body }
func (echoTranslator) event(data []byte) []byte {
	return append(append([]byte("data: "), data...), '\n', '\n')
}
func (echoTranslator) end() []byte { return []byte("data: [DONE]\n\n") }

func TestTranslatedRoutesEndOnAGatewayError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newTranslatingWriter(recorder, echoTranslator{})
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(`data: {"x":1}` + "\n\n" + "event: error\ndata: " + string(gatewayErrorPayload(aiResponseBlockedCode, "no")) +
		"\n\n" + `data: {"x":2}` + "\n\n"))
	writer.finish()
	body := recorder.Body.String()
	if !strings.Contains(body, `"code":"outbound_ai_response_blocked"`) || strings.Contains(body, `"x":2`) || strings.Contains(body, "[DONE]") ||
		!strings.HasPrefix(body, `data: {"x":1}`) {
		t.Fatalf("translated stream = %q", body)
	}
	if code, _ := gatewayErrorCode([]byte(`{"type":"error","error":{"type":"overloaded_error","code":"","message":"busy"}}`)); code != "" {
		t.Fatal("a provider error was taken for a gateway error")
	}
}

func TestOutboundAIResponsePolicyValidation(t *testing.T) {
	check := api.OutboundAIModeration{Kind: api.OutboundAIModerationKindClassifier, IntegrationID: "m", Model: "judge",
		Categories: []string{api.OutboundAIModerationHate}}
	for _, tc := range []struct {
		name   string
		policy api.OutboundAIContentPolicy
		want   bool
	}{
		{"detectors", responsePolicy(api.OutboundAIResponsePolicy{Email: "redact", Secret: "block", BlockedTerms: "block"}, "x"), true},
		{"redacting terms", responsePolicy(api.OutboundAIResponsePolicy{BlockedTerms: "redact"}), false},
		{"unknown action", responsePolicy(api.OutboundAIResponsePolicy{IBAN: "mask"}), false},
		{"moderation without a check", responsePolicy(api.OutboundAIResponsePolicy{Moderation: "block"}), false},
		{"moderation with an off request check", api.OutboundAIContentPolicy{Moderation: check, Response: api.OutboundAIResponsePolicy{Moderation: "flag"}}, true},
		{"prompt injection only", api.OutboundAIContentPolicy{Moderation: api.OutboundAIModeration{Kind: api.OutboundAIModerationKindClassifier,
			IntegrationID: "m", Model: "judge", Categories: []string{api.OutboundAIModerationPromptInjection}},
			Response: api.OutboundAIResponsePolicy{Moderation: "flag"}}, false},
	} {
		if got := tc.policy.Valid(); got != tc.want {
			t.Errorf("%s: Valid = %v", tc.name, got)
		}
	}
	kept := api.OutboundAIContentPolicy{Moderation: check, Response: api.OutboundAIResponsePolicy{Moderation: "flag"}}.Normalized()
	if kept.Moderation.IntegrationID != "m" || kept.Moderation.Action != api.OutboundAIModerationOff || !kept.Response.Active() {
		t.Fatalf("normalized = %+v", kept)
	}
	if cleared := (api.OutboundAIContentPolicy{Moderation: check}).Normalized(); cleared.Moderation.IntegrationID != "" {
		t.Fatalf("an unused check kept its settings: %+v", cleared.Moderation)
	}
}
