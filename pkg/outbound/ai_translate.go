package outbound

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// errTranslationUnsupported marks an OpenAI request the translation layer
// cannot express for the target provider (ADR-991).
var errTranslationUnsupported = errors.New("outbound AI translation: unsupported request")

// translationRefusal is an unsupported request with a reason the caller
// sees in the problem detail (ADR-995).
type translationRefusal struct{ reason string }

func (e translationRefusal) Error() string        { return "outbound AI translation: " + e.reason }
func (e translationRefusal) Is(target error) bool { return target == errTranslationUnsupported }

func refuseTranslation(reason string) error { return translationRefusal{reason: reason} }

// translationRefusalDetail is the caller-facing reason of a translation
// error, or a generic one.
func translationRefusalDetail(err error) string {
	var refusal translationRefusal
	if errors.As(err, &refusal) {
		return refusal.reason
	}
	return "The request cannot be translated for this route's targets"
}

// openAIChatRequest is the OpenAI chat-completions subset routes translate.
type openAIChatRequest struct {
	Model               string             `json:"model"`
	Messages            []openAIMessage    `json:"messages"`
	Tools               []openAITool       `json:"tools"`
	ToolChoice          json.RawMessage    `json:"tool_choice"`
	ParallelToolCalls   *bool              `json:"parallel_tool_calls"`
	MaxTokens           *int64             `json:"max_tokens"`
	MaxCompletionTokens *int64             `json:"max_completion_tokens"`
	Temperature         *float64           `json:"temperature"`
	TopP                *float64           `json:"top_p"`
	Stop                json.RawMessage    `json:"stop"`
	Stream              bool               `json:"stream"`
	StreamOptions       *openAIStreamUsage `json:"stream_options"`
	N                   *int               `json:"n"`
	User                string             `json:"user"`
	ResponseFormat      json.RawMessage    `json:"response_format"`
}

// openAIResponseFormat is a chat request's response_format (ADR-995):
// kind is "" or "text" for plain text, "json_object" or "json_schema".
type openAIResponseFormat struct {
	kind, name, description string
	schema                  json.RawMessage
}

func parseOpenAIResponseFormat(raw json.RawMessage) (openAIResponseFormat, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return openAIResponseFormat{}, nil
	}
	var format struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Schema      json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	if json.Unmarshal(raw, &format) != nil {
		return openAIResponseFormat{}, refuseTranslation("response_format must be an object")
	}
	switch format.Type {
	case "text", "json_object":
		return openAIResponseFormat{kind: format.Type}, nil
	case "json_schema":
		if format.JSONSchema == nil || !isJSONObject(format.JSONSchema.Schema) {
			return openAIResponseFormat{}, refuseTranslation("response_format json_schema needs json_schema.schema as an object")
		}
		name := format.JSONSchema.Name
		if name == "" {
			name = "response"
		}
		return openAIResponseFormat{kind: format.Type, name: name, description: format.JSONSchema.Description,
			schema: format.JSONSchema.Schema}, nil
	}
	return openAIResponseFormat{}, refuseTranslation(fmt.Sprintf("response_format type %q is not supported", format.Type))
}

func isJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

// errJSONObjectNeedsSchema refuses json_object for providers whose
// structured output always needs a schema.
func errJSONObjectNeedsSchema(provider string) error {
	return refuseTranslation(provider + " targets need response_format json_schema; json_object has no schema to enforce")
}

