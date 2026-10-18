package outbound

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// responseTextLoc is one generated text in a decoded response body or
// stream event (ADR-1435): the string holder[key]. channel names the
// streamed text a delta continues, one per choice, output part or content
// block; a whole text has none.
type responseTextLoc struct {
	holder  map[string]any
	key     string
	channel string
}

func (l responseTextLoc) text() string {
	text, _ := l.holder[l.key].(string)
	return text
}

func (l responseTextLoc) set(text string) { l.holder[l.key] = text }

// responseTextPath finds generated text under a JSON path; "*" steps into
// every element of an array. delta marks text that continues a stream
// channel in a stream event; when picks the events or bodies it applies to,
// and elem the array elements.
type responseTextPath struct {
	keys    []string
	delta   bool
	when    func(root map[string]any) bool
	elem    func(element map[string]any) bool
	channel func(root map[string]any, elements []map[string]any) string
}

// responseTexts returns the generated text of a decoded body, where every
// text is whole, or of one stream event. A JSON array, Gemini's stream
// without alt=sse, is read element by element.
func responseTexts(format aiWireFormat, value any, stream bool) []responseTextLoc {
	var roots []map[string]any
	switch typed := value.(type) {
	case map[string]any:
		roots = append(roots, typed)
	case []any:
		for _, item := range typed {
			if root, ok := item.(map[string]any); ok {
				roots = append(roots, root)
			}
		}
	}
	var out []responseTextLoc
	for _, root := range roots {
		for _, path := range responseTextPaths[format] {
			if path.when != nil && !path.when(root) {
				continue
			}
			walkResponseTextPath(root, path.keys, nil, path, func(holder map[string]any, key string, elements []map[string]any) {
				loc := responseTextLoc{holder: holder, key: key}
				if stream && path.delta {
					loc.channel = path.channel(root, elements)
				}
				out = append(out, loc)
			})
		}
	}
	return out
}

func walkResponseTextPath(node map[string]any, keys []string, elements []map[string]any, path responseTextPath,
	visit func(map[string]any, string, []map[string]any)) {
	if len(keys) == 1 {
		if _, ok := node[keys[0]].(string); ok {
			visit(node, keys[0], elements)
		}
		return
	}
	next := node[keys[0]]
	if keys[1] != "*" {
		if child, ok := next.(map[string]any); ok {
			walkResponseTextPath(child, keys[1:], elements, path, visit)
		}
		return
	}
	items, _ := next.([]any)
	for _, item := range items {
		element, ok := item.(map[string]any)
		if !ok || (path.elem != nil && !path.elem(element)) || len(keys) < 3 {
			continue
		}
		walkResponseTextPath(element, keys[2:], append(slices.Clip(elements), element), path, visit)
	}
}

// eventTypeIn picks events whose "type" is one of types; "" stands for a
// body without one.
func eventTypeIn(types ...string) func(map[string]any) bool {
	return func(root map[string]any) bool {
		kind, _ := root["type"].(string)
		return slices.Contains(types, kind)
	}
}

// typed passes elements without a "type" or of one of types.
func typed(types ...string) func(map[string]any) bool {
	return func(element map[string]any) bool {
		kind, ok := element["type"].(string)
		return !ok || slices.Contains(types, kind)
	}
}

// objectTyped picks events whose object field has one of types.
func objectTyped(field string, types ...string) func(map[string]any) bool {
	return func(root map[string]any) bool {
		object, _ := root[field].(map[string]any)
		kind, _ := object["type"].(string)
		return slices.Contains(types, kind)
	}
}

// notThought skips Gemini thought parts: thinking text is not checked.
func notThought(element map[string]any) bool {
	thought, _ := element["thought"].(bool)
	return !thought
}

// jsonIndex renders a decoded JSON number field, or "0".
func jsonIndex(value any) string {
	switch typed := value.(type) {
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return typed
	}
	return "0"
}

func elementChannel(prefix, field, suffix string) func(map[string]any, []map[string]any) string {
	return func(_ map[string]any, elements []map[string]any) string {
		index := "0"
		if len(elements) > 0 {
			index = jsonIndex(elements[0][field])
		}
		return prefix + index + suffix
	}
}

func rootChannel(prefix, field string) func(map[string]any, []map[string]any) string {
	return func(root map[string]any, _ []map[string]any) string { return prefix + jsonIndex(root[field]) }
}

func fixedChannel(name string) func(map[string]any, []map[string]any) string {
	return func(map[string]any, []map[string]any) string { return name }
}

// responsesChannel names a Responses API or transcription delta's text.
func responsesChannel(root map[string]any, _ []map[string]any) string {
	kind, _ := root["type"].(string)
	item, _ := root["item_id"].(string)
	return "item:" + item + ":" + jsonIndex(root["output_index"]) + ":" + jsonIndex(root["content_index"]) + ":" + kind
}

func pathKeys(keys ...string) []string { return keys }

