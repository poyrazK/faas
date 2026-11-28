package outbound

import (
	"bufio"
	"container/list"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Cache hand-over across outboundd restarts and hand-offs
// (ADR-20261128150000). The response cache and the semantic index are
// process memory; without a hand-over every release emptied them. A
// stopping or handing-off generation writes them to one file in the unit's
// private runtime directory and the next generation loads it before it
// serves. The file carries the response cache's key secret: the keys are
// HMACs under it, so entries are found only by a process that holds it.

// cacheSnapshotFile is the one snapshot a directory holds.
const cacheSnapshotFile = "cache.snapshot"

// cacheSnapshotVersion changes whenever the cache key derivation or the
// snapshot's shape does; a loader ignores a snapshot of another version.
const cacheSnapshotVersion = 2

// cacheSnapshotHeader opens the file. Responses and then Semantic entries
// follow it one gob value each, least recently used first, so loading them
// in order rebuilds the LRU. Writing them one at a time keeps the encoder
// from holding a second copy of the whole cache in memory.
type cacheSnapshotHeader struct {
	Version   int
	WrittenAt time.Time
	Secret    [32]byte
	Responses int
	Semantic  int
}

type cacheSnapshotResponse struct {
	Key       string
	Status    int
	Header    http.Header
	Trailer   http.Header
	Body      []byte
	Content   int64
	StoredAt  time.Time
	ExpiresAt time.Time
	OriginAge time.Duration
	Bytes     int64
}

type cacheSnapshotSemantic struct {
	IntegrationID string
	Partition     string
	CacheKey      string
	Vector        []float32
}

// WriteCacheSnapshot writes the handler's caches to dir, replacing an
// earlier snapshot. An empty dir writes nothing.
func (h *Handler) WriteCacheSnapshot(dir string, now time.Time) (int, error) {
	if dir == "" || h == nil || h.responseCache == nil {
		return 0, nil
	}
	header := cacheSnapshotHeader{Version: cacheSnapshotVersion, WrittenAt: now}
	var responses []cacheSnapshotResponse
	var semantic []cacheSnapshotSemantic
	header.Secret, responses = h.responseCache.export(now)
	if h.semanticIndex != nil {
		semantic = h.semanticIndex.export()
	}
	header.Responses, header.Semantic = len(responses), len(semantic)
	tmp, err := os.CreateTemp(dir, cacheSnapshotFile+".*")
	if err != nil {
		return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
	}
	if err := encodeCacheSnapshot(tmp, &header, responses, semantic); err != nil {
		_ = tmp.Close()
		return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, cacheSnapshotFile)); err != nil {
		return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
	}
	return len(responses), nil
}

func encodeCacheSnapshot(w io.Writer, header *cacheSnapshotHeader, responses []cacheSnapshotResponse, semantic []cacheSnapshotSemantic) error {
	buffered := bufio.NewWriter(w)
	enc := gob.NewEncoder(buffered)
	if err := enc.Encode(header); err != nil {
		return err
	}
	for i := range responses {
		if err := enc.Encode(&responses[i]); err != nil {
			return err
		}
	}
	for i := range semantic {
		if err := enc.Encode(&semantic[i]); err != nil {
			return err
		}
	}
	return buffered.Flush()
}

// DropCaches empties the response cache and the semantic index. A handed-off
// generation calls it once its successor serves: the successor loaded the
// same entries, and holding both copies through a drain of up to
// api.HandoffDrainMaxSeconds would double the unit's cache memory.
func (h *Handler) DropCaches() {
	if h == nil {
		return
	}
	if h.responseCache != nil {
		h.responseCache.clear()
	}
	if h.semanticIndex != nil {
		h.semanticIndex.clear()
	}
}

// RemoveCacheSnapshot deletes dir's snapshot, as a failed hand-off does: the
// generation that wrote it keeps serving and a later stop writes a new one.
func RemoveCacheSnapshot(dir string) {
	if dir != "" {
		_ = os.Remove(filepath.Join(dir, cacheSnapshotFile))
	}
}

