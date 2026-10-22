package outbound

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// aiResponseTranslator turns one provider response into OpenAI chat
// completions (ADR-991).
type aiResponseTranslator interface {
	// json converts a whole JSON body, a success or a provider error.
	json(status int, body []byte) []byte
	// event converts one SSE data payload into OpenAI SSE bytes.
	event(data []byte) []byte
	// end finishes a stream: final chunk, optional usage chunk and [DONE].
	end() []byte
}

// awsEventTranslator converts the messages of a binary AWS event stream
// (Bedrock converse-stream, ADR-995) into OpenAI SSE bytes.
type awsEventTranslator interface {
	awsEvent(messageType, eventType string, payload []byte) []byte
}

type openAIUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func newOpenAIUsage(prompt, completion, cached int64) openAIUsage {
	usage := openAIUsage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion}
	usage.PromptTokensDetails.CachedTokens = cached
	return usage
}

// openAIStream builds chat.completion.chunk events for one response.
type openAIStream struct {
	id, model    string
	created      int64
	includeUsage bool
	finish       string
	sawToolCall  bool
	done         bool
}

func (s *openAIStream) chunk(delta map[string]any, finish *string) []byte {
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": finish}
	return sseData(map[string]any{
		"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model,
		"choices": []any{choice},
	})
}

func (s *openAIStream) content(text string) []byte {
	if text == "" {
		return nil
	}
	return s.chunk(map[string]any{"content": text}, nil)
}

func (s *openAIStream) toolCall(index int, call map[string]any) []byte {
	s.sawToolCall = true
	call["index"] = index
	return s.chunk(map[string]any{"tool_calls": []any{call}}, nil)
}

// close emits the final chunk, the usage chunk when the caller asked for
// one, and [DONE].
func (s *openAIStream) close(usage openAIUsage) []byte {
	if s.done {
		return nil
	}
	s.done = true
	finish := s.finish
	if finish == "" {
		finish = "stop"
	}
	if s.sawToolCall && finish == "stop" {
		finish = "tool_calls"
	}
	out := s.chunk(map[string]any{}, &finish)
	if s.includeUsage {
		out = append(out, sseData(map[string]any{
			"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model,
			"choices": []any{}, "usage": usage,
		})...)
	}
	return append(out, "data: [DONE]\n\n"...)
}

func sseData(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return append(append([]byte("data: "), encoded...), '\n', '\n')
}

// openAIErrorBody wraps a provider error in the OpenAI error envelope.
func openAIErrorBody(status int, body []byte) []byte {
	var provider struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	// Bedrock errors carry a top-level message instead (ADR-995).
	var topLevel struct {
		Message string `json:"message"`
	}
	message := http.StatusText(status)
	errorType := "provider_error"
	if json.Unmarshal(body, &provider) == nil && provider.Error != nil && provider.Error.Message != "" {
		message = provider.Error.Message
		if provider.Error.Type != "" {
			errorType = provider.Error.Type
		} else if provider.Error.Status != "" {
			errorType = strings.ToLower(provider.Error.Status)
		}
	} else if json.Unmarshal(body, &topLevel) == nil && topLevel.Message != "" {
		message = topLevel.Message
	}
	encoded, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": message, "type": errorType, "code": strconv.Itoa(status),
	}})
	return encoded
}

