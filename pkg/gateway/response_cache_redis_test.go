// adr: 209
package gateway

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRedisResponseCacheKeyPartitionsApps(t *testing.T) {
	a := responseCacheSampleKey(1)
	b := a
	b.AppID = "another-app"
	if redisResponseCacheKey(a) == redisResponseCacheKey(b) {
		t.Fatal("Redis cache keys collided across apps")
	}
	if got := redisResponseCacheAppPattern(a.AppID); got == redisResponseCacheAppPattern(b.AppID) {
		t.Fatal("Redis app purge patterns collided")
	}
}

func TestRedisResponseCacheRecordRoundTripWindows(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	record := redisResponseCacheRecord{
		Version:         redisResponseCacheVersion,
		Key:             responseCacheSampleKey(2),
		StatusCode:      200,
		Header:          map[string][]string{"Content-Type": {"application/json"}},
		Body:            []byte(`{"ok":true}`),
		FreshUntil:      now.Add(30 * time.Second),
		RevalidateUntil: now.Add(90 * time.Second),
		ErrorUntil:      now.Add(330 * time.Second),
		RuleAction: &state.EdgeRuleCacheAction{
			MaxAgeSeconds:               30,
			StaleWhileRevalidateSeconds: 60,
			StaleIfErrorSeconds:         300,
		},
	}
	entry := record.cacheEntry()
	if !entry.staleUntil.Equal(record.ErrorUntil) {
		t.Fatalf("staleUntil = %s, want later error window %s", entry.staleUntil, record.ErrorUntil)
	}
	entry.header["Content-Type"][0] = "mutated"
	if record.Header["Content-Type"][0] != "application/json" {
		t.Fatal("record header aliased hydrated cache entry")
	}
}
