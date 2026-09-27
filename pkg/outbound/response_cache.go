package outbound

import (
	"bytes"
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultOutboundCacheEntries = 1024
	defaultOutboundCacheBytes   = 32 << 20
	maxOutboundCacheFills       = 8
	maxOutboundCacheEntryBytes  = 1 << 20
)

type outboundCacheEntry struct {
	key       string
	status    int
	header    http.Header
	body      []byte
	content   int64
	storedAt  time.Time
	expiresAt time.Time
	originAge time.Duration
	bytes     int64
}

// outboundResponseCache is deliberately process-local and bounded. Its key
// includes an HMAC of every forwarded request header so credentials and
// caller-specific representation headers never appear in the cache index.
type outboundResponseCache struct {
	mu        sync.Mutex
	secret    [32]byte
	maxItems  int
	maxBytes  int64
	usedBytes int64
	items     map[string]*list.Element
	lru       list.List
	fillSlots chan struct{}
}

func newOutboundResponseCache() (*outboundResponseCache, error) {
	cache := &outboundResponseCache{
		maxItems:  defaultOutboundCacheEntries,
		maxBytes:  defaultOutboundCacheBytes,
		items:     make(map[string]*list.Element),
		fillSlots: make(chan struct{}, maxOutboundCacheFills),
	}
	if _, err := rand.Read(cache.secret[:]); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *outboundResponseCache) key(integrationID, appID string, policyRevision int64, ttlSeconds int, req *http.Request) string {
	if c == nil || req == nil || req.URL == nil {
		return ""
	}
	mac := hmac.New(sha256.New, c.secret[:])
	writeCacheKeyPart(mac, integrationID)
	writeCacheKeyPart(mac, appID)
	writeCacheKeyPart(mac, strconv.FormatInt(policyRevision, 10))
	writeCacheKeyPart(mac, strconv.Itoa(ttlSeconds))
	writeCacheKeyPart(mac, req.Method)
	writeCacheKeyPart(mac, req.URL.String())

	headerValues := make(map[string][]string, len(req.Header))
	for name, values := range req.Header {
		canonical := http.CanonicalHeaderKey(name)
		headerValues[canonical] = append(headerValues[canonical], values...)
	}
	names := make([]string, 0, len(headerValues))
	for name := range headerValues {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		writeCacheKeyPart(mac, name)
		for _, value := range headerValues[name] {
			writeCacheKeyPart(mac, value)
		}
		writeCacheKeyPart(mac, "")
	}
	return hex.EncodeToString(mac.Sum(nil))
}

type cacheKeyWriter interface {
	Write([]byte) (int, error)
}

func writeCacheKeyPart(w cacheKeyWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = w.Write(length[:])
	_, _ = io.WriteString(w, value)
}

func outboundCacheRequestEligible(req *http.Request, ttlSeconds int) bool {
	if req == nil || req.URL == nil || ttlSeconds <= 0 || req.Method != http.MethodGet || (req.Body != nil && req.Body != http.NoBody) {
		return false
	}
	for _, name := range []string{"Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "If-Range"} {
		if len(req.Header.Values(name)) != 0 {
			return false
		}
	}
	return !requestCacheControlDisablesCaching(req.Header)
}

func requestCacheControlDisablesCaching(header http.Header) bool {
	if strings.Contains(strings.ToLower(header.Get("Pragma")), "no-cache") {
		return true
	}
	directives := parseCacheControl(header.Values("Cache-Control"))
	if _, ok := directives["no-store"]; ok {
		return true
	}
	if _, ok := directives["no-cache"]; ok {
		return true
	}
	if _, ok := directives["only-if-cached"]; ok {
		return true
	}
	if _, ok := directives["max-age"]; ok {
		// This gateway does not revalidate against a caller-specified freshness
		// budget, so conservatively bypass the cache for any request max-age.
		return true
	}
	return false
}

func parseCacheControl(values []string) map[string]string {
	directives := make(map[string]string)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			name, argument, hasArgument := strings.Cut(strings.TrimSpace(part), "=")
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if hasArgument {
				argument = strings.Trim(strings.TrimSpace(argument), `"`)
				directives[name] = argument
			} else {
				directives[name] = ""
			}
		}
	}
	return directives
}

func outboundResponseFreshness(resp *http.Response, configuredTTL time.Duration, now time.Time) (time.Duration, time.Duration, bool) {
	if resp == nil || resp.StatusCode != http.StatusOK || configuredTTL <= 0 || resp.Header == nil || len(resp.Header.Values("Set-Cookie")) != 0 {
		return 0, 0, false
	}
	for _, value := range resp.Header.Values("Vary") {
		for _, name := range strings.Split(value, ",") {
			if strings.TrimSpace(name) == "*" {
				return 0, 0, false
			}
		}
	}
	directives := parseCacheControl(resp.Header.Values("Cache-Control"))
	for _, name := range []string{"private", "no-store", "no-cache"} {
		if _, forbidden := directives[name]; forbidden {
			return 0, 0, false
		}
	}

	remaining := configuredTTL
	for _, name := range []string{"max-age", "s-maxage"} {
		value, present := directives[name]
		if !present {
			continue
		}
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seconds < 0 {
			return 0, 0, false
		}
		providerTTL := remaining
		if seconds < int64(remaining/time.Second) {
			providerTTL = time.Duration(seconds) * time.Second
		}
		if providerTTL < remaining {
			remaining = providerTTL
		}
	}

	var date time.Time
	if value := resp.Header.Get("Date"); value != "" {
		parsed, err := http.ParseTime(value)
		if err != nil {
			return 0, 0, false
		}
		date = parsed
	}
	if value := resp.Header.Get("Expires"); value != "" {
		expires, err := http.ParseTime(value)
		if err != nil {
			return 0, 0, false
		}
		base := now
		if !date.IsZero() {
			base = date
		}
		expiresTTL := expires.Sub(base)
		if expiresTTL < remaining {
			remaining = expiresTTL
		}
	}

	age := time.Duration(0)
	if value := resp.Header.Get("Age"); value != "" {
		seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || seconds < 0 || seconds > int64(configuredTTL/time.Second) {
			return 0, 0, false
		}
		age = time.Duration(seconds) * time.Second
	}
	if !date.IsZero() && now.After(date) && now.Sub(date) > age {
		age = now.Sub(date)
	}
	remaining -= age
	if remaining <= 0 {
		return 0, 0, false
	}
	return remaining, age, true
}