// LoadCacheSnapshot fills the handler's caches from dir's snapshot and
// deletes it. A snapshot older than api.OutboundCacheSnapshotMaxAgeSeconds,
// of another version or unreadable is deleted and ignored; expired entries
// are skipped. It reports the responses loaded.
func (h *Handler) LoadCacheSnapshot(dir string, now time.Time) (int, error) {
	if dir == "" || h == nil || h.responseCache == nil {
		return 0, nil
	}
	path := filepath.Join(dir, cacheSnapshotFile)
	// The unit's own 0700 runtime directory and a fixed file name, never a
	// customer path.
	file, err := os.Open(path) //nolint:forbidigo // daemon-owned runtime dir, fixed name
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
	}
	defer func() { _ = os.Remove(path) }()
	defer func() { _ = file.Close() }()
	dec := gob.NewDecoder(bufio.NewReader(file))
	var header cacheSnapshotHeader
	if err := dec.Decode(&header); err != nil {
		return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
	}
	maxAge := time.Duration(api.OutboundCacheSnapshotMaxAgeSeconds) * time.Second
	if header.Version != cacheSnapshotVersion || now.Sub(header.WrittenAt) > maxAge || header.WrittenAt.After(now.Add(time.Minute)) ||
		header.Responses < 0 || header.Semantic < 0 {
		return 0, nil
	}
	responses := make([]cacheSnapshotResponse, 0, min(header.Responses, defaultOutboundCacheEntries))
	for range header.Responses {
		var e cacheSnapshotResponse
		if err := dec.Decode(&e); err != nil {
			return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
		}
		responses = append(responses, e)
	}
	semantic := make([]cacheSnapshotSemantic, 0, min(header.Semantic, api.OutboundAISemanticCacheEntriesMax))
	for range header.Semantic {
		var e cacheSnapshotSemantic
		if err := dec.Decode(&e); err != nil {
			return 0, fmt.Errorf("outbound: cache snapshot: %w", err)
		}
		semantic = append(semantic, e)
	}
	loaded := h.responseCache.load(header.Secret, responses, now)
	if h.semanticIndex != nil {
		for _, entry := range semantic {
			if h.responseCache.has(entry.CacheKey) {
				h.semanticIndex.add(entry.IntegrationID, entry.Partition, entry.CacheKey, entry.Vector)
			}
		}
	}
	return loaded, nil
}

// export copies the live entries, least recently used first, and the key
// secret.
func (c *outboundResponseCache) export(now time.Time) ([32]byte, []cacheSnapshotResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := make([]cacheSnapshotResponse, 0, len(c.items))
	for element := c.lru.Back(); element != nil; element = element.Prev() {
		e := element.Value.(*outboundCacheEntry)
		if !now.Before(e.expiresAt) {
			continue
		}
		entries = append(entries, cacheSnapshotResponse{Key: e.key, Status: e.status, Header: e.header, Trailer: e.trailer,
			Body: e.body, Content: e.content, StoredAt: e.storedAt, ExpiresAt: e.expiresAt, OriginAge: e.originAge, Bytes: e.bytes})
	}
	return c.secret, entries
}

// load adopts a snapshot's secret and entries. Entries already cached under
// this process's own secret would no longer be found, so it drops them; a
// loader runs before the process serves, when there are none.
func (c *outboundResponseCache) load(secret [32]byte, entries []cacheSnapshotResponse, now time.Time) int {
	c.mu.Lock()
	c.secret = secret
	c.items = make(map[string]*list.Element)
	c.lru.Init()
	c.usedBytes = 0
	c.mu.Unlock()
	for _, e := range entries {
		if !now.Before(e.ExpiresAt) || e.Key == "" {
			continue
		}
		c.put(&outboundCacheEntry{key: e.Key, status: e.Status, header: e.Header, trailer: e.Trailer, body: e.Body,
			content: e.Content, storedAt: e.StoredAt, expiresAt: e.ExpiresAt, originAge: e.OriginAge, bytes: e.Bytes})
	}
	// put drops an entry over this binary's limits and evicts to fit them,
	// so count what the cache holds rather than what was offered.
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// clear empties the cache, as a handed-off generation does: the successor
// holds the same entries and this one serves no new requests.
func (c *outboundResponseCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element)
	c.lru.Init()
	c.usedBytes = 0
}

func (c *outboundResponseCache) has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[key]
	return ok
}

// export copies the index entries, least recently used first.
func (x *aiSemanticIndex) export() []cacheSnapshotSemantic {
	x.mu.Lock()
	defer x.mu.Unlock()
	entries := make([]cacheSnapshotSemantic, 0, x.lru.Len())
	for element := x.lru.Back(); element != nil; element = element.Prev() {
		e := element.Value.(*aiSemanticEntry)
		entries = append(entries, cacheSnapshotSemantic{IntegrationID: e.integrationID, Partition: e.partition, CacheKey: e.cacheKey, Vector: e.vector})
	}
	return entries
}

// clear empties the index (DropCaches).
func (x *aiSemanticIndex) clear() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.lru.Init()
	x.integrations = make(map[string]*aiSemanticBucket)
	x.bytes = 0
}
