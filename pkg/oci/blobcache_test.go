package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDiskBlobCacheHitAndDigestVerification(t *testing.T) {
	root := t.TempDir()
	cache, err := NewDiskBlobCache(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("layer bytes")
	digest := testBlobDigest(payload)
	var fetches atomic.Int32
	var hits atomic.Int32
	var misses atomic.Int32
	cache.SetObserver(BlobCacheObserverFuncs{
		Hit:  func() { hits.Add(1) },
		Miss: func() { misses.Add(1) },
	})
	fetch := func(context.Context) (io.ReadCloser, error) {
		fetches.Add(1)
		return io.NopCloser(strings.NewReader(string(payload))), nil
	}

	r, err := cache.Open(context.Background(), digest, fetch)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("read first blob: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("first blob = %q, want %q", got, payload)
	}

	r, err = cache.Open(context.Background(), digest, func(context.Context) (io.ReadCloser, error) {
		fetches.Add(1)
		return io.NopCloser(strings.NewReader("unexpected network body")), nil
	})
	if err != nil {
		t.Fatalf("cache hit: %v", err)
	}
	got, err = io.ReadAll(r)
	_ = r.Close()
	if err != nil || string(got) != string(payload) {
		t.Fatalf("cache hit body = %q, err=%v; want %q", got, err, payload)
	}
	if gotFetches := fetches.Load(); gotFetches != 1 {
		t.Fatalf("fetches = %d, want 1", gotFetches)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("cache hits = %d, want 1", got)
	}
	if got := misses.Load(); got != 1 {
		t.Fatalf("cache misses = %d, want 1", got)
	}

	badDigest := testBlobDigest([]byte("different"))
	_, err = cache.Open(context.Background(), badDigest, func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(payload))), nil
	})
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestDiskBlobCacheCoalescesConcurrentMisses(t *testing.T) {
	cache, err := NewDiskBlobCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("coalesced")
	digest := testBlobDigest(payload)
	started := make(chan struct{})
	release := make(chan struct{})
	var fetches atomic.Int32
	fetch := func(context.Context) (io.ReadCloser, error) {
		fetches.Add(1)
		close(started)
		<-release
		return io.NopCloser(strings.NewReader(string(payload))), nil
	}

	type result struct {
		body []byte
		err  error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := cache.Open(context.Background(), digest, fetch)
			if err != nil {
				results <- result{err: err}
				return
			}
			body, err := io.ReadAll(r)
			_ = r.Close()
			results <- result{body: body, err: err}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	for res := range results {
		if res.err != nil || string(res.body) != string(payload) {
			t.Fatalf("coalesced result body=%q err=%v", res.body, res.err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
}

func TestDiskBlobCacheEvictsOldestEntries(t *testing.T) {
	root := t.TempDir()
	cache, err := NewDiskBlobCache(root, 5)
	if err != nil {
		t.Fatal(err)
	}
	first := []byte("one")
	second := []byte("two")
	firstDigest := testBlobDigest(first)
	secondDigest := testBlobDigest(second)
	open := func(digest string, body []byte) {
		r, err := cache.Open(context.Background(), digest, func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(body))), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, r)
		_ = r.Close()
	}
	open(firstDigest, first)
	open(secondDigest, second)
	hexDigest := strings.TrimPrefix(firstDigest, digestAlgo)
	if _, err := os.Stat(filepath.Join(root, hexDigest[:2], hexDigest[2:])); err == nil {
		t.Fatal("oldest blob still present after budget eviction")
	}
}

func testBlobDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return digestAlgo + hex.EncodeToString(sum[:])
}