func openAICompletion(id, model, content string, toolCalls []map[string]any, finish string, usage openAIUsage) []byte {
	message := map[string]any{"role": "assistant", "content": nil}
	if content != "" {
		message["content"] = content
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	encoded, _ := json.Marshal(map[string]any{
		"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		"usage":   usage,
	})
	return encoded
}

type anthropicUsageFields struct {
	InputTokens   int64 `json:"input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
}

func (u anthropicUsageFields) openAI() openAIUsage {
	return newOpenAIUsage(u.InputTokens+u.CacheRead+u.CacheCreation, u.OutputTokens, u.CacheRead)
}

func anthropicFinish(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	}
	return "stop"
}

type anthropicTranslator struct {
	stream    openAIStream
	toolIndex map[int]int
	nextTool  int
	usage     anthropicUsageFields
}

func newAnthropicTranslator(stream, includeUsage bool) *anthropicTranslator {
	return &anthropicTranslator{stream: openAIStream{created: time.Now().Unix(), includeUsage: includeUsage && stream}, toolIndex: map[int]int{}}
}

func (t *anthropicTranslator) json(status int, body []byte) []byte {
	if status < 200 || status > 299 {
		return openAIErrorBody(status, body)
	}
	var response struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string               `json:"stop_reason"`
		Usage      anthropicUsageFields `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil {
		return openAIErrorBody(http.StatusBadGateway, nil)
	}
	var text strings.Builder
	var calls []map[string]any
	for _, block := range response.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			arguments := string(block.Input)
			if arguments == "" {
				arguments = "{}"
			}
			calls = append(calls, map[string]any{"id": block.ID, "type": "function",
				"function": map[string]any{"name": block.Name, "arguments": arguments}})
		}
	}
	return openAICompletion("chatcmpl-"+response.ID, response.Model, text.String(), calls, anthropicFinish(response.StopReason), response.Usage.openAI())
}

func (t *anthropicTranslator) event(data []byte) []byte {
	var event struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message *struct {
			ID    string               `json:"id"`
			Model string               `json:"model"`
			Usage anthropicUsageFields `json:"usage"`
		} `json:"message"`
		ContentBlock *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
			Text string `json:"text"`
		} `json:"content_block"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Usage *anthropicUsageFields `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &event) != nil {
		return nil
	}
	switch event.Type {
	case "message_start":
		if event.Message != nil {
			t.stream.id, t.stream.model, t.usage = "chatcmpl-"+event.Message.ID, event.Message.Model, event.Message.Usage
		}
		return t.stream.chunk(map[string]any{"role": "assistant", "content": ""}, nil)
	case "content_block_start":
		if event.ContentBlock == nil {
			return nil
		}
		if event.ContentBlock.Type == "tool_use" {
			index := t.nextTool
			t.nextTool++
			t.toolIndex[event.Index] = index
			return t.stream.toolCall(index, map[string]any{"id": event.ContentBlock.ID, "type": "function",
				"function": map[string]any{"name": event.ContentBlock.Name, "arguments": ""}})
		}
		if event.ContentBlock.Type == "text" {
			return t.stream.content(event.ContentBlock.Text)
		}
	case "content_block_delta":
		if event.Delta == nil {
			return nil
		}
		switch event.Delta.Type {
		case "text_delta":
			return t.stream.content(event.Delta.Text)
		case "input_json_delta":
			if index, ok := t.toolIndex[event.Index]; ok && event.Delta.PartialJSON != "" {
				return t.stream.toolCall(index, map[string]any{"function": map[string]any{"arguments": event.Delta.PartialJSON}})
			}
		}
	case "message_delta":
		if event.Delta != nil && event.Delta.StopReason != "" {
			t.stream.finish = anthropicFinish(event.Delta.StopReason)
		}
		if event.Usage != nil {
			t.usage.OutputTokens = max(t.usage.OutputTokens, event.Usage.OutputTokens)
			t.usage.InputTokens = max(t.usage.InputTokens, event.Usage.InputTokens)
			t.usage.CacheRead = max(t.usage.CacheRead, event.Usage.CacheRead)
			t.usage.CacheCreation = max(t.usage.CacheCreation, event.Usage.CacheCreation)
		}
	case "message_stop":
		return t.stream.close(t.usage.openAI())
	case "error":
		if event.Error != nil && !t.stream.done {
			t.stream.done = true
			return append(sseData(map[string]any{"error": map[string]any{"message": event.Error.Message, "type": event.Error.Type}}), "data: [DONE]\n\n"...)
		}
	}
	return nil
}

func (t *anthropicTranslator) end() []byte { return t.stream.close(t.usage.openAI()) }

type geminiUsageFields struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
}

func (u geminiUsageFields) openAI() openAIUsage {
	return newOpenAIUsage(u.PromptTokenCount, u.CandidatesTokenCount+u.ThoughtsTokenCount, u.CachedContentTokenCount)
}

