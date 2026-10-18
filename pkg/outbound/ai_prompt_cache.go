package outbound

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AIPromptCacheHeader names the prompt-cache breakpoints the gateway added
// to a call (ADR-1437).
const AIPromptCacheHeader = "X-Gregale-AI-Prompt-Cache"

// Breakpoints the gateway adds, as the header and metric name them.
const (
	promptCacheMarkPrefix       = "prefix"
	promptCacheMarkConversation = "conversation"
)

// promptPrefixes remembers the stable prompt prefixes this process saw, so
// a prefix is marked only once it repeats within the cache lifetime. It is
// bounded and starts over when full.
type promptPrefixes struct {
	mu   sync.Mutex
	seen map[[32]byte]time.Time
}

// promptPrefixesMax bounds the remembered prefixes.
const promptPrefixesMax = 10_000

// repeated records a prefix and reports whether it was seen within the
// cache lifetime.
func (p *promptPrefixes) repeated(key [32]byte, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil || len(p.seen) >= promptPrefixesMax {
		p.seen = make(map[[32]byte]time.Time)
	}
	last, ok := p.seen[key]
	p.seen[key] = now
	return ok && now.Sub(last) < api.OutboundAIPromptCacheLifetimeSeconds*time.Second
}

func promptPrefixKey(integrationID, model string, parts ...json.RawMessage) [32]byte {
	hash := sha256.New()
	hash.Write([]byte(integrationID + "\x00" + model + "\x00"))
	for _, part := range parts {
		hash.Write(part)
		hash.Write([]byte{0})
	}
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	return key
}

// promptCacheTokens estimates tokens at four bytes per token, as
// reservations do.
func promptCacheTokens(size int) int64 { return int64(size) / api.OutboundAIReserveBytesPerToken }

// applyPromptCache adds breakpoints to a buffered Claude request whose
// integration turned automatic prompt caching on, and returns the marks it
// added (ADR-1437). A request that already marks its own cache is left as
// it is.
func (h *Handler) applyPromptCache(r *http.Request, integration Integration, plan *aiCallPlan, ai *aiRequest) []string {
	if !integration.AIAutoPromptCache || plan == nil || ai == nil || !plan.endpoint.generation || plan.endpoint.batch {
		return nil
	}
	marker := promptCacheMarker{
		session: r.Header.Get(api.OutboundAISessionHeader) != "",
		key: func(parts ...json.RawMessage) [32]byte {
			return promptPrefixKey(integration.ID, plan.facts.model, parts...)
		},
		prefixes: &h.promptPrefixes,
		now:      time.Now(),
	}
	var body []byte
	var marks []string
	switch plan.endpoint.format {
	case aiFormatAnthropic, aiFormatBedrockInvoke:
		body, marks = marker.anthropic(ai.body, plan.endpoint.format == aiFormatBedrockInvoke)
	case aiFormatBedrockConverse:
		model := strings.ToLower(plan.facts.model)
		if !strings.Contains(model, "anthropic") && !strings.Contains(model, "claude") {
			return nil
		}
		body, marks = marker.converse(ai.body)
	default:
		return nil
	}
	if len(marks) > 0 {
		ai.body = body
	}
	return marks
}

// promptCacheMarker places breakpoints in one request body.
type promptCacheMarker struct {
	session  bool
	key      func(parts ...json.RawMessage) [32]byte
	prefixes *promptPrefixes
	now      time.Time
}

// wantsPrefix reports a stable prefix worth a breakpoint: long enough, and
// repeated or part of a named session.
func (m promptCacheMarker) wantsPrefix(parts ...json.RawMessage) bool {
	size := 0
	for _, part := range parts {
		size += len(part)
	}
	if promptCacheTokens(size) < api.OutboundAIPromptCacheMinTokens {
		return false
	}
	repeated := m.prefixes.repeated(m.key(parts...), m.now)
	return repeated || m.session
}

// wantsConversation reports a prompt worth caching for its next turn.
func (m promptCacheMarker) wantsConversation(body []byte, messages json.RawMessage) bool {
	return promptCacheTokens(len(body)) >= api.OutboundAIPromptCacheMinTokens && (m.session || hasAssistantTurn(messages))
}

// ephemeralCache is a five-minute Anthropic breakpoint.
var ephemeralCache = map[string]any{"type": "ephemeral"}

