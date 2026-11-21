package storage_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

var releaseBaseKey = "base/releases/runner-node22-amd64-" + strings.Repeat("a", 64) + ".ext4"

// slowStoreParent models a remote store whose body streams slower than a
// wake deadline: every body blocks until gate closes, and, like an HTTP
// response body, fails once the context of its Get is done.
type slowStoreParent struct {
	data  map[string]string
	gate  chan struct{}
	gets  atomic.Int32
	fails atomic.Int32 // Gets that fail before returning a body
	mu    sync.Mutex
	open  []context.Context
}

func newSlowStoreParent(data map[string]string) *slowStoreParent {
	return &slowStoreParent{data: data, gate: make(chan struct{})}
}

func (p *slowStoreParent) Put(context.Context, string, io.Reader) error { return nil }
func (p *slowStoreParent) Delete(context.Context, string) error         { return nil }

func (p *slowStoreParent) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	p.gets.Add(1)
	if p.fails.Load() > 0 {
		p.fails.Add(-1)
		return nil, errors.New("503 slow down")
	}
	body, ok := p.data[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	p.mu.Lock()
	p.open = append(p.open, ctx)
	p.mu.Unlock()
	return io.NopCloser(&gatedBody{ctx: ctx, gate: p.gate, r: strings.NewReader(body)}), nil
}

type gatedBody struct {
	ctx  context.Context
	gate chan struct{}
	r    io.Reader
}

func (b *gatedBody) Read(p []byte) (int, error) {
	select {
	case <-b.gate:
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.r.Read(p)
}

// waitForBodies blocks until n parent bodies are open, so a test cancels a
// wake only once its fill is really downloading.
func (p *slowStoreParent) waitForBodies(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		open := len(p.open)
		p.mu.Unlock()
		if open >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("parent bodies open = %d, want %d", open, n)
		}
		time.Sleep(time.Millisecond)
	}
}

type getResult struct {
	body string
	err  error
}

func asyncGet(ctx context.Context, cache *storage.LocalCacheBackend, key string) chan getResult {
	out := make(chan getResult, 1)
	go func() {
		rc, err := cache.Get(ctx, key)
		if err != nil {
			out <- getResult{err: err}
			return
		}
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(rc)
		out <- getResult{body: string(b), err: err}
	}()
	return out
}

func recv(t *testing.T, ch chan getResult) getResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("Get did not return")
		return getResult{}
	}
}

// TestDetachedFillSurvivesCancelledWake is the rc.364 failure: a cold boot's
// wake deadline passes while its runtime-release base is still downloading.
// The cancelled wake must leave a cache entry (or an in-flight fill) for the
// retry instead of discarding the partial download every time.
func TestDetachedFillSurvivesCancelledWake(t *testing.T) {
	const otherKey = "layers/app-1.ext4"
	cases := []struct {
		name string
		key  string
		// retryDuringFill starts the retry before the first fill finishes.
		retryDuringFill bool
		// wantGets is the parent Gets after the cancelled wake and its retry.
		wantGets    int32
		wantCached  bool // entry installed although the first wake gave up
		wantRetryOK bool
	}{
		{name: "retry after fill hits the cache", key: releaseBaseKey, wantGets: 1, wantCached: true, wantRetryOK: true},
		{name: "retry during fill joins it", key: releaseBaseKey, retryDuringFill: true, wantGets: 1, wantCached: true, wantRetryOK: true},
		{name: "mutable key stays attached to its caller", key: otherKey, wantGets: 2, wantCached: false, wantRetryOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := newSlowStoreParent(map[string]string{tc.key: "base-bytes"})
			cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
			if err != nil {
				t.Fatalf("NewLocalCacheBackend: %v", err)
			}

			wake, cancelWake := context.WithCancel(context.Background())
			first := asyncGet(wake, cache, tc.key)
			parent.waitForBodies(t, 1)
			cancelWake() // the wake deadline passes mid-download
			if r := recv(t, first); !errors.Is(r.err, context.Canceled) {
				t.Fatalf("cancelled wake: err = %v, want context.Canceled", r.err)
			}

			var retry chan getResult
			if tc.retryDuringFill {
				retry = asyncGet(context.Background(), cache, tc.key)
			}
			close(parent.gate) // the store finishes streaming
			cache.WaitDeferred()
			if got := cached(t, cache, tc.key); got != tc.wantCached {
				t.Fatalf("cached after cancelled wake = %v, want %v", got, tc.wantCached)
			}
			if retry == nil {
				retry = asyncGet(context.Background(), cache, tc.key)
			}
			r := recv(t, retry)
			if tc.wantRetryOK && (r.err != nil || r.body != "base-bytes") {
				t.Fatalf("retry = %q, %v; want base-bytes", r.body, r.err)
			}
			if got := parent.gets.Load(); got != tc.wantGets {
				t.Fatalf("parent Gets = %d, want %d", got, tc.wantGets)
			}
		})
	}
}

