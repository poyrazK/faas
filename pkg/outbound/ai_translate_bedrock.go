package outbound

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Bedrock targets of openai-format routes use the Converse API, which
// serves every Bedrock chat model with one request shape (ADR-995).

type converseBlock = map[string]any

type converseMessage struct {
	Role    string          `json:"role"`
	Content []converseBlock `json:"content"`
}

// translateBedrockChat sends chat completions to /model/{id}/converse, or
// converse-stream for streams.
func translateBedrockChat(request openAIChatRequest, format openAIResponseFormat, model string, includeUsage bool) (routeTranslation, error) {
	translated, err := openAIToConverse(request, format, model)
	if err != nil {
		return routeTranslation{}, err
	}
	operation := "/converse"
	if request.Stream {
		operation = "/converse-stream"
	}
	return routeTranslation{path: "/model/" + url.PathEscape(model) + operation, body: translated,
		translator: newConverseTranslator(request.Stream, includeUsage, model)}, nil
}

// openAIToConverse maps a chat-completions request to a Converse request
// for one Bedrock model.
func openAIToConverse(request openAIChatRequest, format openAIResponseFormat, model string) ([]byte, error) {
	var system []converseBlock
	var messages []converseMessage
	usesTools := false
	appendBlocks := func(role string, blocks []converseBlock) {
		if len(blocks) == 0 {
			return
		}
		if n := len(messages); n > 0 && messages[n-1].Role == role {
			messages[n-1].Content = append(messages[n-1].Content, blocks...)
			return
		}
		messages = append(messages, converseMessage{Role: role, Content: blocks})
	}
	for _, message := range request.Messages {
		switch message.Role {
		case "system", "developer":
			text, err := openAIContentText(message.Content)
			if err != nil {
				return nil, err
			}
			if text != "" {
				system = append(system, converseBlock{"text": text})
			}
		case "user", "assistant":
			blocks, err := converseContentBlocks(message.Content)
			if err != nil {
				return nil, err
			}
			for _, call := range message.ToolCalls {
				usesTools = true
				blocks = append(blocks, converseBlock{"toolUse": map[string]any{"toolUseId": call.ID, "name": call.Function.Name,
					"input": toolArguments(call.Function.Arguments)}})
			}
			appendBlocks(message.Role, blocks)
		case "tool":
			text, err := openAIContentText(message.Content)
			if err != nil {
				return nil, err
			}
			usesTools = true
			appendBlocks("user", []converseBlock{{"toolResult": map[string]any{"toolUseId": message.ToolCallID,
				"content": []converseBlock{{"text": text}}}}})
		default:
			return nil, errTranslationUnsupported
		}
	}
	inference := map[string]any{"maxTokens": openAIOutputCeiling(request)}
	if request.Temperature != nil {
		inference["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		inference["topP"] = *request.TopP
	}
	if stops := openAIStopSequences(request.Stop); len(stops) > 0 {
		inference["stopSequences"] = stops
	}
	out := map[string]any{"messages": messages, "inferenceConfig": inference}
	toolConfig, instruction, err := converseToolConfig(request, usesTools, model)
	if err != nil {
		return nil, err
	}
	if instruction != "" {
		system = append(system, converseBlock{"text": instruction})
	}
	if len(system) > 0 {
		out["system"] = system
	}
	if toolConfig != nil {
		out["toolConfig"] = toolConfig
	}
	switch format.kind {
	case "json_schema":
		jsonSchema := map[string]any{"schema": string(format.schema), "name": format.name}
		if format.description != "" {
			jsonSchema["description"] = format.description
		}
		out["outputConfig"] = map[string]any{"textFormat": map[string]any{"type": "json_schema",
			"structure": map[string]any{"jsonSchema": jsonSchema}}}
	case "json_object":
		return nil, errJSONObjectNeedsSchema("Bedrock")
	}
	return json.Marshal(out)
}

// converseToolConfig maps tools and tool_choice. Converse has no "none"
// choice, so "none" leaves the tools out, which it allows only while the
// conversation has no tool calls. A Claude model that refuses forced tool
// use gets "auto" and a system instruction instead (ADR-996).
func converseToolConfig(request openAIChatRequest, usesTools bool, model string) (map[string]any, string, error) {
	if len(request.Tools) == 0 {
		return nil, "", nil
	}
	mode, name := openAIToolChoice(request.ToolChoice)
	if mode == "none" {
		if usesTools {
			return nil, "", refuseTranslation(`Bedrock targets cannot honor tool_choice "none" in a conversation with tool calls`)
		}
		return nil, "", nil
	}
	instruction := ""
	if (mode == "required" || mode == "function") && claudeRejectsForcedToolChoice(model) {
		instruction, mode = forcedToolInstruction(mode, name), "auto"
	}
	specs := make([]map[string]any, 0, len(request.Tools))
	for _, tool := range request.Tools {
		schema := tool.Function.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		spec := map[string]any{"name": tool.Function.Name, "inputSchema": map[string]any{"json": schema}}
		if tool.Function.Description != "" {
			spec["description"] = tool.Function.Description
		}
		if tool.Function.Strict != nil && *tool.Function.Strict {
			spec["strict"] = true
		}
		specs = append(specs, map[string]any{"toolSpec": spec})
	}
	config := map[string]any{"tools": specs}
	switch mode {
	case "auto":
		config["toolChoice"] = map[string]any{"auto": map[string]any{}}
	case "required":
		config["toolChoice"] = map[string]any{"any": map[string]any{}}
	case "function":
		config["toolChoice"] = map[string]any{"tool": map[string]any{"name": name}}
	}
	return config, instruction, nil
}

// converseImageFormats maps image media types to Converse image formats.
var converseImageFormats = map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif", "image/webp": "webp"}

func converseContentBlocks(raw json.RawMessage) ([]converseBlock, error) {
	parts, err := openAIContentParts(raw)
	if err != nil {
		return nil, err
	}
	blocks := make([]converseBlock, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			if part.Text != "" {
				blocks = append(blocks, converseBlock{"text": part.Text})
			}
		case "image_url":
			if part.ImageURL == nil {
				return nil, errTranslationUnsupported
			}
			media, data, ok := dataURL(part.ImageURL.URL)
			imageFormat := converseImageFormats[media]
			if !ok || imageFormat == "" {
				return nil, refuseTranslation("Bedrock targets accept PNG, JPEG, GIF or WebP data: image URLs only")
			}
			blocks = append(blocks, converseBlock{"image": map[string]any{"format": imageFormat,
				"source": map[string]any{"bytes": data}}})
		default:
			return nil, errTranslationUnsupported
		}
	}
	return blocks, nil
}

