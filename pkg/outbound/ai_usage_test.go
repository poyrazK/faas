package outbound

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func sse(events ...string) string {
	var b strings.Builder
	for _, event := range events {
		b.WriteString(event)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestAIUsageObserverNormalizesProviderFormats(t *testing.T) {
	cases := []struct {
		name        string
		provider    string
		contentType string
		body        string
		want        AIUsage
	}{
		{
			name: "openai chat completion", provider: api.OutboundAIProviderOpenAI, contentType: "application/json",
			body: `{"id":"c1","model":"gpt-4o-mini-2024-07-18","choices":[{"message":{"role":"assistant","content":"hi"}}],
				"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":64}}}`,
			want: AIUsage{Model: "gpt-4o-mini-2024-07-18", Known: true, InputTokens: 100, CachedInputTokens: 64, OutputTokens: 20},
		},
		{
			name: "openai responses api", provider: api.OutboundAIProviderOpenAI, contentType: "application/json; charset=utf-8",
			body: `{"object":"response","model":"gpt-5","usage":{"input_tokens":50,"output_tokens":7,"input_tokens_details":{"cached_tokens":10}}}`,
			want: AIUsage{Model: "gpt-5", Known: true, InputTokens: 50, CachedInputTokens: 10, OutputTokens: 7},
		},
		{
			name: "openai chat stream with include_usage", provider: api.OutboundAIProviderOpenAI, contentType: "text/event-stream",
			body: sse(
				`data: {"model":"gpt-4o","choices":[{"delta":{"content":"he"}}],"usage":null}`,
				`data: {"model":"gpt-4o","choices":[{"delta":{"content":"llo"}}],"usage":null}`,
				`data: {"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":2}}`,
				`data: [DONE]`),
			want: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 9, OutputTokens: 2},
		},
		{
			name: "openai stream without usage", provider: api.OutboundAIProviderOpenAI, contentType: "text/event-stream",
			body: sse(`data: {"model":"gpt-4o","choices":[{"delta":{"content":"x"}}]}`, `data: [DONE]`),
			want: AIUsage{Model: "gpt-4o"},
		},
		{
			name: "openai responses stream", provider: api.OutboundAIProviderOpenAI, contentType: "text/event-stream",
			body: sse(
				"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-5\",\"usage\":null}}",
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}",
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5\",\"usage\":{\"input_tokens\":12,\"output_tokens\":34}}}"),
			want: AIUsage{Model: "gpt-5", Known: true, InputTokens: 12, OutputTokens: 34},
		},
		{
			name: "anthropic message", provider: api.OutboundAIProviderAnthropic, contentType: "application/json",
			body: `{"type":"message","model":"claude-sonnet-5","content":[{"type":"text","text":"hi"}],
				"usage":{"input_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":5,"output_tokens":42}}`,
			want: AIUsage{Model: "claude-sonnet-5", Known: true, InputTokens: 115, CachedInputTokens: 100, CacheWriteTokens: 5, OutputTokens: 42},
		},
		{
			name: "anthropic stream", provider: api.OutboundAIProviderAnthropic, contentType: "text/event-stream",
			body: sse(
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-5\",\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":3,\"output_tokens\":1}}}",
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}",
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":55}}",
				"event: message_stop\ndata: {\"type\":\"message_stop\"}"),
			want: AIUsage{Model: "claude-sonnet-5", Known: true, InputTokens: 13, CachedInputTokens: 3, OutputTokens: 55},
		},
		{
			name: "crlf stream without trailing blank line", provider: api.OutboundAIProviderOpenAI, contentType: "text/event-stream",
			body: "data: {\"model\":\"m\",\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":5}}\r\n",
			want: AIUsage{Model: "m", Known: true, InputTokens: 4, OutputTokens: 5},
		},
		{
			name: "hostile counts are clamped", provider: api.OutboundAIProviderOpenAI, contentType: "application/json",
			body: `{"model":"m","usage":{"prompt_tokens":-5,"completion_tokens":99999999999999}}`,
			want: AIUsage{Model: "m", Known: true, InputTokens: 0, OutputTokens: api.OutboundAIMaxTokensPerResponse},
		},
		{
			name: "unparsed media type", provider: api.OutboundAIProviderOpenAI, contentType: "text/plain",
			body: `{"model":"m","usage":{"prompt_tokens":4}}`,
			want: AIUsage{Model: "unknown"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			whole := newAIUsageObserver(tc.provider, tc.contentType, nil)
			_, _ = whole.Write([]byte(tc.body))
			if got := whole.Finish(); got != tc.want {
				t.Fatalf("one write: got %+v, want %+v", got, tc.want)
			}
			// Providers stream at arbitrary byte boundaries.
			split := newAIUsageObserver(tc.provider, tc.contentType, nil)
			for i := 0; i < len(tc.body); i++ {
				_, _ = split.Write([]byte{tc.body[i]})
			}
			if got := split.Finish(); got != tc.want {
				t.Fatalf("byte writes: got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAIUsageObserverBoundsBufferedBodies(t *testing.T) {
	large := `{"model":"m","pad":"` + strings.Repeat("x", api.OutboundAIUsageJSONParseMaxBytes) + `","usage":{"prompt_tokens":1}}`
	observer := newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil)
	_, _ = observer.Write([]byte(large))
	// ADR-997: an oversized body is not kept, but usage at its end is read
	// from the bounded tail.
	if observer.body != nil || len(observer.tail) > api.OutboundAIUsageTailBytes {
		t.Fatalf("oversized JSON body retained: body %d, tail %d bytes", len(observer.body), len(observer.tail))
	}
	if got := observer.Finish(); !got.Known || got.InputTokens != 1 {
		t.Fatalf("usage at the end of an oversized body = %+v", got)
	}
	early := `{"usage":{"prompt_tokens":9},"pad":"` + strings.Repeat("x", api.OutboundAIUsageJSONParseMaxBytes) + `"}`
	beyond := newAIUsageObserver(api.OutboundAIProviderOpenAI, "application/json", nil)
	_, _ = beyond.Write([]byte(early))
	if got := beyond.Finish(); got.Known {
		t.Fatalf("usage before the tail of an oversized body was read: %+v", got)
	}
	longEvent := "data: {\"pad\":\"" + strings.Repeat("y", api.OutboundAIUsageEventMaxBytes) + "\"}\n\n" +
		"data: {\"model\":\"m\",\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4}}\n\n"
	stream := newAIUsageObserver(api.OutboundAIProviderOpenAI, "text/event-stream", nil)
	_, _ = stream.Write([]byte(longEvent))
	got := stream.Finish()
	if !got.Known || got.InputTokens != 3 || got.OutputTokens != 4 {
		t.Fatalf("event after an oversized event was not parsed: %+v", got)
	}
	if cap(stream.line) > api.OutboundAIUsageEventMaxBytes+4096 {
		t.Fatalf("line buffer grew past the event bound: %d", cap(stream.line))
	}
}

func TestSanitizeAIModel(t *testing.T) {
	cases := map[string]string{
		"  gpt-4o  ":                    "gpt-4o",
		"accounts/x/models/llama-v3:8b": "accounts/x/models/llama-v3:8b",
		"bad\x00\nmodelé":               "badmodel",
		strings.Repeat("a", 300):        strings.Repeat("a", api.OutboundAIModelNameMaxBytes),
	}
	for in, want := range cases {
		if got := sanitizeAIModel(in); got != want {
			t.Fatalf("sanitizeAIModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStreamingMediaType(t *testing.T) {
	for contentType, want := range map[string]bool{
		"text/event-stream":                true,
		"text/event-stream; charset=utf-8": true,
		"application/x-ndjson":             true,
		"application/json":                 false,
		"":                                 false,
	} {
		if got := streamingMediaType(contentType); got != want {
			t.Fatalf("streamingMediaType(%q) = %v, want %v", contentType, got, want)
		}
	}
}

func TestAIUsageOutcome(t *testing.T) {
	known := AIUsage{Known: true}
	for _, tc := range []struct {
		status int
		usage  AIUsage
		want   AIUsageOutcome
	}{
		{200, known, AIUsageOutcomeKnown},
		{200, AIUsage{}, AIUsageOutcomeUnknown},
		{429, known, AIUsageOutcomeError},
		{500, AIUsage{}, AIUsageOutcomeError},
	} {
		if got := aiUsageOutcome(tc.status, tc.usage); got != tc.want {
			t.Fatalf("aiUsageOutcome(%d, %+v) = %s, want %s", tc.status, tc.usage, got, tc.want)
		}
	}
}
