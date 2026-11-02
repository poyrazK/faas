package s3gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/singleflight"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// errCredentialSecret marks a credential whose sealed secret cannot be opened.
var errCredentialSecret = errors.New("s3 credential secret cannot be opened")

// resolvedCredential is an access key's credential, its bucket and its opened
// secret.
type resolvedCredential struct {
	credential state.ObjectS3Credential
	bucket     state.ObjectBucket
	secret     string
	fetchedAt  time.Time
}

// CredentialCache keeps resolved S3 credentials, with their opened secrets,
// for GET and HEAD (ADR-20261030075301 step 4, amendment 1). A resolution is
// reused for ObjectReadAdmissionTTL and, while the database is unreachable,
// for up to ObjectReadAdmissionMaxStale: a revoked credential, a deleted
// bucket or a suspended owner stops reads within 2 s, or 30 s during an
// outage. Writes and signed-URL credentials resolve on every request.
type CredentialCache struct {
	now   func() time.Time
	group singleflight.Group

	mu      sync.Mutex
	entries map[string]resolvedCredential

	resolutions *prometheus.CounterVec
}

func NewCredentialCache(now func() time.Time) *CredentialCache {
	if now == nil {
		now = time.Now
	}
	return &CredentialCache{
		now: now, entries: map[string]resolvedCredential{},
		resolutions: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "s3_gateway_credential_resolutions_total", Help: "S3 credential resolutions for reads by source: cached, refreshed, stale (served while the database is unreachable) or failed."}, []string{"source"}),
	}
}

// Collectors are the cache's metrics, for the gateway's registry.
func (c *CredentialCache) Collectors() []prometheus.Collector {
	return []prometheus.Collector{c.resolutions}
}

// resolveCredential resolves an access key and opens its secret: from the
// credential cache for GET and HEAD when the gateway has one, otherwise from
// the store, so a revoked credential's writes stop at once.
func (h *Handler) resolveCredential(ctx context.Context, method, accessKeyID string) (resolvedCredential, error) {
	load := func(ctx context.Context) (resolvedCredential, error) {
		credential, bucket, err := h.store.ResolveObjectS3Credential(ctx, accessKeyID)
		if err != nil {
			return resolvedCredential{}, err
		}
		secret, err := h.openSecret(credential.SecretSealed)
		if err != nil {
			return resolvedCredential{}, errCredentialSecret
		}
		return resolvedCredential{credential: credential, bucket: bucket, secret: secret}, nil
	}
	if h.credentials == nil || method != http.MethodGet && method != http.MethodHead {
		return load(ctx)
	}
	return h.credentials.resolve(ctx, accessKeyID, load)
}

func (c *CredentialCache) resolve(ctx context.Context, accessKeyID string, load func(context.Context) (resolvedCredential, error)) (resolvedCredential, error) {
	now := c.now()
	c.mu.Lock()
	cached, ok := c.entries[accessKeyID]
	c.mu.Unlock()
	if ok && now.Sub(cached.fetchedAt) < api.ObjectReadAdmissionTTL {
		c.resolutions.WithLabelValues("cached").Inc()
		return cached, nil
	}
	v, err, _ := c.group.Do(accessKeyID, func() (any, error) {
		// A caller that missed just before another refresh finished uses it.
		c.mu.Lock()
		if fresh, ok := c.entries[accessKeyID]; ok && now.Sub(fresh.fetchedAt) < api.ObjectReadAdmissionTTL {
			c.mu.Unlock()
			return fresh, nil
		}
		c.mu.Unlock()
		// A detached read: one caller's cancellation must not fail the others.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectReadAdmissionTimeout)
		defer cancel()
		fresh, err := load(readCtx)
		if err != nil {
			return nil, err
		}
		fresh.fetchedAt = now
		// A signed-URL credential carries its own expiry and issuer checks.
		if fresh.credential.URL == nil {
			c.mu.Lock()
			c.storeLocked(accessKeyID, fresh, now)
			c.mu.Unlock()
		}
		return fresh, nil
	})
	if err != nil {
		// Only an unreachable database serves the last resolution; a key
		// the store no longer resolves stops at once.
		if ok && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, errCredentialSecret) && now.Sub(cached.fetchedAt) < api.ObjectReadAdmissionMaxStale {
			c.resolutions.WithLabelValues("stale").Inc()
			return cached, nil
		}
		if ok {
			c.mu.Lock()
			delete(c.entries, accessKeyID)
			c.mu.Unlock()
		}
		c.resolutions.WithLabelValues("failed").Inc()
		return resolvedCredential{}, err
	}
	c.resolutions.WithLabelValues("refreshed").Inc()
	return v.(resolvedCredential), nil
}

// storeLocked caches a resolution, first dropping expired ones when the cache
// is full; one that still does not fit is used once, not cached.
func (c *CredentialCache) storeLocked(accessKeyID string, r resolvedCredential, now time.Time) {
	if _, ok := c.entries[accessKeyID]; !ok && len(c.entries) >= api.MaxObjectReadAdmissionEntries {
		for key, old := range c.entries {
			if now.Sub(old.fetchedAt) >= api.ObjectReadAdmissionMaxStale {
				delete(c.entries, key)
			}
		}
		if len(c.entries) >= api.MaxObjectReadAdmissionEntries {
			return
		}
	}
	c.entries[accessKeyID] = r
}
