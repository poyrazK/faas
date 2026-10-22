package outbound

// adr: 996 — forced tool choice degrades to "auto" plus an instruction on
// Claude models that refuse it, strict tools pass through, and openai
// routes translate Responses API requests and responses.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClaudeRejectsForcedToolChoice(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-sonnet-5-5":                 true,
		"claude-opus-5-5@20261001":          true,
		"anthropic/claude-fable-5-1":        true,
		"us.anthropic.claude-opus-5-5-v1:0": true,
		"arn:aws:bedrock:us-east-1:1:inference-profile/global.anthropic.claude-mythos-5-1": true,
		"claude-opus-5":                   false,
		"claude-sonnet-5":                 false,
		"claude-sonnet-4-5":               false,
		"anthropic.claude-haiku-4-5-v1:0": false,
		"gpt-4o":                          false,
	} {
		if got := claudeRejectsForcedToolChoice(model); got != want {
			t.Errorf("%s: rejects forced tool choice = %v", model, got)
		}
	}
}

const forcedToolChat = `{"model":"x","messages":[{"role":"system","content":"Sys"},{"role":"user","content":"Weather?"}],
	"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","additionalProperties":false},"strict":true}}],
	"tool_choice":%s}`

func TestForcedToolChoiceDowngrade(t *testing.T) {
	anthropic := Integration{AIProvider: api.OutboundAIProviderAnthropic}
	newer, err := translateRouteRequest(anthropic, "/v1/chat/completions", "", []byte(strings.Replace(forcedToolChat, "%s", `"required"`, 1)), "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	body := decodeJSONMap(t, newer.body)
	if body["tool_choice"].(map[string]any)["type"] != "auto" || body["system"] != "Sys\n\nAnswer by calling one of the provided tools." ||
		body["tools"].([]any)[0].(map[string]any)["strict"] != true {
		t.Fatalf("claude-sonnet-5-5 body = %s", newer.body)
	}
	older, err := translateRouteRequest(anthropic, "/v1/chat/completions", "", []byte(strings.Replace(forcedToolChat, "%s",
		`{"type":"function","function":{"name":"weather"}}`, 1)), "claude-sonnet-4-5")
	if err != nil {
		t.Fatal(err)
	}
	oldBody := decodeJSONMap(t, older.body)
	if choice := oldBody["tool_choice"].(map[string]any); choice["type"] != "tool" || choice["name"] != "weather" || oldBody["system"] != "Sys" {
		t.Fatalf("claude-sonnet-4-5 body = %s", older.body)
	}
	bedrock, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderBedrock}, "/v1/chat/completions", "",
		[]byte(strings.Replace(forcedToolChat, "%s", `{"type":"function","function":{"name":"weather"}}`, 1)), "us.anthropic.claude-opus-5-5-v1:0")
	if err != nil {
		t.Fatal(err)
	}
	converse := decodeJSONMap(t, bedrock.body)
	tools := converse["toolConfig"].(map[string]any)
	system := converse["system"].([]any)
	if tools["toolChoice"].(map[string]any)["auto"] == nil || system[len(system)-1].(map[string]any)["text"] != "Answer by calling the weather tool." ||
		tools["tools"].([]any)[0].(map[string]any)["toolSpec"].(map[string]any)["strict"] != true {
		t.Fatalf("bedrock body = %s", bedrock.body)
	}
}

const responsesRequest = `{"model":"gpt-5","instructions":"Be brief.","max_output_tokens":64,
	"input":[{"role":"user","content":[{"type":"input_text","text":"Weather in Rome?"}]},
		{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Rome\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"sunny"},
		{"type":"reasoning","summary":[]}],
	"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}],
	"tool_choice":{"type":"function","name":"weather"},
	"text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"],"additionalProperties":false}}}}`

