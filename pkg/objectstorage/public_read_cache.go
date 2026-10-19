package objectstorage

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// The public-read matcher runs in front of every GET and HEAD an app hostname
// receives, on the control-plane edge and on every compute edge, before the
// gateway's rate limits. Resolving a host costs two or three control-plane
// queries (custom domain, app, buckets), so without reuse every app request,
// and every request to a made-up hostname, became Postgres load. The mounts
// of a host are reused for a few seconds: a bucket made public, private or
// deleted takes effect within publicMountTTL, far inside the one-year
// Cache-Control the served objects already carry. Lookup errors are never
// cached.
const (
	publicMountTTL      = 5 * time.Second
	publicMountMaxHosts = 16384
)

type publicMountEntry struct {
	// buckets holds only the host's servable mounts: ready, public, with a
	// valid serve_at. Empty means the host has none, or names no app.
	buckets []state.ObjectBucket
	expires time.Time
}

type publicMountCache struct {
	mu    sync.Mutex
	hosts map[string]publicMountEntry
}

func newPublicMountCache() *publicMountCache {
	return &publicMountCache{hosts: make(map[string]publicMountEntry)}
}

func (c *publicMountCache) get(host string, now time.Time) ([]state.ObjectBucket, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.hosts[host]
	if !ok || !now.Before(entry.expires) {
		return nil, false
	}
	return entry.buckets, true
}

func (c *publicMountCache) put(host string, buckets []state.ObjectBucket, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.hosts[host]; !ok && len(c.hosts) >= publicMountMaxHosts {
		for name, entry := range c.hosts {
			if !now.Before(entry.expires) {
				delete(c.hosts, name)
			}
		}
		// Still full of live entries: drop one so the map stays bounded.
		for name := range c.hosts {
			if len(c.hosts) < publicMountMaxHosts {
				break
			}
			delete(c.hosts, name)
		}
	}
	c.hosts[host] = publicMountEntry{buckets: buckets, expires: now.Add(publicMountTTL)}
}

// servableMounts keeps the buckets the public edge may serve.
func servableMounts(buckets []state.ObjectBucket) []state.ObjectBucket {
	var out []state.ObjectBucket
	for _, bucket := range buckets {
		if bucket.State == "ready" && bucket.PublicRead && ValidPublicReadPath(true, bucket.ServeAt) {
			out = append(out, bucket)
		}
	}
	return out
}