type converseUsageFields struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadInputTokens"`
	CacheWriteTokens int64 `json:"cacheWriteInputTokens"`
}

// openAI counts cache reads and writes as prompt tokens, as accounting
// does: Converse inputTokens excludes them.
func (u converseUsageFields) openAI() openAIUsage {
	return newOpenAIUsage(u.InputTokens+u.CacheReadTokens+u.CacheWriteTokens, u.OutputTokens, u.CacheReadTokens)
}

func converseFinish(reason string) string {
	switch reason {
	case "max_tokens", "model_context_window_exceeded":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "guardrail_intervened", "content_filtered":
		return "content_filter"
	}
	return "stop"
}

// converseTranslator turns Converse responses and converse-stream events
// into OpenAI chat completions.
type converseTranslator struct {
	stream    openAIStream
	toolIndex map[int]int
	nextTool  int
	usage     converseUsageFields
}

func newConverseTranslator(stream, includeUsage bool, model string) *converseTranslator {
	return &converseTranslator{stream: openAIStream{id: "chatcmpl-" + randomCompletionID(), model: model,
		created: time.Now().Unix(), includeUsage: includeUsage && stream}, toolIndex: map[int]int{}}
}

// randomCompletionID names a translated completion; Converse responses
// carry no ID of their own.
func randomCompletionID() string {
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

type converseToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

func (t *converseTranslator) json(status int, body []byte) []byte {
	if status < 200 || status > 299 {
		return openAIErrorBody(status, body)
	}
	var response struct {
		Output struct {
			Message struct {
				Content []struct {
					Text    *string          `json:"text"`
					ToolUse *converseToolUse `json:"toolUse"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		StopReason string              `json:"stopReason"`
		Usage      converseUsageFields `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil {
		return openAIErrorBody(http.StatusBadGateway, nil)
	}
	var text strings.Builder
	var calls []map[string]any
	for _, block := range response.Output.Message.Content {
		switch {
		case block.Text != nil:
			text.WriteString(*block.Text)
		case block.ToolUse != nil:
			calls = append(calls, map[string]any{"id": block.ToolUse.ToolUseID, "type": "function",
				"function": map[string]any{"name": block.ToolUse.Name, "arguments": geminiArgs(block.ToolUse.Input)}})
		}
	}
	return openAICompletion(t.stream.id, t.stream.model, text.String(), calls, converseFinish(response.StopReason), response.Usage.openAI())
}

// event is unused: converse-stream is an AWS event stream, read by awsEvent.
func (t *converseTranslator) event([]byte) []byte { return nil }

// awsEvent converts one converse-stream event. An exception ends the
// stream with an OpenAI error event.
func (t *converseTranslator) awsEvent(messageType, eventType string, payload []byte) []byte {
	if messageType == "exception" || messageType == "error" {
		if t.stream.done {
			return nil
		}
		t.stream.done = true
		var exception struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload, &exception)
		if exception.Message == "" {
			exception.Message = "Bedrock stream failed"
		}
		return append(sseData(map[string]any{"error": map[string]any{"message": exception.Message, "type": eventType}}), "data: [DONE]\n\n"...)
	}
	var event struct {
		ContentBlockIndex int `json:"contentBlockIndex"`
		Start             *struct {
			ToolUse *converseToolUse `json:"toolUse"`
		} `json:"start"`
		Delta *struct {
			Text    *string `json:"text"`
			ToolUse *struct {
				Input string `json:"input"`
			} `json:"toolUse"`
		} `json:"delta"`
		StopReason string               `json:"stopReason"`
		Usage      *converseUsageFields `json:"usage"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return nil
	}
	switch eventType {
	case "messageStart":
		return t.stream.chunk(map[string]any{"role": "assistant", "content": ""}, nil)
	case "contentBlockStart":
		if event.Start != nil && event.Start.ToolUse != nil {
			index := t.nextTool
			t.nextTool++
			t.toolIndex[event.ContentBlockIndex] = index
			return t.stream.toolCall(index, map[string]any{"id": event.Start.ToolUse.ToolUseID, "type": "function",
				"function": map[string]any{"name": event.Start.ToolUse.Name, "arguments": ""}})
		}
	case "contentBlockDelta":
		if event.Delta == nil {
			return nil
		}
		if event.Delta.Text != nil {
			return t.stream.content(*event.Delta.Text)
		}
		if event.Delta.ToolUse != nil && event.Delta.ToolUse.Input != "" {
			if index, ok := t.toolIndex[event.ContentBlockIndex]; ok {
				return t.stream.toolCall(index, map[string]any{"function": map[string]any{"arguments": event.Delta.ToolUse.Input}})
			}
		}
	case "messageStop":
		t.stream.finish = converseFinish(event.StopReason)
	case "metadata":
		if event.Usage != nil {
			t.usage = *event.Usage
		}
	}
	return nil
}

func (t *converseTranslator) end() []byte { return t.stream.close(t.usage.openAI()) }