// responseClosedChannels names the channels a stream event ends: an
// Anthropic content block's stop, a Responses API text's done event, or a
// Bedrock content block's stop. Their last text is released without
// waiting for more (ADR-1435).
func responseClosedChannels(format aiWireFormat, value any, eventType string) []string {
	root, _ := value.(map[string]any)
	if root == nil {
		return nil
	}
	kind, _ := root["type"].(string)
	switch {
	case kind == "content_block_stop" && (format == aiFormatAnthropic || format == aiFormatBedrockInvoke):
		return []string{"block:" + jsonIndex(root["index"])}
	case format == aiFormatOpenAI && (kind == "response.output_text.done" || kind == "response.refusal.done"):
		delta := strings.TrimSuffix(kind, ".done") + ".delta"
		item, _ := root["item_id"].(string)
		return []string{"item:" + item + ":" + jsonIndex(root["output_index"]) + ":" + jsonIndex(root["content_index"]) + ":" + delta}
	case format == aiFormatBedrockConverse && eventType == "contentBlockStop":
		return []string{"block:" + jsonIndex(root["contentBlockIndex"])}
	}
	return nil
}

// anthropicResponseTextPaths are Anthropic Messages text blocks, whole and
// streamed. Thinking, tool input and citations are not checked.
var anthropicResponseTextPaths = []responseTextPath{
	{keys: pathKeys("delta", "text"), delta: true, when: eventTypeIn("content_block_delta"), channel: rootChannel("block:", "index")},
	{keys: pathKeys("content", "*", "text"), elem: typed("text")},
	{keys: pathKeys("content_block", "text"), when: objectTyped("content_block", "text")},
	{keys: pathKeys("message", "content", "*", "text"), elem: typed("text")},
}

// responseTextPaths lists, per wire format, where generated text sits.
var responseTextPaths = map[aiWireFormat][]responseTextPath{
	aiFormatOpenAI: {
		// Chat and text completions.
		{keys: pathKeys("choices", "*", "delta", "content"), delta: true, channel: elementChannel("choice:", "index", ":content")},
		{keys: pathKeys("choices", "*", "delta", "refusal"), delta: true, channel: elementChannel("choice:", "index", ":refusal")},
		{keys: pathKeys("choices", "*", "text"), delta: true, channel: elementChannel("choice:", "index", ":text")},
		{keys: pathKeys("choices", "*", "message", "content")},
		{keys: pathKeys("choices", "*", "message", "refusal")},
		// Responses API and transcription stream events.
		{keys: pathKeys("delta"), delta: true, channel: responsesChannel,
			when: eventTypeIn("response.output_text.delta", "response.refusal.delta", "transcript.text.delta")},
		{keys: pathKeys("text"), when: eventTypeIn("", "response.output_text.done", "transcript.text.done")},
		{keys: pathKeys("refusal"), when: eventTypeIn("response.refusal.done")},
		{keys: pathKeys("part", "text"), when: objectTyped("part", "output_text")},
		{keys: pathKeys("part", "refusal"), when: objectTyped("part", "refusal")},
		{keys: pathKeys("item", "content", "*", "text"), elem: typed("output_text")},
		{keys: pathKeys("item", "content", "*", "refusal"), elem: typed("refusal")},
		// Responses API bodies, and the response its stream events carry.
		{keys: pathKeys("output", "*", "content", "*", "text"), elem: typed("message", "output_text")},
		{keys: pathKeys("output", "*", "content", "*", "refusal"), elem: typed("message", "refusal")},
		{keys: pathKeys("response", "output", "*", "content", "*", "text"), elem: typed("message", "output_text")},
		{keys: pathKeys("response", "output", "*", "content", "*", "refusal"), elem: typed("message", "refusal")},
	},
	aiFormatAnthropic: anthropicResponseTextPaths,
	aiFormatGemini: {
		{keys: pathKeys("candidates", "*", "content", "parts", "*", "text"), delta: true, elem: notThought,
			channel: elementChannel("candidate:", "index", "")},
	},
	aiFormatBedrockConverse: {
		{keys: pathKeys("output", "message", "content", "*", "text")},
		{keys: pathKeys("delta", "text"), delta: true, channel: rootChannel("block:", "contentBlockIndex")},
	},
	// Bedrock invoke bodies follow the model family: Anthropic, Llama
	// (generation), Mistral (outputs), Titan (results, outputText), Cohere
	// (generations, text), Claude text completions and OpenAI-compatible
	// models.
	aiFormatBedrockInvoke: append(slices.Clone(anthropicResponseTextPaths),
		responseTextPath{keys: pathKeys("generation"), delta: true, channel: fixedChannel("generation")},
		responseTextPath{keys: pathKeys("outputs", "*", "text"), delta: true, channel: fixedChannel("outputs")},
		responseTextPath{keys: pathKeys("results", "*", "outputText")},
		responseTextPath{keys: pathKeys("outputText"), delta: true, channel: fixedChannel("outputText")},
		responseTextPath{keys: pathKeys("generations", "*", "text")},
		responseTextPath{keys: pathKeys("text"), delta: true, channel: fixedChannel("text")},
		responseTextPath{keys: pathKeys("completion"), delta: true, channel: fixedChannel("completion")},
		responseTextPath{keys: pathKeys("choices", "*", "message", "content")},
		responseTextPath{keys: pathKeys("choices", "*", "delta", "content"), delta: true, channel: elementChannel("choice:", "index", ":content")},
	),
}
