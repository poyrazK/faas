// adr: 211
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

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
		servedDeploymentID: "deployment-live",
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
		tags:            []string{"product:42", "collection-winter"},
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
	retryRedisResponseCacheIO(t, func() error { return writer.Put(entry) })
	var got *cacheEntry
	retryRedisResponseCacheIO(t, func() error {
		got, err = reader.Get(entry.key)
		return err
	})
	if got == nil || got.statusCode != entry.statusCode || got.servedDeploymentID != entry.servedDeploymentID || !reflect.DeepEqual(got.body, entry.body) || !reflect.DeepEqual(got.header, entry.header) || !reflect.DeepEqual(got.tags, entry.tags) {
		t.Fatalf("round trip = %+v, want status/header/body from %+v", got, entry)
	}

	other := *entry
	other.key = entry.key
	other.key.NormalizedPath = "/categories/7"
	retryRedisResponseCacheIO(t, func() error { return writer.Put(&other) })
	if err := reader.InvalidateByAppPath(entry.key.AppID, "/products/*"); err != nil {
		t.Fatalf("InvalidateByAppPath: %v", err)
	}
	retryRedisResponseCacheIO(t, func() error {
		got, err = writer.Get(entry.key)
		return err
	})
	if got != nil {
		t.Fatalf("purged product = %+v; want nil", got)
	}
	retryRedisResponseCacheIO(t, func() error {
		got, err = writer.Get(other.key)
		return err
	})
	if got == nil {
		t.Fatal("unmatched category was purged; want retained entry")
	}
	if err := reader.InvalidateByApp(entry.key.AppID); err != nil {
		t.Fatalf("InvalidateByApp: %v", err)
	}
	retryRedisResponseCacheIO(t, func() error {
		got, err = writer.Get(other.key)
		return err
	})
	if got != nil {
		t.Fatalf("app-purged category = %+v; want nil", got)
	}
}

// adr: 122
func TestRedisResponseCacheInvalidateByAppTag(t *testing.T) {
	server := miniredis.RunT(t)
	cache, err := NewRedisResponseCache(context.Background(), "redis://"+server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	now := time.Now()
	keys := []CacheKey{
		{AppID: "app-1", RuleID: "one"},
		{AppID: "app-1", RuleID: "two"},
		{AppID: "app-2", RuleID: "one"},
	}
	for i, key := range keys {
		tags := []string{"other"}
		if i != 1 {
			tags = []string{"product:42"}
		}
		entry := &cacheEntry{key: key, body: []byte("ok"), tags: tags, freshUntil: now.Add(time.Minute), staleUntil: now.Add(time.Minute)}
		retryRedisResponseCacheIO(t, func() error { return cache.Put(entry) })
	}
	if err := cache.InvalidateByAppTag("app-1", "PRODUCT:42"); err != nil {
		t.Fatal(err)
	}
	for i, key := range keys {
		var entry *cacheEntry
		retryRedisResponseCacheIO(t, func() error {
			entry, err = cache.Get(key)
			return err
		})
		if (entry != nil) != (i != 0) {
			t.Errorf("Get(%d) = %v; want present=%v", i, entry, i != 0)
		}
	}
}

// The optional L2 can time out on a busy race-test runner. Retry only those
// transient failures while testing contents and invalidation; a successful
// miss, corrupt record or other error must still fail its original assertion.
func retryRedisResponseCacheIO(t *testing.T, operation func() error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := operation()
		if err == nil {
			return
		}
		var networkErr net.Error
		if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &networkErr) && networkErr.Timeout()) {
			t.Fatalf("Redis operation: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Redis operation did not recover from timeout: %v", err)
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// adr: 214
func TestRedisResponseCacheSocketDeadlinesRespectOperationBudget(t *testing.T) {
	server := miniredis.RunT(t)
	opts, err := redisResponseCacheOptions("redis://" + server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	// Make the socket timeout longer than both public operation budgets so
	// the probe detects a lost context deadline without measuring host speed.
	opts.ReadTimeout = time.Minute
	opts.WriteTimeout = time.Minute
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	probe := &redisResponseCacheDeadlineProbe{}
	client.AddHook(probe)
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	cache := &RedisResponseCache{client: client}
	entry := &cacheEntry{key: CacheKey{AppID: "deadline-app", NormalizedPath: "/products/42"}, staleUntil: time.Now().Add(time.Minute)}
	retryRedisResponseCacheIO(t, func() error { return cache.Put(entry) })
	var got *cacheEntry
	retryRedisResponseCacheIO(t, func() error {
		got, err = cache.Get(entry.key)
		return err
	})
	if got == nil {
		t.Fatal("Get missed the stored entry")
	}
	if err := cache.InvalidateByAppPath(entry.key.AppID, "/products/*"); err != nil {
		t.Fatal(err)
	}
	if reads, writes := probe.counts(); reads == 0 || writes == 0 {
		t.Fatalf("did not exercise socket deadlines: reads=%d writes=%d", reads, writes)
	}
}

type redisResponseCacheDeadlineProbe struct {
	mu       sync.Mutex
	deadline time.Time
	reads    int
	writes   int
}

func (p *redisResponseCacheDeadlineProbe) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := next(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &redisResponseCacheDeadlineConn{Conn: conn, probe: p}, nil
	}
}

func (p *redisResponseCacheDeadlineProbe) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		switch cmd.Name() {
		case "get", "set", "scan", "unlink":
		default:
			return next(ctx, cmd)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("Redis cache operation is missing its deadline")
		}
		p.mu.Lock()
		p.deadline = deadline
		p.mu.Unlock()
		defer func() {
			p.mu.Lock()
			p.deadline = time.Time{}
			p.mu.Unlock()
		}()
		return next(ctx, cmd)
	}
}

func (p *redisResponseCacheDeadlineProbe) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (p *redisResponseCacheDeadlineProbe) checkDeadline(deadline time.Time, read bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.deadline.IsZero() {
		return nil
	}
	if deadline.IsZero() || deadline.After(p.deadline) {
		return fmt.Errorf("socket deadline %s exceeds cache operation deadline %s", deadline, p.deadline)
	}
	if read {
		p.reads++
	} else {
		p.writes++
	}
	return nil
}

func (p *redisResponseCacheDeadlineProbe) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads, p.writes
}

type redisResponseCacheDeadlineConn struct {
	net.Conn
	probe *redisResponseCacheDeadlineProbe
}

func (c *redisResponseCacheDeadlineConn) SetReadDeadline(deadline time.Time) error {
	if err := c.probe.checkDeadline(deadline, true); err != nil {
		return err
	}
	return c.Conn.SetReadDeadline(deadline)
}

func (c *redisResponseCacheDeadlineConn) SetWriteDeadline(deadline time.Time) error {
	if err := c.probe.checkDeadline(deadline, false); err != nil {
		return err
	}
	return c.Conn.SetWriteDeadline(deadline)
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