func TestResponsesRequestTranslation(t *testing.T) {
	translation, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderAnthropic}, "/v1/responses", "", []byte(responsesRequest), "claude-sonnet-4-5")
	if err != nil {
		t.Fatal(err)
	}
	body := decodeJSONMap(t, translation.body)
	messages := body["messages"].([]any)
	assistant := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	result := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if translation.path != "/v1/messages" || body["system"] != "Be brief." || body["max_tokens"] != float64(64) || len(messages) != 3 ||
		assistant["type"] != "tool_use" || assistant["id"] != "call_1" || result["type"] != "tool_result" ||
		body["tool_choice"].(map[string]any)["name"] != "weather" || body["output_config"] == nil {
		t.Fatalf("anthropic body = %s", translation.body)
	}
	for name, tc := range map[string]struct{ body, reason string }{
		"stored state": {`{"previous_response_id":"resp_1","input":"hi"}`, "stateless"},
		"hosted tool":  {`{"input":"hi","tools":[{"type":"web_search"}]}`, "hosted by OpenAI"},
		"file input":   {`{"input":[{"role":"user","content":[{"type":"input_file","file_id":"f"}]}]}`, `"input_file"`},
		"unknown item": {`{"input":[{"type":"computer_call"}]}`, `"computer_call"`},
	} {
		_, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderGemini}, "/v1/responses", "", []byte(tc.body), "gemini-2.5-flash")
		if !errors.Is(err, errTranslationUnsupported) || !strings.Contains(translationRefusalDetail(err), tc.reason) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	openai, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderOpenAI}, "/v1/responses", "", []byte(responsesRequest), "gpt-5-mini")
	if err != nil || openai.path != "/v1/responses" || openai.translator != nil || !strings.Contains(string(openai.body), `"model":"gpt-5-mini"`) {
		t.Fatalf("openai passthrough = %+v, %v", openai, err)
	}
	azure, err := translateRouteRequest(Integration{AIProvider: api.OutboundAIProviderAzureOpenAI}, "/v1/responses", "", []byte(responsesRequest), "prod")
	if err != nil || azure.path != "/openai/v1/responses" {
		t.Fatalf("azure passthrough = %+v, %v", azure, err)
	}
}

func TestResponsesJSONTranslation(t *testing.T) {
	translator := newResponsesTranslator(newAnthropicTranslator(false, false), "claude-sonnet-4-5")
	out := decodeJSONMap(t, translator.json(http.StatusOK, []byte(`{"id":"msg_1","model":"claude-sonnet-4-5","stop_reason":"tool_use",
		"content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"Rome"}}],
		"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":4}}`)))
	output := out["output"].([]any)
	message := output[0].(map[string]any)
	call := output[1].(map[string]any)
	usage := out["usage"].(map[string]any)
	if out["object"] != "response" || out["status"] != "completed" || !strings.HasPrefix(out["id"].(string), "resp_") ||
		message["content"].([]any)[0].(map[string]any)["text"] != "Checking." || call["type"] != "function_call" ||
		call["call_id"] != "toolu_1" || call["arguments"] != `{"city":"Rome"}` || usage["input_tokens"] != float64(14) ||
		usage["input_tokens_details"].(map[string]any)["cached_tokens"] != float64(4) {
		t.Fatalf("response = %v", out)
	}
	failed := translator.json(http.StatusBadRequest, []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`))
	if !strings.Contains(string(failed), `"message":"bad"`) {
		t.Fatalf("error = %s", failed)
	}
}

func TestResponsesStreamTranslation(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"weather"}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Rome\"}"}}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
	recorder := httptest.NewRecorder()
	writer := newTranslatingWriter(recorder, newResponsesTranslator(newAnthropicTranslator(true, true), "claude-sonnet-4-5"))
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	for i := 0; i < len(stream); i += 17 {
		_, _ = writer.Write([]byte(stream[i:min(i+17, len(stream))]))
	}
	writer.finish()
	var types []string
	var completed map[string]any
	for _, block := range strings.Split(strings.TrimSpace(recorder.Body.String()), "\n\n") {
		lines := strings.SplitN(block, "\n", 2)
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") {
			t.Fatalf("malformed event %q", block)
		}
		types = append(types, strings.TrimPrefix(lines[0], "event: "))
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] == "response.completed" {
			completed = event["response"].(map[string]any)
		}
	}
	joined := strings.Join(types, ",")
	for _, want := range []string{"response.created,response.in_progress,response.output_item.added,response.content_part.added,response.output_text.delta",
		"response.output_text.done,response.content_part.done,response.output_item.done,response.output_item.added",
		"response.function_call_arguments.delta", "response.function_call_arguments.done", "response.completed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("event sequence missing %q: %s", want, joined)
		}
	}
	output := completed["output"].([]any)
	if len(output) != 2 || output[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] != "Hello" ||
		output[1].(map[string]any)["arguments"] != `{"city":"Rome"}` || completed["usage"].(map[string]any)["output_tokens"] != float64(7) {
		t.Fatalf("completed response = %v", completed)
	}
	if strings.Contains(recorder.Body.String(), "[DONE]") || recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream framing = %s", recorder.Body.String())
	}
}
