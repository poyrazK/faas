package outbound

import (
	"bytes"
	"context"
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

// AIPromptPrefixBackend shares prompt-prefix sightings across outboundd
// replicas (ADR 20261019132139).
type AIPromptPrefixBackend interface {
	// SeenAIPromptPrefix records a sighting of a prefix hash and reports
	// whether an earlier one fell within lifetime.
	SeenAIPromptPrefix(ctx context.Context, integrationID string, key [32]byte, lifetime time.Duration) (bool, error)
}

// Results counted in outbound_ai_prompt_cache_shared_total.
const (
	promptPrefixSharedRepeated  = "repeated"
	promptPrefixSharedFirst     = "first"
	promptPrefixSharedRefreshed = "refreshed"
	promptPrefixSharedError     = "error"
)

// promptPrefixes remembers the stable prompt prefixes this process saw, so
// a prefix is marked only once it repeats within the cache lifetime. It is
// bounded and starts over when full.
type promptPrefixes struct {
	mu   sync.Mutex
	seen map[[32]byte]promptPrefixSighting
}

// promptPrefixSighting is when this process last saw a prefix and last told
// the other replicas about it.
type promptPrefixSighting struct {
	last, shared time.Time
}

// promptPrefixesMax bounds the remembered prefixes.
const promptPrefixesMax = 10_000

// repeated records a prefix and reports whether this process saw it within
// the cache lifetime, and whether the shared row is due a refresh.
func (p *promptPrefixes) repeated(key [32]byte, now time.Time) (repeated, refresh bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil || len(p.seen) >= promptPrefixesMax {
		p.seen = make(map[[32]byte]promptPrefixSighting)
	}
	sighting, ok := p.seen[key]
	repeated = ok && now.Sub(sighting.last) < api.OutboundAIPromptCacheLifetimeSeconds*time.Second
	refresh = repeated && now.Sub(sighting.shared) >= api.OutboundAIPromptPrefixRefreshSeconds*time.Second
	sighting.last = now
	if refresh || !repeated {
		// A first sight is shared synchronously; a due refresh in the
		// background. Either way the next one is a minute away.
		sighting.shared = now
	}
	p.seen[key] = sighting
	return repeated, refresh
}

// sharedPrefixes asks the other replicas about prefixes this process has not
// seen (ADR 20261019132139). A slow or failing backend answers "not seen",
// so marking never delays a call beyond its lookup bound.
type sharedPrefixes struct {
	backend       AIPromptPrefixBackend
	integrationID string
	metrics       *Metrics
	metricID      string
}

func (s sharedPrefixes) seen(ctx context.Context, key [32]byte) bool {
	ctx, cancel := context.WithTimeout(ctx, api.OutboundAIPromptPrefixLookupTimeout)
	defer cancel()
	repeated, err := s.backend.SeenAIPromptPrefix(ctx, s.integrationID, key, api.OutboundAIPromptCacheLifetimeSeconds*time.Second)
	switch {
	case err != nil:
		s.metrics.ObserveAIPromptPrefixShared(s.metricID, promptPrefixSharedError)
		return false
	case repeated:
		s.metrics.ObserveAIPromptPrefixShared(s.metricID, promptPrefixSharedRepeated)
	default:
		s.metrics.ObserveAIPromptPrefixShared(s.metricID, promptPrefixSharedFirst)
	}
	return repeated
}

// refresh keeps a locally repeated prefix fresh for the other replicas, in
// the background.
func (s sharedPrefixes) refresh(ctx context.Context, key [32]byte) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, api.OutboundAIPromptPrefixLookupTimeout)
		defer cancel()
		result := promptPrefixSharedRefreshed
		if _, err := s.backend.SeenAIPromptPrefix(ctx, s.integrationID, key, api.OutboundAIPromptCacheLifetimeSeconds*time.Second); err != nil {
			result = promptPrefixSharedError
		}
		s.metrics.ObserveAIPromptPrefixShared(s.metricID, result)
	}()
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
	if backend, ok := h.Backend.(AIPromptPrefixBackend); ok {
		marker.shared = &sharedPrefixes{backend: backend, integrationID: integration.ID,
			metrics: h.Metrics, metricID: integration.MetricLabel()}
	}
	var body []byte
	var marks []string
	switch plan.endpoint.format {
	case aiFormatAnthropic, aiFormatBedrockInvoke:
		body, marks = marker.anthropic(r.Context(), ai.body, plan.endpoint.format == aiFormatBedrockInvoke)
	case aiFormatBedrockConverse:
		model := strings.ToLower(plan.facts.model)
		if !strings.Contains(model, "anthropic") && !strings.Contains(model, "claude") {
			return nil
		}
		body, marks = marker.converse(r.Context(), ai.body)
	default:
		return nil
	}
	if len(marks) > 0 {
		ai.body = body
	}
	return marks
}

// promptCacheMarker places breakpoints in one request body. shared, when
// set, answers for prefixes this process has not seen.
type promptCacheMarker struct {
	session  bool
	key      func(parts ...json.RawMessage) [32]byte
	prefixes *promptPrefixes
	shared   *sharedPrefixes
	now      time.Time
}

// wantsPrefix reports a stable prefix worth a breakpoint: long enough, and
// repeated or part of a named session.
func (m promptCacheMarker) wantsPrefix(ctx context.Context, parts ...json.RawMessage) bool {
	size := 0
	for _, part := range parts {
		size += len(part)
	}
	if promptCacheTokens(size) < api.OutboundAIPromptCacheMinTokens {
		return false
	}
	key := m.key(parts...)
	repeated, refresh := m.prefixes.repeated(key, m.now)
	switch {
	case m.session:
		return true
	case m.shared == nil:
		return repeated
	case repeated:
		if refresh {
			m.shared.refresh(ctx, key)
		}
		return true
	}
	return m.shared.seen(ctx, key)
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
func (m promptCacheMarker) anthropic(ctx context.Context, body []byte, invoke bool) ([]byte, []string) {
	if bytes.Contains(body, []byte(`"cache_control"`)) {
		return body, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || (invoke && len(fields["anthropic_version"]) == 0) {
		return body, nil
	}
	var marks []string
	if m.wantsPrefix(ctx, fields["tools"], fields["system"]) {
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
func (m promptCacheMarker) converse(ctx context.Context, body []byte) ([]byte, []string) {
	if bytes.Contains(body, []byte(`"cachePoint"`)) {
		return body, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return body, nil
	}
	cachePoint := map[string]any{"cachePoint": map[string]any{"type": "default"}}
	var marks []string
	if m.wantsPrefix(ctx, fields["toolConfig"], fields["system"]) {
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
