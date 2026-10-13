package outbound

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// openai-format routes accept OpenAI Responses API requests (ADR-996).
// OpenAI and Azure OpenAI targets get them unchanged. For other targets the
// request becomes a chat-completions request, goes through that target's
// translator, and the chat completion comes back as a Responses object or
// Responses stream events. Translated targets are stateless: requests that
// depend on stored responses or OpenAI-hosted tools are refused.

type openAIResponsesRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"`
	Instructions       string            `json:"instructions"`
	Tools              []json.RawMessage `json:"tools"`
	ToolChoice         json.RawMessage   `json:"tool_choice"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls"`
	MaxOutputTokens    *int64            `json:"max_output_tokens"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"top_p"`
	Stream             bool              `json:"stream"`
	User               string            `json:"user"`
	PreviousResponseID string            `json:"previous_response_id"`
	Conversation       json.RawMessage   `json:"conversation"`
	Prompt             json.RawMessage   `json:"prompt"`
	Background         bool              `json:"background"`
	Text               *struct {
		Format json.RawMessage `json:"format"`
	} `json:"text"`
}

func translateResponsesRequest(integration Integration, providerPath, rawQuery string, body []byte, model string) (routeTranslation, error) {
	switch integration.AIProvider {
	case api.OutboundAIProviderOpenAI:
		if model != "" {
			rewritten, err := withModel(body, model)
			if err != nil {
				return routeTranslation{}, errTranslationUnsupported
			}
			body = rewritten
		}
		return routeTranslation{path: providerPath, rawQuery: rawQuery, body: body}, nil
	case api.OutboundAIProviderAzureOpenAI:
		rewritten, err := withModel(body, model)
		if err != nil {
			return routeTranslation{}, errTranslationUnsupported
		}
		return routeTranslation{path: "/openai/v1/responses", body: rewritten}, nil
	}
	var request openAIResponsesRequest
	if json.Unmarshal(body, &request) != nil {
		return routeTranslation{}, errTranslationUnsupported
	}
	chat, err := responsesToChat(request)
	if err != nil {
		return routeTranslation{}, err
	}
	translation, err := translateChatRequest(integration, chat, model)
	if err != nil {
		return routeTranslation{}, err
	}
	translation.translator = newResponsesTranslator(translation.translator, model)
	return translation, nil
}

// responsesToChat maps a stateless Responses request to chat completions.
func responsesToChat(request openAIResponsesRequest) (openAIChatRequest, error) {
	switch {
	case request.PreviousResponseID != "" || hasJSONValue(request.Conversation):
		return openAIChatRequest{}, refuseTranslation("translated targets are stateless; send the full input instead of previous_response_id or conversation")
	case hasJSONValue(request.Prompt):
		return openAIChatRequest{}, refuseTranslation("stored prompts are not supported for translated targets")
	case request.Background:
		return openAIChatRequest{}, refuseTranslation("background responses are not supported for translated targets")
	}
	chat := openAIChatRequest{Model: request.Model, ToolChoice: responsesToolChoice(request.ToolChoice),
		ParallelToolCalls: request.ParallelToolCalls, MaxCompletionTokens: request.MaxOutputTokens,
		Temperature: request.Temperature, TopP: request.TopP, Stream: request.Stream, User: request.User}
	if request.Stream {
		// The usage chunk feeds response.completed.
		chat.StreamOptions = &openAIStreamUsage{IncludeUsage: true}
	}
	if request.Instructions != "" {
		chat.Messages = append(chat.Messages, openAIMessage{Role: "system", Content: jsonStringValue(request.Instructions)})
	}
	messages, err := responsesInputMessages(request.Input)
	if err != nil {
		return openAIChatRequest{}, err
	}
	chat.Messages = append(chat.Messages, messages...)
	for _, raw := range request.Tools {
		tool, err := responsesTool(raw)
		if err != nil {
			return openAIChatRequest{}, err
		}
		chat.Tools = append(chat.Tools, tool)
	}
	if request.Text != nil && hasJSONValue(request.Text.Format) {
		format, err := responsesTextFormat(request.Text.Format)
		if err != nil {
			return openAIChatRequest{}, err
		}
		chat.ResponseFormat = format
	}
	return chat, nil
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func jsonStringValue(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Output    json.RawMessage `json:"output"`
}

// responsesInputMessages reads input as one user string or a list of
// message, function_call and function_call_output items. Reasoning items
// are dropped; other item types are refused.
func responsesInputMessages(raw json.RawMessage) ([]openAIMessage, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []openAIMessage{{Role: "user", Content: jsonStringValue(text)}}, nil
	}
	var items []responsesInputItem
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return nil, refuseTranslation("input must be a string or a list of input items")
	}
	var messages []openAIMessage
	for _, item := range items {
		switch {
		case item.Type == "message" || (item.Type == "" && item.Role != ""):
			content, err := responsesMessageContent(item.Content)
			if err != nil {
				return nil, err
			}
			messages = append(messages, openAIMessage{Role: item.Role, Content: content})
		case item.Type == "function_call":
			call := openAIToolCall{ID: item.CallID, Type: "function"}
			call.Function.Name, call.Function.Arguments = item.Name, item.Arguments
			if n := len(messages); n > 0 && messages[n-1].Role == "assistant" && len(messages[n-1].ToolCalls) > 0 {
				messages[n-1].ToolCalls = append(messages[n-1].ToolCalls, call)
				continue
			}
			messages = append(messages, openAIMessage{Role: "assistant", Content: json.RawMessage("null"), ToolCalls: []openAIToolCall{call}})
		case item.Type == "function_call_output":
			var output string
			if json.Unmarshal(item.Output, &output) != nil {
				return nil, refuseTranslation("function_call_output output must be a string for translated targets")
			}
			messages = append(messages, openAIMessage{Role: "tool", ToolCallID: item.CallID, Content: jsonStringValue(output)})
		case item.Type == "reasoning":
		default:
			return nil, refuseTranslation(fmt.Sprintf("input item type %q is not supported for translated targets", item.Type))
		}
	}
	return messages, nil
}