func (c *outboundResponseCache) get(key string, now time.Time) (*http.Response, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.items[key]
	if element == nil {
		return nil, false
	}
	entry := element.Value.(*outboundCacheEntry)
	if !entry.expiresAt.After(now) {
		c.remove(element)
		return nil, false
	}
	c.lru.MoveToFront(element)
	header := entry.header.Clone()
	age := entry.originAge + now.Sub(entry.storedAt)
	if age < 0 {
		age = 0
	}
	header.Set("Age", strconv.FormatInt(int64(age/time.Second), 10))
	body := bytes.Clone(entry.body)
	return &http.Response{
		StatusCode:    entry.status,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: entry.content,
	}, true
}

func (c *outboundResponseCache) storeResponse(key string, resp *http.Response, ttlSeconds int, now time.Time, maxHeaderBytes int64, maxHeaders int, maxResponseBytes int64) {
	if c == nil || key == "" || resp == nil || ttlSeconds <= 0 || !responseHeadersWithinBounds(resp.Header, maxHeaderBytes, maxHeaders) {
		return
	}
	configuredTTL := time.Duration(ttlSeconds) * time.Second
	remaining, originAge, ok := outboundResponseFreshness(resp, configuredTTL, now)
	if !ok {
		return
	}
	if resp.Body == nil {
		resp.Body = http.NoBody
	}
	maxBodyBytes := int64(maxOutboundCacheEntryBytes)
	if maxResponseBytes > 0 && maxResponseBytes < maxBodyBytes {
		maxBodyBytes = maxResponseBytes
	}
	if resp.ContentLength > maxBodyBytes {
		return
	}
	select {
	case c.fillSlots <- struct{}{}:
		defer func() { <-c.fillSlots }()
	default:
		// Do not let concurrent response buffering multiply the per-entry
		// bound into unbounded transient memory use.
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	originalBody := resp.Body
	if err != nil || int64(len(body)) > maxBodyBytes {
		resp.Body = &replayReadCloser{prefix: bytes.NewReader(body), source: originalBody, pendingErr: err}
		return
	}
	_ = originalBody.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	storedAt := time.Now()
	expiresAt := now.Add(remaining)
	if !expiresAt.After(storedAt) {
		return
	}
	entry := &outboundCacheEntry{
		key:       key,
		status:    resp.StatusCode,
		header:    cacheSafeResponseHeaders(resp.Header),
		body:      body,
		content:   resp.ContentLength,
		storedAt:  now,
		expiresAt: expiresAt,
		originAge: originAge,
	}
	entry.bytes = int64(len(key)+len(body)+128) + responseHeaderSize(entry.header)
	c.put(entry)
}

type replayReadCloser struct {
	prefix     *bytes.Reader
	source     io.ReadCloser
	pendingErr error
}

func (r *replayReadCloser) Read(p []byte) (int, error) {
	if r.prefix.Len() > 0 {
		n, err := r.prefix.Read(p)
		if err == io.EOF {
			err = nil
		}
		if r.prefix.Len() == 0 && r.pendingErr != nil {
			replayErr := r.pendingErr
			r.pendingErr = nil
			return n, replayErr
		}
		return n, err
	}
	if r.pendingErr != nil {
		replayErr := r.pendingErr
		r.pendingErr = nil
		return 0, replayErr
	}
	return r.source.Read(p)
}

func (r *replayReadCloser) Close() error { return r.source.Close() }

func cacheSafeResponseHeaders(header http.Header) http.Header {
	out := make(http.Header)
	for name, values := range header {
		if isHopByHop(header, name) {
			continue
		}
		out[name] = append([]string(nil), values...)
	}
	return out
}

func responseHeaderSize(header http.Header) int64 {
	var size int64
	for name, values := range header {
		for _, value := range values {
			size += int64(len(name) + len(value))
		}
	}
	return size
}

func (c *outboundResponseCache) put(entry *outboundCacheEntry) {
	if c == nil || entry == nil || entry.bytes > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.items[entry.key]; existing != nil {
		c.remove(existing)
	}
	for len(c.items) >= c.maxItems || c.usedBytes+entry.bytes > c.maxBytes {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		c.remove(oldest)
	}
	element := c.lru.PushFront(entry)
	c.items[entry.key] = element
	c.usedBytes += entry.bytes
}

func (c *outboundResponseCache) remove(element *list.Element) {
	entry := element.Value.(*outboundCacheEntry)
	delete(c.items, entry.key)
	c.usedBytes -= entry.bytes
	c.lru.Remove(element)
}
