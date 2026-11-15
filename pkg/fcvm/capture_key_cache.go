package fcvm

import (
	"context"
	"sync"
	"time"

	"filippo.io/age"
)

const (
	// captureKeyCacheTTL bounds how long a node keeps a sealed snapshot's
	// opened key after secretd last authorized it.
	captureKeyCacheTTL = 10 * time.Minute
	// captureKeyCacheMax bounds the cache; a full cache evicts expired
	// entries first, then the one closest to expiry.
	captureKeyCacheMax = 1024
)

type captureKeyRef struct{ appID, captureID string }

type captureKeyEntry struct {
	identity *age.X25519Identity
	expires  time.Time
}

// captureKeyCache holds sealed snapshot keys a node opened through secretd
// (ADR-20261110210000 slice 3d-3). Only park and warm snapshots (SealedCache)
// are cached: a crash capture restores once per fork and keeps the per-open
// authorization and audit. The zero value is ready to use.
type captureKeyCache struct {
	mu      sync.Mutex
	entries map[captureKeyRef]captureKeyEntry
}

func (c *captureKeyCache) get(ref captureKeyRef, now time.Time) (*age.X25519Identity, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[ref]
	if !ok || !now.Before(e.expires) {
		delete(c.entries, ref)
		return nil, false
	}
	return e.identity, true
}

func (c *captureKeyCache) put(ref captureKeyRef, identity *age.X25519Identity, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[captureKeyRef]captureKeyEntry)
	}
	if _, ok := c.entries[ref]; !ok && len(c.entries) >= captureKeyCacheMax {
		var oldest captureKeyRef
		var oldestAt time.Time
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
				continue
			}
			if oldestAt.IsZero() || e.expires.Before(oldestAt) {
				oldest, oldestAt = k, e.expires
			}
		}
		if len(c.entries) >= captureKeyCacheMax {
			delete(c.entries, oldest)
		}
	}
	c.entries[ref] = captureKeyEntry{identity: identity, expires: now.Add(captureKeyCacheTTL)}
}

// openRemoteCaptureKey opens a sealed capture's key through secretd, from
// the node's cache for a sealed park or warm snapshot.
func (m *Manager) openRemoteCaptureKey(ctx context.Context, remote CaptureKeyOpener, appID string, snap *Snapshot) (*age.X25519Identity, error) {
	if !snap.SealedCache {
		return remote.OpenCaptureKey(ctx, appID, snap.CaptureID)
	}
	ref := captureKeyRef{appID: appID, captureID: snap.CaptureID}
	if identity, ok := m.captureKeys.get(ref, time.Now()); ok {
		return identity, nil
	}
	identity, err := remote.OpenCaptureKey(ctx, appID, snap.CaptureID)
	if err != nil {
		return nil, err
	}
	m.captureKeys.put(ref, identity, time.Now())
	return identity, nil
}