// responsesMessageContent maps input_text, output_text and input_image
// parts to chat content parts.
func responsesMessageContent(raw json.RawMessage) (json.RawMessage, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return jsonStringValue(text), nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return nil, errTranslationUnsupported
	}
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text":
			out = append(out, map[string]any{"type": "text", "text": part.Text})
		case "input_image":
			if part.ImageURL == "" {
				return nil, refuseTranslation("input_image needs an image_url for translated targets")
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]string{"url": part.ImageURL}})
		default:
			return nil, refuseTranslation(fmt.Sprintf("content part %q is not supported for translated targets", part.Type))
		}
	}
	encoded, err := json.Marshal(out)
	return encoded, err
}

// responsesTool maps a function tool; OpenAI-hosted tools are refused.
func responsesTool(raw json.RawMessage) (openAITool, error) {
	var tool struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		Strict      *bool           `json:"strict"`
	}
	if json.Unmarshal(raw, &tool) != nil {
		return openAITool{}, errTranslationUnsupported
	}
	if tool.Type != "function" {
		return openAITool{}, refuseTranslation(fmt.Sprintf("tool type %q is hosted by OpenAI and not supported for translated targets", tool.Type))
	}
	out := openAITool{Type: "function"}
	out.Function.Name, out.Function.Description, out.Function.Parameters, out.Function.Strict =
		tool.Name, tool.Description, tool.Parameters, tool.Strict
	return out, nil
}

// responsesToolChoice maps {"type":"function","name":N} to the chat form;
// string choices are the same in both APIs.
func responsesToolChoice(raw json.RawMessage) json.RawMessage {
	var named struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &named) == nil && named.Type == "function" && named.Name != "" {
		encoded, _ := json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": named.Name}})
		return encoded
	}
	return raw
}

// responsesTextFormat maps text.format to a chat response_format.
func responsesTextFormat(raw json.RawMessage) (json.RawMessage, error) {
	var format struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Schema      json.RawMessage `json:"schema"`
		Strict      *bool           `json:"strict"`
	}
	if json.Unmarshal(raw, &format) != nil {
		return nil, refuseTranslation("text.format must be an object")
	}
	if format.Type != "json_schema" {
		encoded, _ := json.Marshal(map[string]string{"type": format.Type})
		return encoded, nil
	}
	encoded, err := json.Marshal(map[string]any{"type": "json_schema", "json_schema": map[string]any{
		"name": format.Name, "description": format.Description, "schema": format.Schema, "strict": format.Strict}})
	return encoded, err
}

// responsesTranslator turns the chat completions its inner translator
// produces into a Responses object or Responses stream events.
type responsesTranslator struct {
	inner    aiResponseTranslator
	model    string
	created  int64
	id       string
	sequence int
	started  bool
	done     bool
	text     *responsesTextItem
	tools    map[int]*responsesToolItem
	// finished maps output index to the completed item.
	finished   map[int]map[string]any
	usage      *openAIUsage
	finish     string
	nextOutput int
}

