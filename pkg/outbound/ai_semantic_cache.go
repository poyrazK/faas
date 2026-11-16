package outbound

// ADR 20261112194947: the semantic cache answers a call from the response
// cache when its last user turn means the same as a cached call's and the
// rest of the call matches exactly. The index holds a unit vector per
// cached answer and points at the answer's exact response-cache entry, so
// no response is stored twice.

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	aiSemanticHit     = "hit"
	aiSemanticMiss    = "miss"
	aiSemanticSkipped = "skipped"
	aiSemanticError   = "error"

	// AICacheHeader names how the cache answered a call; a semantic hit
	// carries "semantic".
	AICacheHeader = "X-Gregale-AI-Cache"
	// AISimilarityHeader is a semantic hit's cosine similarity.
	AISimilarityHeader = "X-Gregale-AI-Similarity"

	// aiSemanticMask stands in for the last user turn's text in the
	// partition key.
	aiSemanticMask = "\x00gregale-semantic-turn\x00"
)

var errSemanticCandidate = errors.New("outbound: not a semantic cache candidate")

// aiSemanticCandidate is a call the semantic cache can answer: the text it
// embeds and the partition that a cached answer must share.
type aiSemanticCandidate struct {
	text      string
	partition string
}

// semanticCandidate reads a buffered generation call's last user turn. The
// partition is the call's exact cache key with that turn's text masked,
// plus the end user unless the policy shares answers across end users. It
// is keyed, as the exact key is, before the credential is applied.
func (h *Handler) semanticCandidate(integration Integration, plan *aiCallPlan, ai *aiRequest, req *http.Request,
	principalID, endUser string) (*aiSemanticCandidate, error) {
	semantic := integration.AISemanticCache
	if semantic == nil || h.semanticIndex == nil || plan == nil || ai == nil || !plan.endpoint.generation || plan.endpoint.batch ||
		plan.endpoint.media != aiMediaNone {
		return nil, errSemanticCandidate
	}
	text, masked, err := semanticTurn(plan.endpoint.format, plan.path, ai.body)
	if err != nil {
		return nil, err
	}
	partition := h.responseCache.key(integration.ID, principalID, integration.PolicyRevision, integration.ResponseCacheTTLSeconds, req, masked)
	if partition == "" {
		return nil, errSemanticCandidate
	}
	if !semantic.ShareAcrossUsers {
		digest := sha256.Sum256([]byte(partition + "\x00" + endUser))
		partition = hex.EncodeToString(digest[:])
	}
	return &aiSemanticCandidate{text: text, partition: partition}, nil
}

// semanticTurn returns the text of a call's last turn, which must be a
// user turn of text only, and the body with that text masked. A call with
// more than OutboundAISemanticCacheMaxTurns user turns, or more than
// OutboundAISemanticCacheMaxInputBytes of text, is not a candidate.
func semanticTurn(format aiWireFormat, path string, body []byte) (string, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var doc map[string]any
	if decoder.Decode(&doc) != nil {
		return "", nil, errSemanticCandidate
	}
	var field string
	switch {
	case format == aiFormatOpenAI && strings.HasSuffix(path, "/chat/completions"), format == aiFormatAnthropic:
		field = "messages"
	case format == aiFormatOpenAI && strings.HasSuffix(path, "/responses"):
		field = "input"
		if text, ok := doc[field].(string); ok {
			doc[field] = aiSemanticMask
			return semanticResult(text, doc)
		}
	case format == aiFormatGemini:
		field = "contents"
	default:
		return "", nil, errSemanticCandidate
	}
	turns, ok := doc[field].([]any)
	if !ok || len(turns) == 0 {
		return "", nil, errSemanticCandidate
	}
	users := 0
	for _, turn := range turns {
		if semanticUserTurn(format, turn) {
			users++
		}
	}
	last, ok := turns[len(turns)-1].(map[string]any)
	if !ok || users > api.OutboundAISemanticCacheMaxTurns || !semanticUserTurn(format, last) {
		return "", nil, errSemanticCandidate
	}
	key := "content"
	if format == aiFormatGemini {
		key = "parts"
	}
	text, masked, ok := semanticTurnText(last[key])
	if !ok {
		return "", nil, errSemanticCandidate
	}
	last[key] = masked
	return semanticResult(text, doc)
}

func semanticResult(text string, doc map[string]any) (string, []byte, error) {
	if strings.TrimSpace(text) == "" || len(text) > api.OutboundAISemanticCacheMaxInputBytes {
		return "", nil, errSemanticCandidate
	}
	masked, err := json.Marshal(doc)
	if err != nil {
		return "", nil, errSemanticCandidate
	}
	return text, masked, nil
}

