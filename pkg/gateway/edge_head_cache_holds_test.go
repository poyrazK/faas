package gateway

import (
	"net/http"
	"testing"
)

// spec: §4.1 (gateway hot-path overhead per proxied request)
//
// TestEdgeHeadHeaderCacheSkipsIdenticalPut pins that put leaves an identical
// entry untouched (no copy per response) and still replaces it whenever the
// stored headers would differ: a changed value, a new or missing allowed
// header. Disallowed headers never enter the comparison.
func TestEdgeHeadHeaderCacheSkipsIdenticalPut(t *testing.T) {
	c := newEdgeHeadHeaderCache()
	base := http.Header{"Content-Type": {"text/plain"}, "Etag": {`"v1"`}, "Set-Cookie": {"s=1"}}
	c.put("app", base)
	stored, ok := c.get("app")
	if !ok || stored.Get("Etag") != `"v1"` || stored.Get("Set-Cookie") != "" {
		t.Fatalf("first put stored %v", stored)
	}
	c.mu.RLock()
	first := c.entries["app"] // the stored map itself; get returns a clone
	c.mu.RUnlock()

	// Same shareable headers (Set-Cookie differs but is never stored): no new entry.
	c.put("app", http.Header{"Content-Type": {"text/plain"}, "Etag": {`"v1"`}, "Set-Cookie": {"s=2"}})
	c.mu.RLock()
	same := c.entries["app"]
	c.mu.RUnlock()
	if !sameHeaderMap(same, first) {
		t.Fatal("identical put replaced the cached entry")
	}

	for name, h := range map[string]http.Header{
		"changed value": {"Content-Type": {"text/plain"}, "Etag": {`"v2"`}},
		"added header":  {"Content-Type": {"text/plain"}, "Etag": {`"v1"`}, "Cache-Control": {"max-age=60"}},
		"missing key":   {"Content-Type": {"text/plain"}},
		"extra value":   {"Content-Type": {"text/plain"}, "Etag": {`"v1"`, `"v1b"`}},
	} {
		c.put("app", base)
		c.put("app", h)
		got, _ := c.get("app")
		for k, v := range h {
			if !edgeHeadHeaderAllowed(k) {
				continue
			}
			if g := got.Values(k); len(g) != len(v) || g[0] != v[0] {
				t.Errorf("%s: %s = %q, want %q", name, k, g, v)
			}
		}
		for k := range got {
			if _, ok := h[k]; !ok {
				t.Errorf("%s: stale header %s kept", name, k)
			}
		}
	}
}

// sameHeaderMap reports whether a and b are the same map (not merely equal).
func sameHeaderMap(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	a["X-Identity-Probe"] = []string{"1"}
	_, shared := b["X-Identity-Probe"]
	delete(a, "X-Identity-Probe")
	return shared
}