// TestDetachedFillSharedAndBounded covers the singleflight and its bounds:
// concurrent wakes share one download, a failed fill is not remembered, and
// a hung store is cut off by the fill's own timeout rather than a wake's.
func TestDetachedFillSharedAndBounded(t *testing.T) {
	cases := []struct {
		name      string
		waiters   int
		failFirst int32
		timeout   time.Duration
		hang      bool // the store never finishes streaming
		wantErr   []string
		wantGets  int32
		wantCache bool
	}{
		{name: "concurrent wakes share one fill", waiters: 4, wantGets: 1, wantCache: true},
		{name: "failed fill is retried by the next wake", waiters: 1, failFirst: 1, wantErr: []string{"503 slow down"}, wantGets: 2, wantCache: true},
		{name: "hung store is bounded by the fill timeout", waiters: 1, timeout: 50 * time.Millisecond, hang: true, wantErr: []string{"deadline exceeded"}, wantGets: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := newSlowStoreParent(map[string]string{releaseBaseKey: "base-bytes"})
			parent.fails.Store(tc.failFirst)
			cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
			if err != nil {
				t.Fatalf("NewLocalCacheBackend: %v", err)
			}
			if tc.timeout > 0 {
				cache.SetDetachedFillTimeoutForTest(tc.timeout)
			}

			var errs []string
			for _, want := range tc.wantErr {
				r := recv(t, asyncGet(context.Background(), cache, releaseBaseKey))
				if r.err == nil || !strings.Contains(r.err.Error(), want) {
					t.Fatalf("Get err = %v, want %q", r.err, want)
				}
				errs = append(errs, r.err.Error())
			}
			if tc.hang {
				cache.WaitDeferred()
				if cached(t, cache, releaseBaseKey) {
					t.Fatal("hung fill installed an entry")
				}
				if got := parent.gets.Load(); got != tc.wantGets {
					t.Fatalf("parent Gets = %d, want %d", got, tc.wantGets)
				}
				return
			}

			results := make([]chan getResult, tc.waiters)
			for i := range results {
				results[i] = asyncGet(context.Background(), cache, releaseBaseKey)
			}
			parent.waitForBodies(t, 1)
			close(parent.gate)
			for i, ch := range results {
				if r := recv(t, ch); r.err != nil || r.body != "base-bytes" {
					t.Fatalf("waiter %d = %q, %v (earlier errors %v)", i, r.body, r.err, errs)
				}
			}
			cache.WaitDeferred()
			if got := parent.gets.Load(); got != tc.wantGets {
				t.Fatalf("parent Gets = %d, want %d", got, tc.wantGets)
			}
			if got := cached(t, cache, releaseBaseKey); got != tc.wantCache {
				t.Fatalf("cached = %v, want %v", got, tc.wantCache)
			}
		})
	}
}
