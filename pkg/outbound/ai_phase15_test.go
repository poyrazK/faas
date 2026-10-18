package outbound

// adr: 1437 — with automatic prompt caching on, the gateway marks a Claude
// request's repeated system prompt or tools and a continuing conversation's
// last block, on the Anthropic, Vertex and Bedrock wire forms, and leaves
// short prompts and caller-marked requests alone.

import (
	"encoding/json"
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

var longText = strings.Repeat("You are a careful assistant for the billing team. ", 120)

func testMarker(session bool) promptCacheMarker {
	return promptCacheMarker{session: session, prefixes: &promptPrefixes{}, now: time.Now(),
		key: func(parts ...json.RawMessage) [32]byte { return promptPrefixKey("i", "claude", parts...) }}
}

func anthropicBody(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestAnthropicPrefixIsMarkedOnceItRepeats(t *testing.T) {
	marker := testMarker(false)
	body := anthropicBody(t, map[string]any{"model": "claude-sonnet-5-5", "system": longText,
		"tools":    []any{map[string]any{"name": "lookup", "input_schema": map[string]any{"type": "object"}}},
		"messages": []any{map[string]any{"role": "user", "content": "What is my balance?"}}})
	if out, marks := marker.anthropic(body, false); len(marks) != 0 || string(out) != string(body) {
		t.Fatalf("a first-seen prefix was marked: %v", marks)
	}
	out, marks := marker.anthropic(body, false)
	if strings.Join(marks, ",") != promptCacheMarkPrefix {
		t.Fatalf("marks = %v", marks)
	}
	var marked struct {
		System []map[string]any `json:"system"`
		Tools  []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(out, &marked); err != nil || len(marked.System) != 1 || marked.System[0]["text"] != longText ||
		marked.System[0]["cache_control"] == nil || marked.Tools[0]["cache_control"] != nil {
		t.Fatalf("marked body = %s", out)
	}
	// A caller's own breakpoint wins; a short prompt is not worth one.
	if _, marks := marker.anthropic(out, false); len(marks) != 0 {
		t.Fatalf("a marked body was marked again: %v", marks)
	}
	short := anthropicBody(t, map[string]any{"system": "Be brief.", "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	for range 2 {
		if _, marks := testMarker(true).anthropic(short, false); len(marks) != 0 {
			t.Fatalf("a short prompt was marked: %v", marks)
		}
	}
	// Tools alone carry the prefix breakpoint on the last tool; a session
	// also marks its conversation.
	toolsOnly := anthropicBody(t, map[string]any{"tools": []any{map[string]any{"name": "a", "description": longText},
		map[string]any{"name": "b"}}, "messages": []any{map[string]any{"role": "user", "content": "go"}}})
	out, marks = testMarker(true).anthropic(toolsOnly, false)
	if strings.Join(marks, ",") != "prefix,conversation" || !strings.Contains(string(out), `{"cache_control":{"type":"ephemeral"},"name":"b"}`) {
		t.Fatalf("tools-only prefix = %v %s", marks, out)
	}
}

func TestAnthropicConversationIsMarkedAtItsLastBlock(t *testing.T) {
	conversation := func(last any) []byte {
		return anthropicBody(t, map[string]any{"system": "Short system.", "messages": []any{
			map[string]any{"role": "user", "content": longText},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "lookup", "input": map[string]any{}}}},
			map[string]any{"role": "user", "content": last},
		}})
	}
	out, marks := testMarker(false).anthropic(conversation([]any{
		map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "42"},
	}), false)
	if strings.Join(marks, ",") != promptCacheMarkConversation || !strings.Contains(string(out), `"cache_control":{"type":"ephemeral"},"content":"42","tool_use_id":"t1","type":"tool_result"`) {
		t.Fatalf("tool result conversation = %v %s", marks, out)
	}
	out, _ = testMarker(false).anthropic(conversation([]any{
		map[string]any{"type": "text", "text": "and now?"}, map[string]any{"type": "thinking", "thinking": "x", "signature": "s"},
	}), false)
	if !strings.Contains(string(out), `{"cache_control":{"type":"ephemeral"},"text":"and now?","type":"text"}`) ||
		strings.Contains(string(out), `"signature":"s","thinking":"x","type":"thinking","cache_control"`) {
		t.Fatalf("thinking block was marked or text skipped: %s", out)
	}
	out, _ = testMarker(false).anthropic(conversation("plain question"), false)
	if !strings.Contains(string(out), `"content":[{"cache_control":{"type":"ephemeral"},"text":"plain question","type":"text"}]`) {
		t.Fatalf("string content = %s", out)
	}
	// A first turn without a session is not marked; with one it is.
	first := anthropicBody(t, map[string]any{"messages": []any{map[string]any{"role": "user", "content": longText}}})
	if _, marks := testMarker(false).anthropic(first, false); len(marks) != 0 {
		t.Fatalf("a lone first turn was marked: %v", marks)
	}
	if _, marks := testMarker(true).anthropic(first, false); strings.Join(marks, ",") != promptCacheMarkConversation {
		t.Fatalf("a session's first turn = %v", marks)
	}
	// A Bedrock invoke body must be an Anthropic one.
	if _, marks := testMarker(true).anthropic(first, true); len(marks) != 0 {
		t.Fatalf("an invoke body without anthropic_version was marked: %v", marks)
	}
}