type openAIStreamUsage struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls"`
	ToolCallID string           `json:"tool_call_id"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
		// Strict asks for schema-valid arguments; Anthropic and Bedrock
		// honor it per tool (ADR-996).
		Strict *bool `json:"strict,omitempty"`
	} `json:"function"`
}

// claudeModelsWithoutForcedToolChoice are the Claude models that refuse
// tool_choice "any" and "tool" with a 400 (ADR-996). Older and other Claude
// models accept forced tool use.
var claudeModelsWithoutForcedToolChoice = []string{"claude-fable-5-1", "claude-mythos-5-1", "claude-opus-5-5", "claude-sonnet-5-5"}

// claudeRejectsForcedToolChoice reports whether a target model, as an
// Anthropic, Vertex or Bedrock ID (with an "anthropic." prefix, a regional
// inference-profile prefix, an ARN, a version suffix or an @date), is one of
// claudeModelsWithoutForcedToolChoice.
func claudeRejectsForcedToolChoice(model string) bool {
	name := strings.ToLower(model)
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	if index := strings.Index(name, "anthropic."); index >= 0 {
		name = name[index+len("anthropic."):]
	}
	for _, family := range claudeModelsWithoutForcedToolChoice {
		if name == family {
			return true
		}
		if rest, ok := strings.CutPrefix(name, family); ok && (rest[0] == '-' || rest[0] == '@' || rest[0] == ':') {
			return true
		}
	}
	return false
}

// forcedToolInstruction is the system-prompt sentence that stands in for a
// forced tool choice on models that refuse one; it steers, it does not
// guarantee a call.
func forcedToolInstruction(mode, name string) string {
	if mode == "function" {
		return fmt.Sprintf("Answer by calling the %s tool.", name)
	}
	return "Answer by calling one of the provided tools."
}

type openAIPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// routeTranslation is one target's provider-native request and the
// translator for its response; translator is nil when no response
// translation is needed (openai and azure_openai targets).
type routeTranslation struct {
	path, rawQuery string
	body           []byte
	translator     aiResponseTranslator
}

// translateRouteRequest rewrites an openai-format route's request for one
// target: chat completions (ADR-991), embeddings (ADR-995) or Responses API
// requests (ADR-996). providerPath and the returned path are escaped paths.
func translateRouteRequest(integration Integration, providerPath, rawQuery string, body []byte, model string) (routeTranslation, error) {
	switch {
	case strings.HasSuffix(providerPath, "/embeddings"):
		return translateEmbeddingsRequest(integration, providerPath, rawQuery, body, model)
	case strings.HasSuffix(providerPath, "/responses"):
		return translateResponsesRequest(integration, providerPath, rawQuery, body, model)
	}
	provider := integration.AIProvider
	switch provider {
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
		return routeTranslation{path: "/openai/v1/chat/completions", body: rewritten}, nil
	}
	var request openAIChatRequest
	if json.Unmarshal(body, &request) != nil {
		return routeTranslation{}, errTranslationUnsupported
	}
	return translateChatRequest(integration, request, model)
}

// translateChatRequest translates a parsed chat-completions request for a
// non-OpenAI target.
func translateChatRequest(integration Integration, request openAIChatRequest, model string) (routeTranslation, error) {
	provider := integration.AIProvider
	if len(request.Messages) == 0 || model == "" {
		return routeTranslation{}, errTranslationUnsupported
	}
	if request.N != nil && *request.N > 1 {
		return routeTranslation{}, refuseTranslation("n above 1 is not supported for translated targets")
	}
	format, err := parseOpenAIResponseFormat(request.ResponseFormat)
	if err != nil {
		return routeTranslation{}, err
	}
	includeUsage := request.StreamOptions != nil && request.StreamOptions.IncludeUsage
	switch provider {
	case api.OutboundAIProviderAnthropic:
		fields, err := openAIToAnthropic(request, model, format)
		if err != nil {
			return routeTranslation{}, err
		}
		translated, err := json.Marshal(fields)
		if err != nil {
			return routeTranslation{}, errTranslationUnsupported
		}
		return routeTranslation{path: "/v1/messages", body: translated,
			translator: newAnthropicTranslator(request.Stream, includeUsage)}, nil
	case api.OutboundAIProviderGemini:
		translated, err := openAIToGemini(request, format)
		if err != nil {
			return routeTranslation{}, err
		}
		verb, query := ":generateContent", ""
		if request.Stream {
			verb, query = ":streamGenerateContent", "alt=sse"
		}
		return routeTranslation{path: "/v1beta/models/" + url.PathEscape(model) + verb, rawQuery: query, body: translated,
			translator: newGeminiTranslator(request.Stream, includeUsage, model)}, nil
	case api.OutboundAIProviderVertex:
		return translateVertexChat(integration, request, format, model, includeUsage)
	case api.OutboundAIProviderBedrock:
		return translateBedrockChat(request, format, model, includeUsage)
	}
	return routeTranslation{}, errTranslationUnsupported
}

// vertexPublisherModel splits a Vertex target model into its publisher and
// model: an "anthropic/" or "google/" prefix names the publisher, and
// otherwise a "claude" model is Anthropic's and anything else Google's.
func vertexPublisherModel(model string) (publisher, name string) {
	if rest, ok := strings.CutPrefix(model, "anthropic/"); ok {
		return "anthropic", rest
	}
	if rest, ok := strings.CutPrefix(model, "google/"); ok {
		return "google", rest
	}
	if strings.HasPrefix(model, "claude") {
		return "anthropic", model
	}
	return "google", model
}

// translateVertexChat sends chat completions to Claude (rawPredict) or
// Gemini (generateContent) on Vertex AI under the integration's one
// project and location (ADR-995).
func translateVertexChat(integration Integration, request openAIChatRequest, format openAIResponseFormat, model string,
	includeUsage bool) (routeTranslation, error) {
	base, ok := api.OutboundVertexBasePath(integration.AllowedPathPrefixes)
	if !ok {
		return routeTranslation{}, refuseTranslation("the Vertex integration's allowed path prefixes must name one /v1/projects/{project}/locations/{location}")
	}
	publisher, name := vertexPublisherModel(model)
	if name == "" {
		return routeTranslation{}, errTranslationUnsupported
	}
	modelPath := base + "/publishers/" + publisher + "/models/" + url.PathEscape(name)
	if publisher == "anthropic" {
		fields, err := openAIToAnthropic(request, name, format)
		if err != nil {
			return routeTranslation{}, err
		}
		// Vertex names the model in the path and the API version in the body.
		delete(fields, "model")
		fields["anthropic_version"] = api.OutboundVertexAnthropicVersion
		translated, err := json.Marshal(fields)
		if err != nil {
			return routeTranslation{}, errTranslationUnsupported
		}
		verb := ":rawPredict"
		if request.Stream {
			verb = ":streamRawPredict"
		}
		return routeTranslation{path: modelPath + verb, body: translated, translator: newAnthropicTranslator(request.Stream, includeUsage)}, nil
	}
	translated, err := openAIToGemini(request, format)
	if err != nil {
		return routeTranslation{}, err
	}
	verb, query := ":generateContent", ""
	if request.Stream {
		verb, query = ":streamGenerateContent", "alt=sse"
	}
	return routeTranslation{path: modelPath + verb, rawQuery: query, body: translated,
		translator: newGeminiTranslator(request.Stream, includeUsage, name)}, nil
}

// openAIContentParts reads a message content: a string, an array of parts
// or null.
func openAIContentParts(raw json.RawMessage) ([]openAIPart, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []openAIPart{{Type: "text", Text: text}}, nil
	}
	var parts []openAIPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, errTranslationUnsupported
	}
	return parts, nil
}

func openAIContentText(raw json.RawMessage) (string, error) {
	parts, err := openAIContentParts(raw)
	if err != nil {
		return "", err
	}
	var texts []string
	for _, part := range parts {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func openAIStopSequences(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

func openAIOutputCeiling(request openAIChatRequest) int64 {
	if request.MaxCompletionTokens != nil && *request.MaxCompletionTokens > 0 {
		return *request.MaxCompletionTokens
	}
	if request.MaxTokens != nil && *request.MaxTokens > 0 {
		return *request.MaxTokens
	}
	return api.OutboundAIDefaultOutputReserveTokens
}

// openAIToolChoice reads tool_choice as "auto", "none", "required" or a
// named function.
func openAIToolChoice(raw json.RawMessage) (mode, name string) {
	var simple string
	if json.Unmarshal(raw, &simple) == nil {
		return simple, ""
	}
	var named struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &named) == nil && named.Function.Name != "" {
		return "function", named.Function.Name
	}
	return "", ""
}

// dataURL splits "data:<media>;base64,<data>".
func dataURL(value string) (mediaType, data string, ok bool) {
	rest, found := strings.CutPrefix(value, "data:")
	if !found {
		return "", "", false
	}
	meta, payload, found := strings.Cut(rest, ",")
	media, encoding, _ := strings.Cut(meta, ";")
	if !found || encoding != "base64" || media == "" {
		return "", "", false
	}
	return media, payload, true
}

// toolArguments parses a tool call's JSON arguments; a call with invalid
// arguments is sent as an empty object.
func toolArguments(arguments string) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &object) != nil || object == nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(arguments)
}

type anthropicBlock = map[string]any

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

// openAIToAnthropic maps a chat-completions request to Messages API fields.
func openAIToAnthropic(request openAIChatRequest, model string, format openAIResponseFormat) (map[string]any, error) {
	var system []string
	var messages []anthropicMessage
	appendBlocks := func(role string, blocks []anthropicBlock) {
		if len(blocks) == 0 {
			return
		}
		if n := len(messages); n > 0 && messages[n-1].Role == role {
			messages[n-1].Content = append(messages[n-1].Content, blocks...)
			return
		}
		messages = append(messages, anthropicMessage{Role: role, Content: blocks})
	}
	for _, message := range request.Messages {
		switch message.Role {
		case "system", "developer":
			text, err := openAIContentText(message.Content)
			if err != nil {
				return nil, err
			}
			system = append(system, text)
		case "user", "assistant":
			blocks, err := anthropicContentBlocks(message.Content)
			if err != nil {
				return nil, err
			}
			for _, call := range message.ToolCalls {
				blocks = append(blocks, anthropicBlock{"type": "tool_use", "id": call.ID, "name": call.Function.Name,
					"input": toolArguments(call.Function.Arguments)})
			}
			appendBlocks(message.Role, blocks)
		case "tool":
			text, err := openAIContentText(message.Content)
			if err != nil {
				return nil, err
			}
			appendBlocks("user", []anthropicBlock{{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": text}})
		default:
			return nil, errTranslationUnsupported
		}
	}
	out := map[string]any{"model": model, "messages": messages, "max_tokens": openAIOutputCeiling(request)}
	choice, instruction := anthropicToolChoice(request, model)
	if instruction != "" {
		system = append(system, instruction)
	}
	if len(system) > 0 {
		out["system"] = strings.Join(system, "\n\n")
	}
	if request.Temperature != nil {
		out["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		out["top_p"] = *request.TopP
	}
	if stops := openAIStopSequences(request.Stop); len(stops) > 0 {
		out["stop_sequences"] = stops
	}
	if request.Stream {
		out["stream"] = true
	}
	if request.User != "" {
		out["metadata"] = map[string]string{"user_id": request.User}
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			schema := tool.Function.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			entry := map[string]any{"name": tool.Function.Name, "input_schema": schema}
			if tool.Function.Description != "" {
				entry["description"] = tool.Function.Description
			}
			if tool.Function.Strict != nil && *tool.Function.Strict {
				entry["strict"] = true
			}
			tools = append(tools, entry)
		}
		out["tools"] = tools
	}
	if choice != nil {
		out["tool_choice"] = choice
	}
	// ADR-995: structured output always takes a schema.
	switch format.kind {
	case "json_schema":
		out["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": format.schema}}
	case "json_object":
		return nil, errJSONObjectNeedsSchema("Anthropic")
	}
	return out, nil
}

func anthropicContentBlocks(raw json.RawMessage) ([]anthropicBlock, error) {
	parts, err := openAIContentParts(raw)
	if err != nil {
		return nil, err
	}
	blocks := make([]anthropicBlock, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			if part.Text != "" {
				blocks = append(blocks, anthropicBlock{"type": "text", "text": part.Text})
			}
		case "image_url":
			if part.ImageURL == nil {
				return nil, errTranslationUnsupported
			}
			if media, data, ok := dataURL(part.ImageURL.URL); ok {
				blocks = append(blocks, anthropicBlock{"type": "image", "source": map[string]string{"type": "base64", "media_type": media, "data": data}})
			} else if strings.HasPrefix(part.ImageURL.URL, "https://") {
				blocks = append(blocks, anthropicBlock{"type": "image", "source": map[string]string{"type": "url", "url": part.ImageURL.URL}})
			} else {
				return nil, errTranslationUnsupported
			}
		default:
			return nil, errTranslationUnsupported
		}
	}
	return blocks, nil
}

// anthropicToolChoice maps tool_choice. For a model that refuses forced
// tool use (ADR-996) a forced choice becomes "auto" plus an instruction for
// the system prompt.
func anthropicToolChoice(request openAIChatRequest, model string) (map[string]any, string) {
	mode, name := openAIToolChoice(request.ToolChoice)
	instruction := ""
	if (mode == "required" || mode == "function") && claudeRejectsForcedToolChoice(model) {
		instruction, mode = forcedToolInstruction(mode, name), "auto"
	}
	var choice map[string]any
	switch mode {
	case "auto":
		choice = map[string]any{"type": "auto"}
	case "none":
		choice = map[string]any{"type": "none"}
	case "required":
		choice = map[string]any{"type": "any"}
	case "function":
		choice = map[string]any{"type": "tool", "name": name}
	}
	if request.ParallelToolCalls != nil && !*request.ParallelToolCalls && len(request.Tools) > 0 {
		if choice == nil {
			choice = map[string]any{"type": "auto"}
		}
		if choice["type"] != "none" {
			choice["disable_parallel_tool_use"] = true
		}
	}
	return choice, instruction
}

type geminiPart = map[string]any

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

// openAIToGemini maps a chat-completions request to generateContent.
func openAIToGemini(request openAIChatRequest, format openAIResponseFormat) ([]byte, error) {
	var system []string
	var contents []geminiContent
	toolNames := map[string]string{}
	appendParts := func(role string, parts []geminiPart) {
		if len(parts) == 0 {
			return
		}
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, parts...)
			return
		}
		contents = append(contents, geminiContent{Role: role, Parts: parts})
	}
	for _, message := range request.Messages {
		switch message.Role {
		case "system", "developer":
			text, err := openAIContentText(message.Content)
			if err != nil {
				return nil, err
			}
			system = append(system, text)
		case "user", "assistant":
			parts, err := geminiParts(message.Content)
			if err != nil {
				return nil, err
			}
			for _, call := range message.ToolCalls {
				toolNames[call.ID] = call.Function.Name
				parts = append(parts, geminiPart{"functionCall": map[string]any{"name": call.Function.Name, "args": toolArguments(call.Function.Arguments)}})
			}
			role := "user"
			if message.Role == "assistant" {
				role = "model"
			}
			appendParts(role, parts)
		case "tool":
			text, err := openAIContentText(message.Content)
			if err != nil {
				return nil, err
			}
			name := toolNames[message.ToolCallID]
			if name == "" {
				return nil, errTranslationUnsupported
			}
			var response any = map[string]string{"result": text}
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(text), &object) == nil && object != nil {
				response = object
			}
			appendParts("user", []geminiPart{{"functionResponse": map[string]any{"name": name, "response": response}}})
		default:
			return nil, errTranslationUnsupported
		}
	}
	generation := map[string]any{"maxOutputTokens": openAIOutputCeiling(request)}
	if request.Temperature != nil {
		generation["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		generation["topP"] = *request.TopP
	}
	if stops := openAIStopSequences(request.Stop); len(stops) > 0 {
		generation["stopSequences"] = stops
	}
	// ADR-995: JSON mode, with the caller's JSON Schema when it has one.
	switch format.kind {
	case "json_object":
		generation["responseMimeType"] = "application/json"
	case "json_schema":
		generation["responseMimeType"] = "application/json"
		generation["responseJsonSchema"] = format.schema
	}
	out := map[string]any{"contents": contents, "generationConfig": generation}
	if len(system) > 0 {
		out["systemInstruction"] = map[string]any{"parts": []geminiPart{{"text": strings.Join(system, "\n\n")}}}
	}
	if len(request.Tools) > 0 {
		declarations := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			declaration := map[string]any{"name": tool.Function.Name}
			if tool.Function.Description != "" {
				declaration["description"] = tool.Function.Description
			}
			if len(tool.Function.Parameters) > 0 {
				declaration["parameters"] = tool.Function.Parameters
			}
			declarations = append(declarations, declaration)
		}
		out["tools"] = []map[string]any{{"functionDeclarations": declarations}}
	}
	if mode, name := openAIToolChoice(request.ToolChoice); mode != "" {
		config := map[string]any{}
		switch mode {
		case "auto":
			config["mode"] = "AUTO"
		case "none":
			config["mode"] = "NONE"
		case "required":
			config["mode"] = "ANY"
		case "function":
			config["mode"], config["allowedFunctionNames"] = "ANY", []string{name}
		}
		out["toolConfig"] = map[string]any{"functionCallingConfig": config}
	}
	return json.Marshal(out)
}

func geminiParts(raw json.RawMessage) ([]geminiPart, error) {
	parts, err := openAIContentParts(raw)
	if err != nil {
		return nil, err
	}
	out := make([]geminiPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			if part.Text != "" {
				out = append(out, geminiPart{"text": part.Text})
			}
		case "image_url":
			if part.ImageURL == nil {
				return nil, errTranslationUnsupported
			}
			media, data, ok := dataURL(part.ImageURL.URL)
			if !ok {
				// generateContent needs inline bytes or an uploaded file URI.
				return nil, fmt.Errorf("%w: Gemini targets accept data: image URLs only", errTranslationUnsupported)
			}
			out = append(out, geminiPart{"inlineData": map[string]string{"mimeType": media, "data": data}})
		default:
			return nil, errTranslationUnsupported
		}
	}
	return out, nil
}
