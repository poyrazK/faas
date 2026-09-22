package edgejwks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// ADR-091 D14: cached keys must expire even if every caller still presents
// the old kid (or no kid), otherwise a removed signing key stays trusted.
func TestCacheRefreshesKnownAndAbsentKeyIDs(t *testing.T) {
	for _, kid := range []string{"old-key", ""} {
		t.Run("kid="+kid, func(t *testing.T) {
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte(`{"keys":[]}`)) // The IdP has revoked its previous key.
			}))
			defer srv.Close()
			cache := NewCache(Options{HTTPClient: srv.Client()}).(*jwksCache)
			if err := cache.Register(srv.URL); err != nil {
				t.Fatal(err)
			}
			entry := cache.byURL[srv.URL]
			entry.set = &jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{KeyID: "old-key"}}}
			entry.lastFetch = time.Now().Add(-2 * cache.refresh)
			set, registered, err := cache.Get(t.Context(), srv.URL, kid)
			if err != nil || !registered {
				t.Fatalf("Get: registered=%v error=%v", registered, err)
			}
			if hits.Load() != 1 || len(set.Keys) != 0 {
				t.Fatalf("expired signing key was retained: fetches=%d keys=%d", hits.Load(), len(set.Keys))
			}
		})
	}
}

func TestCacheWaitingFetchHonorsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			close(started)
		}
		<-release
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	defer srv.Close()
	cache := NewCache(Options{HTTPClient: srv.Client()})
	if err := cache.Register(srv.URL); err != nil {
		t.Fatal(err)
	}
	leader := make(chan error, 1)
	go func() { _, _, err := cache.Get(t.Context(), srv.URL, ""); leader <- err }()
	defer func() {
		close(release)
		select {
		case err := <-leader:
			if err != nil {
				t.Errorf("follower cancellation disrupted the leader fetch: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("leader fetch did not finish after release")
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader fetch did not start")
	}
	for _, alreadyCanceled := range []bool{true, false} {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		if alreadyCanceled {
			cancel()
		}
		follower := make(chan error, 1)
		go func() { _, _, err := cache.Get(ctx, srv.URL, ""); follower <- err }()
		<-ctx.Done()
		select {
		case err := <-follower:
			if !errors.Is(err, ctx.Err()) {
				t.Errorf("waiting caller error = %v, want %v", err, ctx.Err())
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("canceled caller remained blocked on another request's JWKS fetch")
		}
		cancel()
	}
	if hits.Load() != 1 {
		t.Fatal("canceled follower started another network request")
	}
}

func TestCacheRefreshFailureDoesNotServeExpiredKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	cache := NewCache(Options{HTTPClient: srv.Client()}).(*jwksCache)
	if err := cache.Register(srv.URL); err != nil {
		t.Fatal(err)
	}
	entry := cache.byURL[srv.URL]
	entry.set = &jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{KeyID: "old-key"}}}
	entry.lastFetch = time.Now().Add(-2 * cache.refresh)
	set, registered, err := cache.Get(t.Context(), srv.URL, "old-key")
	if err == nil || !registered || set != nil {
		t.Fatalf("expired keys were served during a refresh failure: set=%v registered=%v err=%v", set, registered, err)
	}
}