func TestConverseIsMarkedWithCachePoints(t *testing.T) {
	body := anthropicBody(t, map[string]any{
		"system": []any{map[string]any{"text": longText}},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"text": "hi"}}},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"text": "hello"}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"text": "next"}}},
		}})
	out, marks := testMarker(true).converse(body)
	if strings.Join(marks, ",") != "prefix,conversation" ||
		!strings.Contains(string(out), `"system":[{"text":"`+longText+`"},{"cachePoint":{"type":"default"}}]`) ||
		!strings.Contains(string(out), `{"text":"next"},{"cachePoint":{"type":"default"}}]`) {
		t.Fatalf("converse = %v %s", marks, out)
	}
	tools := anthropicBody(t, map[string]any{"toolConfig": map[string]any{"tools": []any{map[string]any{"toolSpec": map[string]any{"description": longText}}}},
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"text": "x"}}}}})
	if out, marks := testMarker(true).converse(tools); len(marks) == 0 || !strings.Contains(string(out), `{"cachePoint":{"type":"default"}}]},"messages"`) &&
		!strings.Contains(string(out), `"tools":[{"toolSpec"`) {
		t.Fatalf("converse tools = %v %s", marks, out)
	}
}

func TestAutoPromptCacheThroughTheGateway(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"claude-sonnet-5-5","content":[{"type":"text","text":"ok"}],`+
			`"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":1500,"cache_creation_input_tokens":0}}`)
	}))
	defer upstream.Close()
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "claude", upstream.URL, api.OutboundAIProviderAnthropic, account, app)
	integration.AIAutoPromptCache = true
	handler, backend := phase9Handler(t, upstream.Client(), integration)
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	request := anthropicBody(t, map[string]any{"model": "claude-sonnet-5-5", "max_tokens": 100, "system": longText,
		"messages": []any{map[string]any{"role": "user", "content": "balance?"}}})
	first := asApp(handler, http.MethodPost, "/i/claude/v1/messages", string(request), token)
	second := asApp(handler, http.MethodPost, "/i/claude/v1/messages", string(request), token)
	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Header().Get(AIPromptCacheHeader) != "" ||
		second.Header().Get(AIPromptCacheHeader) != promptCacheMarkPrefix {
		t.Fatalf("calls = %d %q, %d %q", first.Code, first.Header().Get(AIPromptCacheHeader), second.Code, second.Header().Get(AIPromptCacheHeader))
	}
	if strings.Contains(bodies[0], "cache_control") || !strings.Contains(bodies[1], `"cache_control":{"type":"ephemeral"}`) {
		t.Fatalf("provider bodies = %q", bodies)
	}
	if row, ok := backend.AIUsageRollup("claude", app, "claude-sonnet-5-5"); !ok || row.CachedInputTokens == 0 {
		t.Fatalf("cached input tokens = %+v, %v", row, ok)
	}

	integration.AIAutoPromptCache = false
	handler, _ = phase9Handler(t, upstream.Client(), integration)
	for range 2 {
		if got := asApp(handler, http.MethodPost, "/i/claude/v1/messages", string(request), token); got.Header().Get(AIPromptCacheHeader) != "" {
			t.Fatal("an integration without automatic caching was marked")
		}
	}
	if !api.PutOutboundAIPolicyRequest.Valid(api.PutOutboundAIPolicyRequest{AIProvider: "anthropic", AutoPromptCache: true}) ||
		(api.PutOutboundAIPolicyRequest{AutoPromptCache: true}).Valid() {
		t.Fatal("auto_prompt_cache needs a provider")
	}
}