type responsesTextItem struct {
	id     string
	index  int
	buffer strings.Builder
}

type responsesToolItem struct {
	id, callID, name string
	index            int
	arguments        strings.Builder
}

func newResponsesTranslator(inner aiResponseTranslator, model string) *responsesTranslator {
	return &responsesTranslator{inner: inner, model: model, created: time.Now().Unix(),
		id: "resp_" + randomCompletionID(), tools: map[int]*responsesToolItem{}, finished: map[int]map[string]any{}}
}

type chatCompletion struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   *string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

func (t *responsesTranslator) json(status int, body []byte) []byte {
	translated := t.inner.json(status, body)
	if status < 200 || status > 299 {
		return translated
	}
	var completion chatCompletion
	if json.Unmarshal(translated, &completion) != nil {
		return openAIErrorBody(http.StatusBadGateway, nil)
	}
	if completion.Model != "" {
		t.model = completion.Model
	}
	t.usage = completion.Usage
	var output []map[string]any
	if len(completion.Choices) > 0 {
		choice := completion.Choices[0]
		t.finish = choice.FinishReason
		if choice.Message.Content != nil && *choice.Message.Content != "" {
			output = append(output, responsesMessageItem("msg_"+randomCompletionID(), *choice.Message.Content, "completed"))
		}
		for _, call := range choice.Message.ToolCalls {
			output = append(output, responsesFunctionCallItem("fc_"+randomCompletionID(), call.ID, call.Function.Name,
				call.Function.Arguments, "completed"))
		}
	}
	encoded, _ := json.Marshal(t.response(output))
	return encoded
}

func responsesMessageItem(id, text, status string) map[string]any {
	return map[string]any{"type": "message", "id": id, "status": status, "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
}

func responsesFunctionCallItem(id, callID, name, arguments, status string) map[string]any {
	return map[string]any{"type": "function_call", "id": id, "call_id": callID, "name": name, "arguments": arguments, "status": status}
}

// response builds the Responses object; status follows the finish reason.
func (t *responsesTranslator) response(output []map[string]any) map[string]any {
	status := "completed"
	var incomplete any
	switch t.finish {
	case "length":
		status, incomplete = "incomplete", map[string]string{"reason": "max_output_tokens"}
	case "content_filter":
		status, incomplete = "incomplete", map[string]string{"reason": "content_filter"}
	}
	if output == nil {
		output = []map[string]any{}
	}
	out := map[string]any{"id": t.id, "object": "response", "created_at": t.created, "status": status, "model": t.model,
		"output": output, "error": nil, "incomplete_details": incomplete, "parallel_tool_calls": true,
		"tool_choice": "auto", "tools": []any{}}
	if t.usage != nil {
		usage := map[string]any{"input_tokens": t.usage.PromptTokens, "output_tokens": t.usage.CompletionTokens,
			"total_tokens": t.usage.TotalTokens, "input_tokens_details": map[string]int64{"cached_tokens": t.usage.PromptTokensDetails.CachedTokens},
			"output_tokens_details": map[string]int64{"reasoning_tokens": 0}}
		out["usage"] = usage
	}
	return out
}

func (t *responsesTranslator) event(data []byte) []byte { return t.convert(t.inner.event(data)) }

func (t *responsesTranslator) end() []byte { return t.convert(t.inner.end()) }

// awsEvent lets the wrapper sit on top of the Bedrock stream translator.
func (t *responsesTranslator) awsEvent(messageType, eventType string, payload []byte) []byte {
	if aws, ok := t.inner.(awsEventTranslator); ok {
		return t.convert(aws.awsEvent(messageType, eventType, payload))
	}
	return nil
}

type chatChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// convert reads the chat SSE its inner translator wrote and emits the
// matching Responses stream events.
func (t *responsesTranslator) convert(chatSSE []byte) []byte {
	var out []byte
	for _, block := range bytes.Split(chatSSE, []byte("\n\n")) {
		data, ok := bytes.CutPrefix(bytes.TrimSpace(block), []byte("data: "))
		if !ok || t.done {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			out = append(out, t.complete()...)
			continue
		}
		var chunk chatChunk
		if json.Unmarshal(data, &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			t.done = true
			out = append(out, t.emit("error", map[string]any{"code": chunk.Error.Type, "message": chunk.Error.Message, "param": nil})...)
			continue
		}
		out = append(out, t.start(chunk.Model)...)
		if chunk.Usage != nil {
			t.usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				out = append(out, t.textDelta(choice.Delta.Content)...)
			}
			for _, call := range choice.Delta.ToolCalls {
				out = append(out, t.toolDelta(call.Index, call.ID, call.Function.Name, call.Function.Arguments)...)
			}
			if choice.FinishReason != nil {
				t.finish = *choice.FinishReason
			}
		}
	}
	return out
}