// anthropic marks an Anthropic Messages body: the last system block, or
// else the last tool, and the last eligible block of the last message. A
// Bedrock invoke body must name its anthropic_version.
func (m promptCacheMarker) anthropic(body []byte, invoke bool) ([]byte, []string) {
	if bytes.Contains(body, []byte(`"cache_control"`)) {
		return body, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || (invoke && len(fields["anthropic_version"]) == 0) {
		return body, nil
	}
	var marks []string
	if m.wantsPrefix(fields["tools"], fields["system"]) {
		if marked, ok := markAnthropicBlocks(fields["system"], false); ok {
			fields["system"], marks = marked, append(marks, promptCacheMarkPrefix)
		} else if marked, ok := markLastObject(fields["tools"], "cache_control", ephemeralCache); ok {
			fields["tools"], marks = marked, append(marks, promptCacheMarkPrefix)
		}
	}
	if m.wantsConversation(body, fields["messages"]) {
		if marked, ok := markLastMessage(fields["messages"], func(content json.RawMessage) (json.RawMessage, bool) {
			return markAnthropicBlocks(content, true)
		}); ok {
			fields["messages"], marks = marked, append(marks, promptCacheMarkConversation)
		}
	}
	return encodeMarked(body, fields, marks)
}

// converse marks a Bedrock Converse body with cachePoint blocks: after the
// system blocks, or else the tools, and after the last message's content.
func (m promptCacheMarker) converse(body []byte) ([]byte, []string) {
	if bytes.Contains(body, []byte(`"cachePoint"`)) {
		return body, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return body, nil
	}
	cachePoint := map[string]any{"cachePoint": map[string]any{"type": "default"}}
	var marks []string
	if m.wantsPrefix(fields["toolConfig"], fields["system"]) {
		if marked, ok := appendToArray(fields["system"], cachePoint); ok {
			fields["system"], marks = marked, append(marks, promptCacheMarkPrefix)
		} else if marked, ok := appendToolCachePoint(fields["toolConfig"], cachePoint); ok {
			fields["toolConfig"], marks = marked, append(marks, promptCacheMarkPrefix)
		}
	}
	if m.wantsConversation(body, fields["messages"]) {
		if marked, ok := markLastMessage(fields["messages"], func(content json.RawMessage) (json.RawMessage, bool) {
			return appendToArray(content, cachePoint)
		}); ok {
			fields["messages"], marks = marked, append(marks, promptCacheMarkConversation)
		}
	}
	return encodeMarked(body, fields, marks)
}

func encodeMarked(body []byte, fields map[string]json.RawMessage, marks []string) ([]byte, []string) {
	if len(marks) == 0 {
		return body, nil
	}
	encoded, err := encodeResponseJSON(fields)
	if err != nil {
		return body, nil
	}
	return encoded, marks
}

// hasAssistantTurn reports a conversation that carries earlier turns.
func hasAssistantTurn(messages json.RawMessage) bool {
	var turns []struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(messages, &turns) != nil {
		return false
	}
	for _, turn := range turns {
		if turn.Role == "assistant" {
			return true
		}
	}
	return false
}

// cacheableAnthropicBlock reports a block that can carry cache_control:
// non-empty text, images, documents, tool uses and results, and search
// results. Thinking blocks cannot.
func cacheableAnthropicBlock(block map[string]any) bool {
	switch kind, _ := block["type"].(string); kind {
	case "text":
		text, _ := block["text"].(string)
		return strings.TrimSpace(text) != ""
	case "image", "document", "tool_use", "tool_result", "search_result":
		return true
	}
	return false
}

// markAnthropicBlocks marks the last eligible block of a system prompt or
// message content. A non-empty string becomes one text block that carries
// the breakpoint.
func markAnthropicBlocks(raw json.RawMessage, content bool) (json.RawMessage, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil, false
		}
		return marshalRaw([]any{map[string]any{"type": "text", "text": text, "cache_control": ephemeralCache}})
	}
	var blocks []map[string]any
	if decodeUseNumber(raw, &blocks) != nil {
		return nil, false
	}
	for index := len(blocks) - 1; index >= 0; index-- {
		if cacheableAnthropicBlock(blocks[index]) {
			blocks[index]["cache_control"] = ephemeralCache
			return marshalRaw(blocks)
		}
		if !content {
			// A system prompt's breakpoint must sit at its end.
			return nil, false
		}
	}
	return nil, false
}

// markLastMessage rewrites the last message's content with mark.
func markLastMessage(raw json.RawMessage, mark func(json.RawMessage) (json.RawMessage, bool)) (json.RawMessage, bool) {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return nil, false
	}
	last := messages[len(messages)-1]
	marked, ok := mark(last["content"])
	if !ok {
		return nil, false
	}
	last["content"] = marked
	return marshalRaw(messages)
}

// markLastObject sets key on the last object of an array.
func markLastObject(raw json.RawMessage, key string, value any) (json.RawMessage, bool) {
	var items []map[string]any
	if decodeUseNumber(raw, &items) != nil || len(items) == 0 {
		return nil, false
	}
	items[len(items)-1][key] = value
	return marshalRaw(items)
}

// appendToArray appends an element to a non-empty array.
func appendToArray(raw json.RawMessage, element any) (json.RawMessage, bool) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return nil, false
	}
	encoded, err := json.Marshal(element)
	if err != nil {
		return nil, false
	}
	return marshalRaw(append(items, encoded))
}

// appendToolCachePoint appends a cachePoint to a Converse toolConfig's
// tools.
func appendToolCachePoint(raw json.RawMessage, cachePoint any) (json.RawMessage, bool) {
	var config map[string]json.RawMessage
	if json.Unmarshal(raw, &config) != nil {
		return nil, false
	}
	tools, ok := appendToArray(config["tools"], cachePoint)
	if !ok {
		return nil, false
	}
	config["tools"] = tools
	return marshalRaw(config)
}

func decodeUseNumber(raw json.RawMessage, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(value)
}

func marshalRaw(value any) (json.RawMessage, bool) {
	encoded, err := encodeResponseJSON(value)
	return encoded, err == nil
}
