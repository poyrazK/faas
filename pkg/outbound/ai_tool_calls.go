package outbound

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"sort"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// Tool calls a model requests (ADR 20261020202035). The gateway keeps their
// names for the request log and the trace, and hashes names and arguments
// so a trace can spot a model asking for the same thing again. Arguments
// are hashed as they arrive and never kept.

// aiToolCall is one requested tool call. A streamed call's arguments arrive
// in fragments, hashed in order. raw keeps them, up to
// OutboundAIQualityToolArgumentsMaxBytes, to check that they parse
// (ADR 20261021171712); longer arguments are not checked.
type aiToolCall struct {
	name     string
	args     hash.Hash
	raw      []byte
	overflow bool
}

// write adds the next bytes of the call's arguments.
func (c *aiToolCall) write(p []byte) {
	c.args.Write(p)
	if c.overflow {
		return
	}
	if len(c.raw)+len(p) > api.OutboundAIQualityToolArgumentsMaxBytes {
		c.raw, c.overflow = nil, true
		return
	}
	c.raw = append(c.raw, p...)
}

// aiToolCalls collects one response's tool calls. byKey finds a call by
// its provider ID, or by its stream position while its fragments arrive,
// so a call reported twice (a streamed item, then the final response)
// counts once.
type aiToolCalls struct {
	calls []*aiToolCall
	byKey map[string]*aiToolCall
}

// call returns the call with the key, adding it when new. It returns nil
// past OutboundAIToolCallsPerResponseMax calls. An empty key is always new.
func (t *aiToolCalls) call(key, name string) (*aiToolCall, bool) {
	if key != "" {
		if existing := t.byKey[key]; existing != nil {
			if existing.name == "" {
				existing.name = sanitizeAIToolName(name)
			}
			return existing, false
		}
	}
	if len(t.calls) >= api.OutboundAIToolCallsPerResponseMax {
		return nil, false
	}
	call := &aiToolCall{name: sanitizeAIToolName(name), args: sha256.New()}
	t.calls = append(t.calls, call)
	if key != "" {
		if t.byKey == nil {
			t.byKey = make(map[string]*aiToolCall)
		}
		t.byKey[key] = call
	}
	return call, true
}

// complete records a call reported whole; a repeat of a known key is
// ignored.
func (t *aiToolCalls) complete(key, name string, args []byte) {
	if call, added := t.call(key, name); added {
		call.write(args)
	}
}

// fragment adds a piece of a streamed call: its name when the stream names
// it, and the next fragment of its arguments.
func (t *aiToolCalls) fragment(key, name string, args []byte) {
	if call, _ := t.call(key, name); call != nil {
		call.write(args)
	}
}

// names lists the named calls in the order they were requested.
func (t *aiToolCalls) names() []string {
	var names []string
	for _, call := range t.calls {
		if call.name != "" {
			names = append(names, call.name)
		}
	}
	return names
}

// invalidArguments reports a named call whose arguments, when checked, do
// not parse as JSON. Empty arguments are a call without parameters.
func (t *aiToolCalls) invalidArguments() bool {
	for _, call := range t.calls {
		if call.name != "" && !call.overflow && len(bytes.TrimSpace(call.raw)) > 0 && !json.Valid(call.raw) {
			return true
		}
	}
	return false
}