func (t *responsesTranslator) emit(eventType string, fields map[string]any) []byte {
	fields["type"], fields["sequence_number"] = eventType, t.sequence
	t.sequence++
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil
	}
	return append(append([]byte("event: "+eventType+"\ndata: "), encoded...), '\n', '\n')
}

func (t *responsesTranslator) start(model string) []byte {
	if t.started {
		return nil
	}
	t.started = true
	if model != "" {
		t.model = model
	}
	response := t.response(nil)
	response["status"], response["incomplete_details"] = "in_progress", nil
	delete(response, "usage")
	out := t.emit("response.created", map[string]any{"response": response})
	return append(out, t.emit("response.in_progress", map[string]any{"response": response})...)
}

func (t *responsesTranslator) textDelta(delta string) []byte {
	var out []byte
	if t.text == nil {
		t.text = &responsesTextItem{id: "msg_" + randomCompletionID(), index: t.nextOutput}
		t.nextOutput++
		out = append(out, t.emit("response.output_item.added", map[string]any{"output_index": t.text.index,
			"item": map[string]any{"type": "message", "id": t.text.id, "status": "in_progress", "role": "assistant", "content": []any{}}})...)
		out = append(out, t.emit("response.content_part.added", map[string]any{"item_id": t.text.id, "output_index": t.text.index,
			"content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})...)
	}
	t.text.buffer.WriteString(delta)
	return append(out, t.emit("response.output_text.delta", map[string]any{"item_id": t.text.id, "output_index": t.text.index,
		"content_index": 0, "delta": delta})...)
}

func (t *responsesTranslator) toolDelta(index int, callID, name, arguments string) []byte {
	var out []byte
	item, ok := t.tools[index]
	if !ok {
		out = append(out, t.closeText()...)
		item = &responsesToolItem{id: "fc_" + randomCompletionID(), callID: callID, name: name, index: t.nextOutput}
		t.nextOutput++
		t.tools[index] = item
		out = append(out, t.emit("response.output_item.added", map[string]any{"output_index": item.index,
			"item": responsesFunctionCallItem(item.id, callID, name, "", "in_progress")})...)
	}
	if arguments == "" {
		return out
	}
	item.arguments.WriteString(arguments)
	return append(out, t.emit("response.function_call_arguments.delta", map[string]any{"item_id": item.id,
		"output_index": item.index, "delta": arguments})...)
}

// closeText finishes the open message item.
func (t *responsesTranslator) closeText() []byte {
	if t.text == nil {
		return nil
	}
	text, item := t.text.buffer.String(), t.text
	t.text = nil
	part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
	out := t.emit("response.output_text.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": 0, "text": text})
	out = append(out, t.emit("response.content_part.done", map[string]any{"item_id": item.id, "output_index": item.index,
		"content_index": 0, "part": part})...)
	done := responsesMessageItem(item.id, text, "completed")
	t.finished[item.index] = done
	return append(out, t.emit("response.output_item.done", map[string]any{"output_index": item.index, "item": done})...)
}

// complete closes every open item and emits the final response event.
func (t *responsesTranslator) complete() []byte {
	out := t.start("")
	out = append(out, t.closeText()...)
	toolsByIndex := make(map[int]*responsesToolItem, len(t.tools))
	for _, item := range t.tools {
		toolsByIndex[item.index] = item
	}
	output := make([]map[string]any, 0, t.nextOutput)
	for position := 0; position < t.nextOutput; position++ {
		if item, ok := toolsByIndex[position]; ok {
			arguments := item.arguments.String()
			out = append(out, t.emit("response.function_call_arguments.done", map[string]any{"item_id": item.id,
				"output_index": item.index, "arguments": arguments})...)
			done := responsesFunctionCallItem(item.id, item.callID, item.name, arguments, "completed")
			t.finished[position] = done
			out = append(out, t.emit("response.output_item.done", map[string]any{"output_index": item.index, "item": done})...)
		}
		if done, ok := t.finished[position]; ok {
			output = append(output, done)
		}
	}
	t.done = true
	response := t.response(output)
	eventType := "response.completed"
	if response["status"] == "incomplete" {
		eventType = "response.incomplete"
	}
	return append(out, t.emit(eventType, map[string]any{"response": response})...)
}
