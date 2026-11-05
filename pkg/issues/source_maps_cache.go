package issues

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

type sourceMapCacheEntry struct {
	key   [sha256.Size]byte
	set   *SourceMapSet
	bytes int64
}

// SourceMapCache bounds the decoded manifests retained for reuse. Callers must
// still check the deployment's current publication and digest on every use.
type SourceMapCache struct {
	mu       sync.Mutex
	limit    int
	maxBytes int64
	bytes    int64
	entries  map[[sha256.Size]byte]*list.Element
	recently list.List
}

func NewSourceMapCache(limit int) *SourceMapCache {
	return NewSourceMapCacheWithBudget(limit, 0)
}

// NewSourceMapCacheWithBudget limits both entry count and estimated retained
// memory. A zero byte limit preserves the count-only behavior.
func NewSourceMapCacheWithBudget(limit int, maxBytes int64) *SourceMapCache {
	return &SourceMapCache{limit: max(1, limit), maxBytes: maxBytes, entries: make(map[[sha256.Size]byte]*list.Element)}
}

// retainedBytes conservatively estimates manifests, strings, slice capacity,
// map bookkeeping and decoded entries, not just the uploaded JSON size.
func (s *SourceMapSet) retainedBytes() int64 {
	size := int64(8192 + len(s.Request.Directory) + len(s.Digest))
	for _, a := range s.Request.Manifest.Artifacts {
		size += int64(256 + len(a.GeneratedFile) + len(a.SHA256) + len(a.Payload))
	}
	for name, m := range s.maps {
		size += int64(512 + len(name) + cap(m.sources)*16 + cap(m.names)*16 + cap(m.lines)*24)
		for _, value := range m.sources {
			size += int64(len(value))
		}
		for _, value := range m.names {
			size += int64(len(value))
		}
		for _, row := range m.lines {
			size += int64(cap(row) * 48)
		}
	}
	return size
}

func (c *SourceMapCache) get(key [sha256.Size]byte) *SourceMapSet {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[key]; element != nil {
		c.recently.MoveToFront(element)
		return element.Value.(sourceMapCacheEntry).set
	}
	return nil
}

func (c *SourceMapCache) put(key [sha256.Size]byte, set *SourceMapSet) *SourceMapSet {
	weight := set.retainedBytes()
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[key]; element != nil {
		c.recently.MoveToFront(element)
		return element.Value.(sourceMapCacheEntry).set
	}
	// Do not evict useful entries for an individual set exceeding the budget.
	if c.maxBytes > 0 && weight > c.maxBytes {
		return set
	}
	c.entries[key] = c.recently.PushFront(sourceMapCacheEntry{key: key, set: set, bytes: weight})
	c.bytes += weight
	for c.recently.Len() > c.limit || (c.maxBytes > 0 && c.bytes > c.maxBytes) {
		oldest := c.recently.Back()
		entry := oldest.Value.(sourceMapCacheEntry)
		delete(c.entries, entry.key)
		c.bytes -= entry.bytes
		c.recently.Remove(oldest)
	}
	return set
}

func (c *SourceMapCache) remove(key [sha256.Size]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[key]; element != nil {
		delete(c.entries, key)
		c.bytes -= element.Value.(sourceMapCacheEntry).bytes
		c.recently.Remove(element)
	}
}

func (c *SourceMapCache) Compile(raw []byte) (*SourceMapSet, error) {
	key := sha256.Sum256(raw)
	if set := c.get(key); set != nil {
		return set, nil
	}
	var request api.PublishIssueSourceMapsRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	set, err := CompileSourceMaps(request)
	if err != nil {
		return nil, err
	}
	return c.put(key, set), nil
}

// Remove drops a retired manifest from this process's cache.
func (c *SourceMapCache) Remove(raw []byte) { c.remove(sha256.Sum256(raw)) }

// GetDeployment reuses a compiled set after the caller has checked publication
// status and digest in the database. The key is scoped to app and deployment.
func (c *SourceMapCache) GetDeployment(key string) *SourceMapSet {
	return c.get(sha256.Sum256([]byte("deployment:" + key)))
}

// PutDeployment keeps the same bounded LRU as manifest-based entries.
func (c *SourceMapCache) PutDeployment(key string, set *SourceMapSet) {
	c.put(sha256.Sum256([]byte("deployment:"+key)), set)
}

// RemoveDeployment removes a retired deployment from the cache.
func (c *SourceMapCache) RemoveDeployment(key string) {
	c.remove(sha256.Sum256([]byte("deployment:" + key)))
}