type geminiResponse struct {
	ResponseID   string `json:"responseId"`
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content struct {
			Parts []struct {
				Text         string `json:"text"`
				Thought      bool   `json:"thought"`
				FunctionCall *struct {
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *geminiUsageFields `json:"usageMetadata"`
}

func geminiFinish(reason string) string {
	switch reason {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		return "content_filter"
	}
	return "stop"
}

type geminiTranslator struct {
	stream   openAIStream
	started  bool
	nextTool int
	usage    geminiUsageFields
}

func newGeminiTranslator(stream, includeUsage bool, model string) *geminiTranslator {
	return &geminiTranslator{stream: openAIStream{created: time.Now().Unix(), includeUsage: includeUsage && stream, model: model}}
}

func (t *geminiTranslator) json(status int, body []byte) []byte {
	if status < 200 || status > 299 {
		return openAIErrorBody(status, body)
	}
	var response geminiResponse
	if json.Unmarshal(body, &response) != nil {
		return openAIErrorBody(http.StatusBadGateway, nil)
	}
	var text strings.Builder
	var calls []map[string]any
	finish := "stop"
	if len(response.Candidates) > 0 {
		candidate := response.Candidates[0]
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil {
				calls = append(calls, map[string]any{"id": "call_" + strconv.Itoa(len(calls)), "type": "function",
					"function": map[string]any{"name": part.FunctionCall.Name, "arguments": geminiArgs(part.FunctionCall.Args)}})
			} else if !part.Thought {
				text.WriteString(part.Text)
			}
		}
		finish = geminiFinish(candidate.FinishReason)
	}
	if len(calls) > 0 && finish == "stop" {
		finish = "tool_calls"
	}
	var usage geminiUsageFields
	if response.UsageMetadata != nil {
		usage = *response.UsageMetadata
	}
	model := response.ModelVersion
	if model == "" {
		model = t.stream.model
	}
	return openAICompletion("chatcmpl-"+response.ResponseID, model, text.String(), calls, finish, usage.openAI())
}

func geminiArgs(args json.RawMessage) string {
	if len(args) == 0 || string(args) == "null" {
		return "{}"
	}
	return string(args)
}

func (t *geminiTranslator) event(data []byte) []byte {
	var chunk geminiResponse
	if json.Unmarshal(data, &chunk) != nil {
		return nil
	}
	var out []byte
	if !t.started {
		t.started = true
		t.stream.id = "chatcmpl-" + chunk.ResponseID
		if chunk.ModelVersion != "" {
			t.stream.model = chunk.ModelVersion
		}
		out = append(out, t.stream.chunk(map[string]any{"role": "assistant", "content": ""}, nil)...)
	}
	if chunk.UsageMetadata != nil {
		t.usage = *chunk.UsageMetadata
	}
	if len(chunk.Candidates) == 0 {
		return out
	}
	candidate := chunk.Candidates[0]
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			index := t.nextTool
			t.nextTool++
			out = append(out, t.stream.toolCall(index, map[string]any{"id": "call_" + strconv.Itoa(index), "type": "function",
				"function": map[string]any{"name": part.FunctionCall.Name, "arguments": geminiArgs(part.FunctionCall.Args)}})...)
		} else if !part.Thought {
			out = append(out, t.stream.content(part.Text)...)
		}
	}
	if candidate.FinishReason != "" {
		t.stream.finish = geminiFinish(candidate.FinishReason)
	}
	return out
}

func (t *geminiTranslator) end() []byte { return t.stream.close(t.usage.openAI()) }

type translateMode int

const (
	translatePassthrough translateMode = iota
	translateJSON
	translateEventStream
	// translateAWSEventStream reads Bedrock's binary event stream.
	translateAWSEventStream
)

// translatingWriter rewrites a target's provider response into OpenAI chat
// completions on its way to the route attempt writer. Gateway problem
// responses pass through unchanged.
type translatingWriter struct {
	dst        http.ResponseWriter
	header     http.Header
	translator aiResponseTranslator
	mode       translateMode
	status     int
	body       bytes.Buffer
	line       []byte
	data       []byte
	frames     awsEventStreamReader
	wrote      bool
	// aborted marks a stream a response check ended (ADR-1435).
	aborted bool
}

func newTranslatingWriter(dst http.ResponseWriter, translator aiResponseTranslator) *translatingWriter {
	return &translatingWriter{dst: dst, header: make(http.Header), translator: translator}
}

