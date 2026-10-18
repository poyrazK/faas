// spec: §4.1
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func failingCacheProxy(h *Handler) http.Handler {
	stream := &failAfterFramesStream{
		frames: []*vmmdpb.ForwardHTTPStreamResponse{
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusOK}}},
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("partial")}},
		},
		err: status.Error(codes.Internal, "guest connection reset"),
	}
	nodes := singleClientLookup{cli: &failAfterFramesClient{stream: stream}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fwdOnceWithEvents(w, r, nodes, h.log, Target{NodeID: "node-1"}, nil)
	})
}

func TestResponseCache_IncompleteOriginResponse(t *testing.T) {
	for _, mode := range []string{"upstream abort", "request canceled"} {
		t.Run(mode, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.setLegacyHot()
			cache := NewResponseCache()
			h.WithResponseCache(cache)
			rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
			seedCacheRule(t, h, b.host, rule)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			h.proxyByNode = func(Target) http.Handler {
				calls++
				if calls == 1 && mode == "upstream abort" {
					return failingCacheProxy(h)
				}
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if calls == 1 {
						_, _ = w.Write([]byte("partial"))
						cancel()
						return
					}
					_, _ = w.Write([]byte("complete"))
				})
			}
			var failure any
			func() {
				defer func() { failure = recover() }()
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://"+b.host+"/catalog", nil)
				h.ServeHTTP(httptest.NewRecorder(), r)
			}()
			abortErr, _ := failure.(error)
			if mode == "upstream abort" && !errors.Is(abortErr, http.ErrAbortHandler) {
				t.Fatalf("upstream abort = %v, want ErrAbortHandler", failure)
			}
			if mode == "request canceled" && failure != nil {
				t.Fatalf("canceled request panicked: %v", failure)
			}
			if cache.Len() != 0 {
				t.Fatal("incomplete origin response populated the cache")
			}
			for range 2 {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+b.host+"/catalog", nil))
				if w.Code != http.StatusOK || w.Body.String() != "complete" {
					t.Fatalf("later response = %d %q, want complete body", w.Code, w.Body.String())
				}
			}
			if calls != 2 || cache.Len() != 1 {
				t.Fatalf("complete response was not cached: origin calls=%d entries=%d", calls, cache.Len())
			}
		})
	}
}

func TestCacheRefresh_IncompleteOriginPreservesStaleEntry(t *testing.T) {
	for _, mode := range []string{"upstream abort", "request canceled"} {
		t.Run(mode, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.setLegacyHot()
			cache := NewResponseCache()
			h.WithResponseCache(cache)
			rule := EdgeRuleCacheResolved{ID: "rule-1", MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}
			request := httptest.NewRequest(http.MethodGet, "http://"+b.host+"/catalog", nil)
			key := CacheKey{AppID: b.app.ID, RuleID: rule.ID, Method: request.Method,
				NormalizedPath: request.URL.Path, VaryHash: computeVaryHash(request, rule.VaryOn)}
			now := time.Now()
			cache.Put(key, http.StatusOK, nil, []byte("previous complete response"), now.Add(-time.Second), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction())
			ctx, cancel := context.WithCancel(request.Context())
			defer cancel()
			h.proxyByNode = func(Target) http.Handler {
				if mode == "upstream abort" {
					return failingCacheProxy(h)
				}
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte("partial"))
					cancel()
				})
			}
			func() {
				defer func() {
					if failure := recover(); failure != nil {
						t.Errorf("background refresh let a transport abort escape: %v", failure)
					}
				}()
				h.refreshCacheFromWarmTarget(ctx, request, b.app, &rule, key)
			}()
			outcome, entry := cache.Get(key)
			if outcome != "stale_if_error_eligible" || entry == nil || string(entry.body) != "previous complete response" {
				t.Fatal("failed refresh replaced the previous complete response")
			}
			h.proxyByNode = func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte("refreshed complete response"))
				})
			}
			h.refreshCacheFromWarmTarget(request.Context(), request, b.app, &rule, key)
			outcome, entry = cache.Get(key)
			if outcome != "fresh" || entry == nil || string(entry.body) != "refreshed complete response" {
				t.Fatal("a subsequent successful refresh did not populate the cache")
			}
		})
	}
}
