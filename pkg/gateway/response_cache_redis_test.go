// adr: 211
package gateway

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRedisResponseCacheLiveRoundTripAndInvalidation(t *testing.T) {
	server := miniredis.RunT(t)
	redisURL := "redis://" + server.Addr()

	writer, err := NewRedisResponseCache(context.Background(), redisURL)
	if err != nil {
		t.Fatalf("NewRedisResponseCache(writer): %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	reader, err := NewRedisResponseCache(context.Background(), redisURL)
	if err != nil {
		t.Fatalf("NewRedisResponseCache(reader): %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	now := time.Now()
	entry := &cacheEntry{
		key: CacheKey{
			AppID:          "app-live",
			RuleID:         "rule-live",
			Method:         "GET",
			NormalizedPath: "/products/42",
			Query:          "currency=EUR",
			VaryHash:       [32]byte{1},
		},
		statusCode:      200,
		header:          map[string][]string{"Content-Type": {"application/json"}},
		body:            []byte(`{"id":42}`),
		freshUntil:      now.Add(30 * time.Second),
		revalidateUntil: now.Add(90 * time.Second),
		errorUntil:      now.Add(5 * time.Minute),
		staleUntil:      now.Add(5 * time.Minute),
		ruleAction: &state.EdgeRuleCacheAction{
			MaxAgeSeconds:               30,
			StaleWhileRevalidateSeconds: 60,
			StaleIfErrorSeconds:         300,
			VaryOn:                      []string{"Accept-Language"},
			Methods:                     []string{"GET"},
		},
	}
	if err := writer.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := reader.Get(entry.key)
	if err != nil {
		t.Fatalf("Get from second client: %v", err)
	}
	if got == nil || got.statusCode != entry.statusCode || !reflect.DeepEqual(got.body, entry.body) || !reflect.DeepEqual(got.header, entry.header) {
		t.Fatalf("round trip = %+v, want status/header/body from %+v", got, entry)
	}

	other := *entry
	other.key = entry.key
	other.key.NormalizedPath = "/categories/7"
	if err := writer.Put(&other); err != nil {
		t.Fatalf("Put(other): %v", err)
	}
	if err := reader.InvalidateByAppPath(entry.key.AppID, "/products/*"); err != nil {
		t.Fatalf("InvalidateByAppPath: %v", err)
	}
	if got, err := writer.Get(entry.key); err != nil || got != nil {
		t.Fatalf("purged product = %+v, %v; want nil, nil", got, err)
	}
	if got, err := writer.Get(other.key); err != nil || got == nil {
		t.Fatalf("unmatched category = %+v, %v; want retained entry", got, err)
	}
	if err := reader.InvalidateByApp(entry.key.AppID); err != nil {
		t.Fatalf("InvalidateByApp: %v", err)
	}
	if got, err := writer.Get(other.key); err != nil || got != nil {
		t.Fatalf("app-purged category = %+v, %v; want nil, nil", got, err)
	}
}

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