func (w *translatingWriter) Header() http.Header { return w.header }

func (w *translatingWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote, w.status = true, status
	mediaType, _, _ := mime.ParseMediaType(w.header.Get("Content-Type"))
	awsTranslator, readsAWS := w.translator.(awsEventTranslator)
	switch {
	case mediaType == "application/problem+json":
		w.mode = translatePassthrough
	case mediaType == "text/event-stream" && status >= 200 && status <= 299:
		w.mode = translateEventStream
	case mediaType == awsEventStreamMediaType && readsAWS && status >= 200 && status <= 299:
		w.mode = translateAWSEventStream
		w.frames.onMessage = func(messageType, eventType string, payload []byte) {
			if w.aborted {
				return
			}
			if code, message := gatewayErrorCode(payload); messageType == "exception" && code != "" {
				_, _ = w.dst.Write(openAIGatewayErrorEvent(code, message))
				w.aborted = true
				return
			}
			_, _ = w.dst.Write(awsTranslator.awsEvent(messageType, eventType, payload))
		}
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		w.mode = translateJSON
	default:
		w.mode = translatePassthrough
	}
	for name, values := range w.header {
		if w.mode != translatePassthrough && (name == "Content-Length" || name == "Content-Type" || name == "Content-Encoding") {
			continue
		}
		w.dst.Header()[name] = values
	}
	switch w.mode {
	case translateEventStream, translateAWSEventStream:
		w.dst.Header().Set("Content-Type", "text/event-stream")
	case translateJSON:
		w.dst.Header().Set("Content-Type", "application/json")
	}
	w.dst.WriteHeader(status)
}

func (w *translatingWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	switch w.mode {
	case translateJSON:
		return w.body.Write(p)
	case translateEventStream:
		w.writeEvents(p)
		return len(p), nil
	case translateAWSEventStream:
		w.frames.write(p)
		return len(p), nil
	}
	return w.dst.Write(p)
}

// writeEvents splits provider SSE into data payloads and writes the
// translated events.
func (w *translatingWriter) writeEvents(p []byte) {
	for len(p) > 0 {
		newline := bytes.IndexByte(p, '\n')
		if newline < 0 {
			w.line = append(w.line, p...)
			return
		}
		w.line = append(w.line, p[:newline]...)
		p = p[newline+1:]
		line := bytes.TrimSuffix(w.line, []byte{'\r'})
		if len(line) == 0 {
			if len(w.data) > 0 && !bytes.Equal(w.data, []byte("[DONE]")) {
				w.dispatch(w.data)
			}
			w.data = w.data[:0]
		} else if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			if len(w.data) > 0 {
				w.data = append(w.data, '\n')
			}
			w.data = append(w.data, bytes.TrimPrefix(value, []byte{' '})...)
		}
		w.line = w.line[:0]
	}
}

// dispatch translates one provider event. A response check's error event
// ends the translated stream with an OpenAI error event (ADR-1435).
func (w *translatingWriter) dispatch(data []byte) {
	if w.aborted {
		return
	}
	if code, message := gatewayErrorCode(data); code != "" {
		_, _ = w.dst.Write(openAIGatewayErrorEvent(code, message))
		w.aborted = true
		return
	}
	_, _ = w.dst.Write(w.translator.event(data))
}

// Flush passes streamed output through promptly.
func (w *translatingWriter) Flush() {
	if flusher, ok := w.dst.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the caller's writer for write
// deadlines.
func (w *translatingWriter) Unwrap() http.ResponseWriter { return w.dst }

// finish writes the translated JSON body, or ends the translated stream.
func (w *translatingWriter) finish() {
	switch w.mode {
	case translateJSON:
		_, _ = w.dst.Write(w.translator.json(w.status, w.body.Bytes()))
	case translateEventStream:
		if len(w.data) > 0 && !bytes.Equal(w.data, []byte("[DONE]")) {
			w.dispatch(w.data)
			w.data = w.data[:0]
		}
		if !w.aborted {
			_, _ = w.dst.Write(w.translator.end())
		}
		w.Flush()
	case translateAWSEventStream:
		if !w.aborted {
			_, _ = w.dst.Write(w.translator.end())
		}
		w.Flush()
	}
}
