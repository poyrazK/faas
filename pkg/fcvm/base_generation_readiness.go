// adr: 20261022184531 — vmmd reports whether it can adopt its configured
// base generations, so schedd can mark a node that cannot degraded.
package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// BaseGenerationReadiness is the verdict on one configured runtime base.
type BaseGenerationReadiness struct {
	BaseKey    string
	Generation string
	// Adoptable is false only when the node proved it cannot boot
	// Generation: its cache does not hold it and the canonical scan
	// sidecar names another generation.
	Adoptable bool
	Reason    string
}

type baseGenerationReadinessCache struct {
	mu        sync.Mutex
	report    []BaseGenerationReadiness
	checkedAt time.Time
	running   bool
}

// BaseGenerationReadiness returns the latest verdicts without waiting. When
// they are older than api.BaseGenerationReadinessCheckIntervalSeconds it
// starts one background check; the first call after startup returns none.
// Ping calls it, and Ping must stay a cheap liveness probe.
func (m *Manager) BaseGenerationReadiness(ctx context.Context) []BaseGenerationReadiness {
	if len(m.baseGenerations) == 0 || m.storage == nil {
		return nil
	}
	c := &m.baseReadiness
	c.mu.Lock()
	report := slices.Clone(c.report)
	start := !c.running && time.Since(c.checkedAt) >= api.BaseGenerationReadinessCheckIntervalSeconds*time.Second
	if start {
		c.running = true
	}
	c.mu.Unlock()
	if start {
		go func() {
			checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.BaseGenerationReadinessCheckTimeoutSeconds*time.Second)
			defer cancel()
			next := m.checkBaseGenerations(checkCtx)
			c.mu.Lock()
			c.report, c.checkedAt, c.running = next, time.Now(), false
			c.mu.Unlock()
		}()
	}
	return report
}

// checkBaseGenerations decides every configured base, ordered by key.
func (m *Manager) checkBaseGenerations(ctx context.Context) []BaseGenerationReadiness {
	keys := make([]string, 0, len(m.baseGenerations))
	for key := range m.baseGenerations {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	report := make([]BaseGenerationReadiness, 0, len(keys))
	for _, baseKey := range keys {
		expected := m.baseGenerations[baseKey]
		adoptable, reason := m.baseGenerationAdoptable(ctx, baseKey, expected)
		report = append(report, BaseGenerationReadiness{BaseKey: baseKey, Generation: expected, Adoptable: adoptable, Reason: reason})
	}
	return report
}

// baseGenerationAdoptable mirrors ensureBaseGeneration's decision without
// its side effects: it neither refreshes the cache nor marks a generation,
// so a node still serving its cached generation is never disturbed. Anything
// it cannot read counts as adoptable; the wake path decides on its own
// evidence and schedd re-places a refused wake.
func (m *Manager) baseGenerationAdoptable(ctx context.Context, baseKey, expected string) (bool, string) {
	if expected == "" {
		return true, ""
	}
	baseCache, baseCacheKey, err := storage.CacheBackendForKey(m.storage, baseKey)
	if err != nil || baseCache == nil {
		return true, ""
	}
	if generation, ok, readErr := baseCache.CachedGeneration(baseCacheKey); readErr == nil && ok && generation == expected {
		return true, ""
	}
	scanCache, scanCacheKey, err := storage.CacheBackendForKey(m.storage, wire.ScanKeyForBaseKey(baseKey))
	if err != nil || scanCache == nil {
		return true, ""
	}
	rc, err := scanCache.ReadParent(ctx, scanCacheKey)
	if err != nil {
		return true, ""
	}
	defer func() { _ = rc.Close() }()
	var scan baseScanSidecar
	if err := json.NewDecoder(rc).Decode(&scan); err != nil || scan.Image == "" {
		return true, ""
	}
	if scanNamesConfiguredBase(scan.Image, expected) {
		return true, ""
	}
	return false, fmt.Sprintf("shared storage holds generation %s and this node's cache does not hold the configured one", scan.Image)
}