// semanticUserTurn reports a turn the user wrote. A Responses item with a
// type other than message (a tool output) is not one; a Gemini turn with
// no role is the user's.
func semanticUserTurn(format aiWireFormat, value any) bool {
	turn, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if kind, ok := turn["type"].(string); ok && kind != "message" {
		return false
	}
	role, _ := turn["role"].(string)
	return role == "user" || (format == aiFormatGemini && role == "")
}

// semanticTurnText joins a turn's text parts and masks them. A part that
// is not text, such as an image, audio, file or tool result, makes the
// turn no candidate.
func semanticTurnText(content any) (string, any, bool) {
	if text, ok := content.(string); ok {
		return text, aiSemanticMask, true
	}
	parts, ok := content.([]any)
	if !ok || len(parts) == 0 {
		return "", nil, false
	}
	texts := make([]string, 0, len(parts))
	for _, value := range parts {
		part, ok := value.(map[string]any)
		if !ok {
			return "", nil, false
		}
		text, ok := part["text"].(string)
		kind, typed := part["type"].(string)
		if !ok || (typed && kind != "text" && kind != "input_text") {
			return "", nil, false
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n"), aiSemanticMask, true
}

// semanticEmbedding embeds text through the policy's embedding integration
// in process, admitted, capped and metered on it under the caller
// semantic_cache. The vector comes back at unit length.
func (h *Handler) semanticEmbedding(ctx context.Context, accountID string, semantic *api.OutboundAISemanticCache, text string) ([]float32, error) {
	integration, err := h.Resolver.Integration(ctx, semantic.EmbeddingIntegrationID)
	if err != nil || !internalAllowed(integration, internalPrincipal{accountID: accountID}) ||
		!slices.Contains(api.OutboundAISemanticCacheEmbeddingProviders, integration.AIProvider) {
		return nil, errSemanticCandidate
	}
	body, err := json.Marshal(map[string]string{"model": semantic.EmbeddingModel, "input": text})
	if err != nil {
		return nil, err
	}
	translation, err := translateEmbeddingsRequest(integration, "/v1/embeddings", "", body, semantic.EmbeddingModel)
	if err != nil {
		return nil, err
	}
	call := h.internalCall(ctx, internalPrincipal{accountID: accountID, caller: AIUsageCallerSemanticCache}, integration.ID,
		replayAttempt{path: translation.path, rawQuery: translation.rawQuery, body: translation.body,
			translator: translation.translator, model: semantic.EmbeddingModel},
		api.OutboundAISemanticCacheEmbedTimeoutSeconds*time.Second)
	if !call.succeeded() {
		return nil, errors.New("outbound: the semantic cache embedding call failed")
	}
	return semanticVector(call.body)
}

// semanticVector reads an OpenAI embeddings list's first vector and
// scales it to unit length.
func semanticVector(body []byte) ([]float32, error) {
	var list struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &list) != nil || len(list.Data) == 0 {
		return nil, errors.New("outbound: the embedding response has no vector")
	}
	values := list.Data[0].Embedding
	if len(values) == 0 || len(values) > api.OutboundAISemanticCacheMaxDimensions {
		return nil, errors.New("outbound: the embedding has an unusable dimension")
	}
	var norm float64
	for _, value := range values {
		norm += value * value
	}
	norm = math.Sqrt(norm)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, errors.New("outbound: the embedding is not a direction")
	}
	vector := make([]float32, len(values))
	for index, value := range values {
		vector[index] = float32(value / norm)
	}
	return vector, nil
}

// semanticLookup searches the index after the exact cache missed and the
// moderation check passed. On a hit it returns the cached response; on a
// miss, the vector to index the answer with once it is cached. Any failure
// sends the call to the provider.
func (h *Handler) semanticLookup(ctx context.Context, w http.ResponseWriter, integration Integration, candidate *aiSemanticCandidate,
	metricIntegrationID string) (*http.Response, []float32) {
	vector, err := h.semanticEmbedding(ctx, integration.AccountID, integration.AISemanticCache, candidate.text)
	if err != nil {
		h.Metrics.ObserveAISemanticCache(metricIntegrationID, aiSemanticError)
		return nil, nil
	}
	threshold := float32(integration.AISemanticCache.Threshold()) / 10_000
	for {
		cacheKey, similarity, found := h.semanticIndex.nearest(integration.ID, candidate.partition, vector, threshold)
		if !found {
			h.Metrics.ObserveAISemanticCache(metricIntegrationID, aiSemanticMiss)
			return nil, vector
		}
		if cached, ok := h.responseCache.get(cacheKey, time.Now()); ok {
			h.Metrics.ObserveAISemanticCache(metricIntegrationID, aiSemanticHit)
			w.Header().Set(AICacheHeader, "semantic")
			w.Header().Set(AISimilarityHeader, strconv.FormatFloat(float64(similarity), 'f', 4, 32))
			return cached, nil
		}
		// The answer left the response cache; so does its vector.
		h.semanticIndex.forget(integration.ID, cacheKey)
	}
}