// signature is a 16-byte hash of the named calls and their arguments, in
// any order, or nil for a response that requested none.
func (t *aiToolCalls) signature() []byte {
	var entries []string
	for _, call := range t.calls {
		if call.name != "" {
			entries = append(entries, call.name+"\x00"+hex.EncodeToString(call.args.Sum(nil)))
		}
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Strings(entries)
	digest := sha256.New()
	for _, entry := range entries {
		digest.Write([]byte(entry))
		digest.Write([]byte{'\n'})
	}
	return digest.Sum(nil)[:16]
}

// sanitizeAIToolName bounds a provider-reported tool name to printable
// ASCII of at most OutboundAIToolNameMaxBytes.
func sanitizeAIToolName(name string) string {
	var b []byte
	for i := 0; i < len(name) && len(b) < api.OutboundAIToolNameMaxBytes; i++ {
		if c := name[i]; c > 0x20 && c <= 0x7e {
			b = append(b, c)
		}
	}
	return string(b)
}

// Markers of envelope fields that can carry tool calls; a byte search
// skips the parse for the many that do not.
var (
	toolMarkerChat      = []byte(`"tool_calls"`)
	toolMarkerUse       = []byte(`"tool_use"`)
	toolMarkerFunction  = []byte(`"functionCall"`)
	toolMarkerResponses = []byte(`_call"`)
	toolMarkerBedrock   = []byte(`"toolUse"`)
	toolMarkerPartial   = []byte(`"partial_json"`)
)

// noteToolCalls reads the tool calls an envelope carries, in every format
// the gateway meters.
func (o *aiUsageObserver) noteToolCalls(envelope aiEnvelope) {
	tools := &o.tools
	if bytes.Contains(envelope.Choices, toolMarkerChat) {
		noteChatToolCalls(tools, envelope.Choices)
	}
	if bytes.Contains(envelope.Content, toolMarkerUse) {
		noteAnthropicToolUses(tools, envelope.Content)
	}
	if envelope.Message != nil && bytes.Contains(envelope.Message.Content, toolMarkerUse) {
		noteAnthropicToolUses(tools, envelope.Message.Content)
	}
	index, indexed := rawIndex(envelope.Index)
	if bytes.Contains(envelope.ContentBlock, toolMarkerUse) && indexed {
		var block struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(envelope.ContentBlock, &block) == nil && block.Type == "tool_use" {
			tools.fragment("anthropic:"+strconv.Itoa(index), block.Name, nil)
		}
	}
	if bytes.Contains(envelope.Delta, toolMarkerPartial) && indexed {
		var delta struct {
			PartialJSON string `json:"partial_json"`
		}
		if json.Unmarshal(envelope.Delta, &delta) == nil {
			if call := tools.byKey["anthropic:"+strconv.Itoa(index)]; call != nil {
				call.write([]byte(delta.PartialJSON))
			}
		}
	}
	if bytes.Contains(envelope.Candidates, toolMarkerFunction) {
		noteGeminiFunctionCalls(tools, envelope.Candidates)
	}
	for _, output := range []json.RawMessage{envelope.Output, responseOutput(envelope.Response)} {
		if bytes.Contains(output, toolMarkerResponses) || bytes.Contains(output, toolMarkerBedrock) {
			noteOutputToolCalls(tools, output)
		}
	}
	if envelope.Type == "response.output_item.done" && bytes.Contains(envelope.Item, toolMarkerResponses) {
		noteResponsesItem(tools, envelope.Item)
	}
	if blockIndex, ok := rawIndex(envelope.ContentBlockIndex); ok {
		noteBedrockStreamToolUse(tools, blockIndex, envelope.Start, envelope.Delta)
	}
}

// rawIndex reads a stream position. Envelope fields stay raw, so an
// unexpected value in one never fails the parse that finds usage.
func rawIndex(raw json.RawMessage) (int, bool) {
	var index int
	if len(raw) == 0 || json.Unmarshal(raw, &index) != nil || index < 0 {
		return 0, false
	}
	return index, true
}

func responseOutput(inner *aiEnvelopeInner) json.RawMessage {
	if inner == nil {
		return nil
	}
	return inner.Output
}

// noteChatToolCalls reads OpenAI Chat Completions tool calls: whole in a
// message, or in fragments keyed by choice and tool index in a stream.
func noteChatToolCalls(tools *aiToolCalls, raw json.RawMessage) {
	var choices []struct {
		Index   int             `json:"index"`
		Message *chatToolHolder `json:"message"`
		Delta   *chatToolHolder `json:"delta"`
	}
	if json.Unmarshal(raw, &choices) != nil {
		return
	}
	for _, choice := range choices {
		if choice.Message != nil {
			for position, call := range choice.Message.ToolCalls {
				key := call.ID
				if key == "" {
					key = "chat:" + strconv.Itoa(choice.Index) + ":" + strconv.Itoa(position)
				}
				tools.complete(key, call.Function.Name, []byte(call.Function.Arguments))
			}
		}
		if choice.Delta != nil {
			for _, call := range choice.Delta.ToolCalls {
				index := 0
				if call.Index != nil {
					index = *call.Index
				}
				tools.fragment("chat:"+strconv.Itoa(choice.Index)+":"+strconv.Itoa(index), call.Function.Name,
					[]byte(call.Function.Arguments))
			}
		}
	}
}

type chatToolHolder struct {
	ToolCalls []struct {
		Index    *int   `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// noteAnthropicToolUses reads Anthropic Messages tool_use blocks.
func noteAnthropicToolUses(tools *aiToolCalls, raw json.RawMessage) {
	var blocks []struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}
	for _, block := range blocks {
		if block.Type == "tool_use" {
			tools.complete(block.ID, block.Name, block.Input)
		}
	}
}

// noteGeminiFunctionCalls reads Gemini functionCall parts. A stream sends
// each call whole in one chunk.
func noteGeminiFunctionCalls(tools *aiToolCalls, raw json.RawMessage) {
	var candidates []struct {
		Content struct {
			Parts []struct {
				FunctionCall *struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &candidates) != nil {
		return
	}
	for _, candidate := range candidates {
		for _, part := range candidate.Content.Parts {
			if call := part.FunctionCall; call != nil {
				tools.complete(call.ID, call.Name, call.Args)
			}
		}
	}
}

// noteOutputToolCalls reads an OpenAI Responses output array or a Bedrock
// Converse output message.
func noteOutputToolCalls(tools *aiToolCalls, raw json.RawMessage) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if json.Unmarshal(trimmed, &items) == nil {
			for _, item := range items {
				noteResponsesItem(tools, item)
			}
		}
		return
	}
	var output struct {
		Message struct {
			Content []struct {
				ToolUse *struct {
					ToolUseID string          `json:"toolUseId"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
				} `json:"toolUse"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(trimmed, &output) != nil {
		return
	}
	for _, block := range output.Message.Content {
		if use := block.ToolUse; use != nil {
			tools.complete(use.ToolUseID, use.Name, use.Input)
		}
	}
}

// noteResponsesItem reads one OpenAI Responses function or custom tool
// call item.
func noteResponsesItem(tools *aiToolCalls, raw json.RawMessage) {
	var item struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Input     string `json:"input"`
	}
	if json.Unmarshal(raw, &item) != nil || (item.Type != "function_call" && item.Type != "custom_tool_call") {
		return
	}
	key := item.CallID
	if key == "" {
		key = item.ID
	}
	tools.complete(key, item.Name, []byte(item.Arguments+item.Input))
}

// noteBedrockStreamToolUse reads a Bedrock ConverseStream tool use: its
// start names it, its deltas carry its input.
func noteBedrockStreamToolUse(tools *aiToolCalls, index int, start, delta json.RawMessage) {
	key := "bedrock:" + strconv.Itoa(index)
	if bytes.Contains(start, toolMarkerBedrock) {
		var block struct {
			ToolUse *struct {
				Name string `json:"name"`
			} `json:"toolUse"`
		}
		if json.Unmarshal(start, &block) == nil && block.ToolUse != nil {
			tools.fragment(key, block.ToolUse.Name, nil)
		}
	}
	if bytes.Contains(delta, toolMarkerBedrock) {
		var block struct {
			ToolUse *struct {
				Input string `json:"input"`
			} `json:"toolUse"`
		}
		if json.Unmarshal(delta, &block) == nil && block.ToolUse != nil {
			if call := tools.byKey[key]; call != nil {
				call.write([]byte(block.ToolUse.Input))
			}
		}
	}
}
