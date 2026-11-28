package outbound

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/aiprices"
)

func planForTest(t *testing.T, integration Integration, appID, path, body string) (*aiCallPlan, *aiCallRejection) {
	t.Helper()
	ai := &aiRequest{body: []byte(body)}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/", strings.NewReader(body))
	return planAICall(request, integration, appID, path, ai, nil)
}

// Prod hunt 2026-11-28: a call asking for n answers reserved one answer's
// output, so 20 concurrent n=2000 calls spent USD 2.72 under a USD 0.33 daily
// cap. Every answer (OpenAI n and best_of, Gemini candidateCount) now counts.
func TestReservationCountsEveryAnswer(t *testing.T) {
	prices := ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: 1_000_000, OutputMillicentsPerMTok: 2_000_000},
		{ModelPrefix: "gemini-2.5-pro", InputMillicentsPerMTok: 1_000_000, OutputMillicentsPerMTok: 2_000_000},
	})
	openai := Integration{AIProvider: api.OutboundAIProviderOpenAI, AIPrices: prices}
	gemini := Integration{AIProvider: api.OutboundAIProviderGemini, AIPrices: prices}
	for _, tc := range []struct {
		name        string
		integration Integration
		path, body  string
		output      int64
	}{
		{"one answer", openai, "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":100}`, 100},
		{"n", openai, "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":100,"n":2000}`, 200_000},
		{"best_of", openai, "/v1/completions", `{"model":"gpt-4o","max_tokens":100,"best_of":5,"n":2}`, 500},
		{"candidateCount", gemini, "/v1beta/models/gemini-2.5-pro:generateContent",
			`{"generationConfig":{"maxOutputTokens":100,"candidateCount":4}}`, 400},
		{"hostile count is bounded", openai, "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":1,"n":99999999999}`, aiChoicesMax},
		// Review of #8198: the Gemini JSON API takes snake_case names too.
		{"candidate_count", gemini, "/v1beta/models/gemini-2.5-pro:generateContent",
			`{"generation_config":{"max_output_tokens":100,"candidate_count":4}}`, 400},
		// The legacy completions endpoint answers each prompt of an array n times.
		{"prompt array", openai, "/v1/completions", `{"model":"gpt-4o","max_tokens":100,"n":2,"prompt":["a","b","c"]}`, 600},
		{"token array is one prompt", openai, "/v1/completions", `{"model":"gpt-4o","max_tokens":100,"prompt":[1,2,3]}`, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, rejection := planForTest(t, tc.integration, "", tc.path, tc.body)
			if rejection != nil {
				t.Fatal(rejection)
			}
			if plan.reserve.estimate.OutputTokens != tc.output {
				t.Fatalf("reserved output = %d, want %d", plan.reserve.estimate.OutputTokens, tc.output)
			}
			input := int64((len(tc.body) + 3) / 4)
			if want := (input*1_000_000 + tc.output*2_000_000 + 999_999) / 1_000_000; plan.reserve.cost != want {
				t.Fatalf("reserved cost = %d, want %d", plan.reserve.cost, want)
			}
		})
	}
}

// The app's output ceiling bounds a call's whole output, not one answer's.
func TestOutputCeilingCountsEveryAnswer(t *testing.T) {
	ceiling := int64(1000)
	integration := Integration{AIProvider: api.OutboundAIProviderOpenAI,
		BindingAIGuardrails: map[string]AIGuardrails{"app-1": {MaxOutputTokens: &ceiling}}}
	if _, rejection := planForTest(t, integration, "app-1", "/v1/chat/completions", `{"model":"m","max_tokens":100,"n":10}`); rejection != nil {
		t.Fatalf("10 x 100 under a 1000 ceiling refused: %+v", rejection)
	}
	rejection := func(body string) *aiCallRejection {
		_, r := planForTest(t, integration, "app-1", "/v1/chat/completions", body)
		return r
	}
	if r := rejection(`{"model":"m","max_tokens":100,"n":11}`); r == nil || r.code != "outbound_ai_max_tokens_exceeded" {
		t.Fatalf("11 x 100 over a 1000 ceiling = %+v", r)
	}
	if r := rejection(`{"model":"m","n":2000}`); r == nil || r.code != "outbound_ai_max_tokens_exceeded" {
		t.Fatalf("2000 answers with no ceiling = %+v, want refused: the ceiling cannot give each a token", r)
	}
	plan, r := planForTest(t, integration, "app-1", "/v1/chat/completions", `{"model":"m","n":4}`)
	if r != nil {
		t.Fatal(r)
	}
	if plan.facts.outputCeiling != 250 || plan.reserve.estimate.OutputTokens != 1000 {
		t.Fatalf("4 answers under 1000: per-answer ceiling %d, reserved %d; want 250 and 1000",
			plan.facts.outputCeiling, plan.reserve.estimate.OutputTokens)
	}
}

// Prod hunt 2026-11-28: a previous_response_id call costing USD 0.60 was
// admitted with USD 0.085 left, since its input was estimated from its few
// body bytes. A call continuing stored context reserves the model's context
// window; others keep the body estimate.
func TestStoredContextReservesTheContextWindow(t *testing.T) {
	integration := Integration{AIProvider: api.OutboundAIProviderOpenAI}
	window, ok := aiprices.Default().MaxInputTokens(api.OutboundAIProviderOpenAI, "gpt-4o")
	if !ok {
		t.Fatal("gpt-4o has no context window")
	}
	for _, tc := range []struct {
		name, path, body string
		input            int64
	}{
		{"previous response", "/v1/responses", `{"model":"gpt-4o","max_output_tokens":10,"previous_response_id":"resp_1","input":"go on"}`, window},
		{"conversation", "/v1/responses", `{"model":"gpt-4o","max_output_tokens":10,"conversation":"conv_1","input":"go on"}`, window},
		{"stored prompt", "/v1/responses", `{"model":"gpt-4o","max_output_tokens":10,"prompt":{"id":"pmpt_1"}}`, window},
		{"uploaded file", "/v1/responses", `{"model":"gpt-4o","max_output_tokens":10,"input":[{"role":"user","content":[{"type":"input_file","file_id":"file-1"}]}]}`, window},
		{"unknown model", "/v1/responses", `{"model":"private-x","max_output_tokens":10,"previous_response_id":"resp_1"}`, api.OutboundAIUnknownModelInputReserveTokens},
		{"legacy completion prompt text", "/v1/completions", `{"model":"gpt-4o","max_tokens":10,"prompt":"hello"}`, 0},
		{"plain chat", "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":10,"messages":[]}`, 0},
		// Review of #8198: text that mentions file_id, or a tool parameter
		// named file_id, is not an uploaded file.
		{"file_id in text", "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":10,"messages":[{"role":"user","content":"what does \"file_id\": \"x\" mean?"}]}`, 0},
		{"file_id tool parameter", "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":10,"messages":[],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"file_id":{"type":"string"}}}}}]}`, 0},
		{"anthropic document source", "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":10,"messages":[{"role":"user","content":[{"type":"document","source":{"type":"file","file_id":"file_1"}}]}]}`, window},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, rejection := planForTest(t, integration, "", tc.path, tc.body)
			if rejection != nil {
				t.Fatal(rejection)
			}
			want := tc.input
			if want == 0 {
				want = int64((len(tc.body) + 3) / 4)
			}
			if plan.reserve.estimate.InputTokens != want {
				t.Fatalf("reserved input = %d, want %d", plan.reserve.estimate.InputTokens, want)
			}
			// Review of #8198: only the cost reserves the context window;
			// token guardrails check the body estimate, or every continued
			// conversation would be refused by a tokens-per-minute guard.
			if bodyInput := int64((len(tc.body) + 3) / 4); plan.reserve.tokens != bodyInput+10 {
				t.Fatalf("reserved tokens = %d, want the body estimate %d + output 10", plan.reserve.tokens, bodyInput)
			}
		})
	}
	gemini := Integration{AIProvider: api.OutboundAIProviderGemini}
	plan, rejection := planForTest(t, gemini, "", "/v1beta/models/gemini-2.5-pro:generateContent",
		`{"cachedContent":"cachedContents/1","generationConfig":{"maxOutputTokens":10}}`)
	if rejection != nil {
		t.Fatal(rejection)
	}
	if geminiWindow, _ := aiprices.Default().MaxInputTokens(api.OutboundAIProviderGemini, "gemini-2.5-pro"); plan.reserve.estimate.InputTokens != geminiWindow {
		t.Fatalf("cached content reserved %d input tokens, want %d", plan.reserve.estimate.InputTokens, geminiWindow)
	}
}

func TestBatchRequestCountsEveryAnswer(t *testing.T) {
	n, ceiling := int64(8), int64(100)
	if got := (batchRequestBody{MaxTokens: &ceiling, N: &n}).outputCeiling(true); got != 800 {
		t.Fatalf("batch request n=8 x 100 = %d, want 800", got)
	}
	if got := (batchRequestBody{N: &n}).outputCeiling(true); got != 8*api.OutboundAIDefaultOutputReserveTokens {
		t.Fatalf("batch request n=8 with no ceiling = %d", got)
	}
}