// aiSemanticIndex is the process's semantic index: per integration, the
// unit vectors of cached answers grouped by partition, bounded in entries
// per integration, integrations and bytes, least recently used first out.
type aiSemanticIndex struct {
	mu           sync.Mutex
	lru          list.List
	integrations map[string]*aiSemanticBucket
	bytes        int64
	maxEntries   int
	maxBuckets   int
	maxBytes     int64
}

type aiSemanticBucket struct {
	entries    int
	partitions map[string]map[*list.Element]struct{}
	byKey      map[string]*list.Element
}

type aiSemanticEntry struct {
	integrationID string
	partition     string
	cacheKey      string
	vector        []float32
}

func newAISemanticIndex() *aiSemanticIndex {
	return &aiSemanticIndex{integrations: make(map[string]*aiSemanticBucket), maxEntries: api.OutboundAISemanticCacheEntriesMax,
		maxBuckets: api.OutboundAISemanticCacheIntegrationsMax, maxBytes: api.OutboundAISemanticCacheIndexBytesMax}
}

func (e *aiSemanticEntry) size() int64 {
	return int64(len(e.vector)*4 + len(e.partition) + len(e.cacheKey) + len(e.integrationID) + 96)
}

// nearest returns the most similar entry of a partition at or above the
// threshold, and marks it recently used.
func (x *aiSemanticIndex) nearest(integrationID, partition string, vector []float32, threshold float32) (string, float32, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	bucket := x.integrations[integrationID]
	if bucket == nil {
		return "", 0, false
	}
	var best *list.Element
	bestScore := threshold
	for element := range bucket.partitions[partition] {
		entry := element.Value.(*aiSemanticEntry)
		if len(entry.vector) != len(vector) {
			continue
		}
		var score float32
		for index, value := range vector {
			score += value * entry.vector[index]
		}
		if score >= bestScore {
			best, bestScore = element, score
		}
	}
	if best == nil {
		return "", 0, false
	}
	x.lru.MoveToFront(best)
	return best.Value.(*aiSemanticEntry).cacheKey, min(bestScore, 1), true
}

// add indexes a cached answer's vector, replacing an entry that points at
// the same answer.
func (x *aiSemanticIndex) add(integrationID, partition, cacheKey string, vector []float32) {
	if x == nil || cacheKey == "" || len(vector) == 0 {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	bucket := x.integrations[integrationID]
	if bucket != nil {
		if existing := bucket.byKey[cacheKey]; existing != nil {
			x.remove(existing)
			bucket = x.integrations[integrationID]
		}
	}
	entry := &aiSemanticEntry{integrationID: integrationID, partition: partition, cacheKey: cacheKey, vector: vector}
	if entry.size() > x.maxBytes {
		return
	}
	for bucket == nil && len(x.integrations) >= x.maxBuckets && x.lru.Len() > 0 {
		x.remove(x.lru.Back())
	}
	if bucket == nil {
		bucket = &aiSemanticBucket{partitions: make(map[string]map[*list.Element]struct{}), byKey: make(map[string]*list.Element)}
		x.integrations[integrationID] = bucket
	}
	if bucket.entries >= x.maxEntries {
		for element := x.lru.Back(); element != nil; element = element.Prev() {
			if element.Value.(*aiSemanticEntry).integrationID == integrationID {
				x.remove(element)
				break
			}
		}
		if x.integrations[integrationID] == nil {
			x.integrations[integrationID] = bucket
		}
	}
	for x.bytes+entry.size() > x.maxBytes && x.lru.Len() > 0 {
		x.remove(x.lru.Back())
		if x.integrations[integrationID] == nil {
			x.integrations[integrationID] = bucket
		}
	}
	element := x.lru.PushFront(entry)
	if bucket.partitions[partition] == nil {
		bucket.partitions[partition] = make(map[*list.Element]struct{})
	}
	bucket.partitions[partition][element] = struct{}{}
	bucket.byKey[cacheKey] = element
	bucket.entries++
	x.bytes += entry.size()
}

// forget drops the entry that points at an answer no longer cached.
func (x *aiSemanticIndex) forget(integrationID, cacheKey string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if bucket := x.integrations[integrationID]; bucket != nil {
		if element := bucket.byKey[cacheKey]; element != nil {
			x.remove(element)
		}
	}
}

func (x *aiSemanticIndex) remove(element *list.Element) {
	entry := element.Value.(*aiSemanticEntry)
	x.lru.Remove(element)
	x.bytes -= entry.size()
	bucket := x.integrations[entry.integrationID]
	if bucket == nil {
		return
	}
	delete(bucket.partitions[entry.partition], element)
	if len(bucket.partitions[entry.partition]) == 0 {
		delete(bucket.partitions, entry.partition)
	}
	delete(bucket.byKey, entry.cacheKey)
	if bucket.entries--; bucket.entries == 0 {
		delete(x.integrations, entry.integrationID)
	}
}
