// adr: 459 — candidate verification cannot read or populate customer caches.
package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

type smokeCacheFixture struct {
	handler        *Handler
	backend        *deploymentSmokeRoutingBackend
	cache          *ResponseCache
	rule           EdgeRuleCacheResolved
	key            CacheKey
	candidateCalls atomic.Int32
}

func newSmokeCacheFixture(t *testing.T) *smokeCacheFixture {
	t.Helper()
	f := &smokeCacheFixture{}
	stable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("stable"))
	}))
	t.Cleanup(stable.Close)
	candidate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.candidateCalls.Add(1)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", "candidate")
		_, _ = w.Write([]byte("candidate"))
	}))
	t.Cleanup(candidate.Close)
	fake := &fakeBackend{
		app:  App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanFree, MaxConcurrency: 1},
		host: "demo.apps.test", upstream: stable.Listener.Addr().String(),
	}
	fake.AddTarget(Target{NodeID: fake.upstream, InstanceID: "stable-1", DeploymentID: "stable"})
	f.backend = &deploymentSmokeRoutingBackend{
		fakeBackend: fake, deploymentID: "candidate", token: "challenge",
		resolved: Target{AppID: fake.app.ID, NodeID: candidate.Listener.Addr().String(), InstanceID: "candidate-1", DeploymentID: "candidate"},
	}
	f.cache = NewResponseCache()
	f.handler = NewHandlerWith(f.backend, NewMetrics(), nil).WithResponseCache(f.cache)
	f.rule = EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/", MaxAgeSeconds: 60, StaleIfErrorSeconds: 300, StaleWhileRevalidateSeconds: 60}
	seedCacheRule(t, f.handler, fake.host, f.rule)
	f.key = CacheKey{AppID: fake.app.ID, RuleID: f.rule.ID, Method: http.MethodGet, NormalizedPath: "/", VaryHash: hostVaryHash(fake.host)}
	return f
}

func (f *smokeCacheFixture) request(smoke bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://demo.apps.test/", nil)
	if smoke {
		r.Header.Set(apihostingreceipt.PlatformSmokeHeader, "1")
		r.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, f.backend.deploymentID)
		r.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, f.backend.token)
	}
	return r
}

func (f *smokeCacheFixture) seed(t *testing.T, state string) {
	t.Helper()
	now := time.Now()
	fresh, revalidate := now.Add(time.Minute), now.Add(2*time.Minute)
	if state != "fresh" {
		fresh = now.Add(-2 * time.Second)
	}
	if state == "stale_if_error_eligible" {
		revalidate = now.Add(-time.Second)
	}
	if !f.cache.PutWithWindowsAndTags(f.key, http.StatusOK, nil, []byte("stable"), fresh, revalidate, now.Add(5*time.Minute), f.rule.toStateEdgeRuleCacheAction(), nil) {
		t.Fatal("could not seed serving response")
	}
	if got, _ := f.cache.Get(f.key); got != state {
		t.Fatalf("seed state = %q, want %q", got, state)
	}
}

func TestDeploymentSmokeCacheIsolation(t *testing.T) {
	for _, state := range []string{"miss", "fresh", "stale_while_revalidate_eligible", "stale_if_error_eligible"} {
		t.Run(state, func(t *testing.T) {
			f := newSmokeCacheFixture(t)
			f.handler.headHeaders.put(f.backend.app.ID, http.Header{"Etag": {"stable"}})
			if state != "miss" {
				f.seed(t, state)
			}
			for range 2 {
				w := httptest.NewRecorder()
				f.handler.ServeHTTP(w, f.request(true))
				if w.Code != http.StatusOK || w.Body.String() != "candidate" {
					t.Fatalf("probe = (%d, %q), want candidate response", w.Code, w.Body.String())
				}
				if w.Header().Get(api.DeploymentIDHeader) != "candidate" || w.Header().Get(apihostingreceipt.ServedResponseHeader) != apihostingreceipt.CandidateResponseProof("candidate", "challenge") {
					t.Fatalf("missing fresh candidate proof: %v", w.Header())
				}
				if got := w.Result().Header.Get("Cache-Control"); got != "no-store" {
					t.Fatalf("probe Cache-Control = %q", got)
				}
			}
			if f.candidateCalls.Load() != 2 {
				t.Fatalf("candidate calls = %d, want both probes at origin", f.candidateCalls.Load())
			}
			if headers, ok := f.handler.headHeaders.get(f.backend.app.ID); !ok || headers.Get("ETag") != "stable" {
				t.Fatalf("candidate replaced customer HEAD headers: %v", headers)
			}
			outcome, entry := f.cache.Get(f.key)
			if state == "miss" {
				if outcome != "" || f.cache.Len() != 0 {
					t.Fatal("candidate response populated customer cache")
				}
			} else if outcome != state || string(entry.body) != "stable" {
				t.Fatalf("serving cache changed: outcome=%q entry=%+v", outcome, entry)
			}
			if state == "fresh" || state == "miss" {
				w := httptest.NewRecorder()
				f.handler.ServeHTTP(w, f.request(false))
				if w.Code != http.StatusOK || w.Body.String() != "stable" || w.Header().Get(apihostingreceipt.ServedResponseHeader) != "" {
					t.Fatalf("customer response = (%d, %q), headers=%v", w.Code, w.Body.String(), w.Header())
				}
				if w.Result().Header.Get("Cache-Control") == "no-store" {
					t.Fatal("candidate policy leaked onto customer response")
				}
			}
		})
	}
}

type smokeSharedResponseCache struct {
	*memorySharedResponseCache
	reads, writes atomic.Int32
}

func (c *smokeSharedResponseCache) Get(key CacheKey) (*cacheEntry, error) {
	c.reads.Add(1)
	return c.memorySharedResponseCache.Get(key)
}

func (c *smokeSharedResponseCache) Put(entry *cacheEntry) error {
	c.writes.Add(1)
	return c.memorySharedResponseCache.Put(entry)
}

func TestDeploymentSmokeBypassesSharedCache(t *testing.T) {
	f := newSmokeCacheFixture(t)
	shared := &smokeSharedResponseCache{memorySharedResponseCache: &memorySharedResponseCache{entries: map[string]*cacheEntry{}}}
	f.cache.WithSharedStore(shared)
	f.seed(t, "fresh")
	shared.reads.Store(0)
	shared.writes.Store(0)
	// A second gateway has an empty local cache and the same shared cache.
	f.cache = NewResponseCache().WithSharedStore(shared)
	f.handler.WithResponseCache(f.cache)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, f.request(true))
	if w.Code != http.StatusOK || w.Body.String() != "candidate" || shared.reads.Load() != 0 || shared.writes.Load() != 0 || f.cache.Len() != 0 {
		t.Fatalf("probe touched shared cache: response=%d %q reads=%d writes=%d local=%d", w.Code, w.Body.String(), shared.reads.Load(), shared.writes.Load(), f.cache.Len())
	}
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, f.request(false))
	if w.Code != http.StatusOK || w.Body.String() != "stable" || shared.reads.Load() != 1 {
		t.Fatalf("customer lost shared cache: response=%d %q reads=%d", w.Code, w.Body.String(), shared.reads.Load())
	}
}

func TestDeploymentSmokeNoStoreAtResponseCommit(t *testing.T) {
	for _, commit := range []string{"explicit status", "implicit status", "flush"} {
		t.Run(commit, func(t *testing.T) {
			w := httptest.NewRecorder()
			rec := newTestStatusRecorder(w)
			rec.deploymentSmoke = true
			rec.Header().Set("Cache-Control", "public, max-age=3600")
			rec.installHeaderOps([]EdgeRuleHeaderOp{{Name: "Cache-Control", Action: "remove"}})
			switch commit {
			case "explicit status":
				rec.WriteHeader(http.StatusBadGateway)
			case "implicit status":
				_, _ = rec.Write([]byte("candidate"))
			case "flush":
				rec.Header().Set("Content-Type", "application/grpc")
				rec.Flush()
			}
			if got := w.Result().Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control at commit = %q", got)
			}
		})
	}
}

func TestDeploymentSmokeNoStoreAfterEarlyHints(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "implicit"}[implicit], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				rec := &statusRecorder{ResponseWriter: w, deploymentSmoke: true}
				rec.WriteHeader(http.StatusEarlyHints)
				// ReverseProxy clears interim headers before the final response.
				rec.Header().Del("Cache-Control")
				rec.Header().Set("Cache-Control", "public, max-age=3600")
				if !implicit {
					rec.WriteHeader(http.StatusOK)
				}
				_, _ = rec.Write([]byte("candidate"))
			}))
			t.Cleanup(server.Close)
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("final response can be cached: %v", response.Header)
			}
		})
	}
}

func TestDeploymentSmokeInvalidChallengeRetainsCacheAndAuth(t *testing.T) {
	for _, kind := range []string{"marker only", "wrong token", "wrong deployment", "wrong app", "expired"} {
		t.Run(kind, func(t *testing.T) {
			f := newSmokeCacheFixture(t)
			f.seed(t, "fresh")
			r := f.request(true)
			switch kind {
			case "marker only":
				r.Header.Del(apihostingreceipt.PlatformSmokeTokenHeader)
				r.Header.Del(apihostingreceipt.PlatformSmokeDeploymentHeader)
			case "wrong token":
				r.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, "forged")
			case "wrong deployment":
				r.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, "another")
			case "wrong app":
				b := NewPGBackend(nil, nil, nil)
				b.AuthorizeDeploymentSmoke("other-app", "candidate", "challenge", time.Now().Add(time.Minute))
				f.handler.backend = b
			case "expired":
				b := NewPGBackend(nil, nil, nil)
				b.AuthorizeDeploymentSmoke(f.backend.app.ID, "candidate", "challenge", time.Now().Add(-time.Second))
				f.handler.backend = b
			}
			w := httptest.NewRecorder()
			rec := newTestStatusRecorder(w)
			if served, _ := f.handler.applyEdgeRuleCache(w, r, f.backend.app, rec); !served || w.Body.String() != "stable" {
				t.Fatalf("invalid challenge bypassed cache: %d %q", w.Code, w.Body.String())
			}
			app := f.backend.app
			app.RequireAuthn = true
			w = httptest.NewRecorder()
			rec = newTestStatusRecorder(w)
			if f.handler.enforceRequireAuthn(w, r, rec, app) {
				t.Fatal("invalid challenge bypassed customer authentication")
			}
		})
	}
}

func TestDeploymentSmokeCannotServeStaleOnWakeFailure(t *testing.T) {
	f := newSmokeCacheFixture(t)
	f.seed(t, "stale_if_error_eligible")
	f.backend.resolved = Target{}
	f.backend.wakeErr = errors.New("candidate wake unavailable")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, f.request(true))
	if w.Code < 500 || w.Body.String() == "stable" || w.Header().Get(apihostingreceipt.ServedResponseHeader) != "" {
		t.Fatalf("failed candidate replayed cache: code=%d body=%q headers=%v", w.Code, w.Body.String(), w.Header())
	}
	if w.Result().Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("failure can be cached: %v", w.Result().Header)
	}
}

func TestDeploymentSmokeBypassesStaleHelpers(t *testing.T) {
	f := newSmokeCacheFixture(t)
	f.seed(t, "stale_if_error_eligible")
	r := f.request(true)
	r = r.WithContext(withCacheRuleContext(r.Context(), &f.rule, f.backend.app.ID, http.MethodGet, "/", "", f.key.VaryHash))
	for name, serve := range map[string]func(http.ResponseWriter, *http.Request, App, *statusRecorder) (bool, string){
		"while waking": f.handler.serveStaleWhileWaking,
		"wake error":   f.handler.tryServeStaleOnWakeError,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			rec := newTestStatusRecorder(w)
			if served, _ := serve(w, r, f.backend.app, rec); served || w.Body.Len() != 0 {
				t.Fatal("candidate probe received stale customer body")
			}
		})
	}
}

type smokeCacheHeaderMatcher struct {
	*cacheOnlyMatcher
	accountID string
}

func (m *smokeCacheHeaderMatcher) MatchHeaders(context.Context, string, string, string) *EdgeRuleHeadersResolved {
	return &EdgeRuleHeadersResolved{AccountID: m.accountID, ResponseHeaders: []EdgeRuleHeaderOp{{Name: "Cache-Control", Action: "set", Value: "public, max-age=7200"}}}
}

func TestDeploymentSmokeNoStoreOverridesGuestAndEdgeHeaders(t *testing.T) {
	for _, proxy := range []string{"http", "bridge"} {
		t.Run(proxy, func(t *testing.T) {
			f := newSmokeCacheFixture(t)
			f.handler.edgeRules = &smokeCacheHeaderMatcher{cacheOnlyMatcher: f.handler.edgeRules.(*cacheOnlyMatcher), accountID: f.backend.app.AccountID}
			if proxy == "bridge" {
				f.handler.proxyByNode = ForwardingReverseProxy(singleClientLookup{cli: &failAfterFramesClient{stream: &failAfterFramesStream{
					err: io.EOF,
					frames: []*vmmdpb.ForwardHTTPStreamResponse{{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
						Status: http.StatusOK, Headers: []*vmmdpb.Header{{Name: "Cache-Control", Value: "public, max-age=3600"}},
					}}}},
				}}}, nil)
			}
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, f.request(true))
			if w.Code != http.StatusOK || w.Result().Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("probe = %d, headers=%v", w.Code, w.Result().Header)
			}
			if f.cache.Len() != 0 {
				t.Fatal("candidate populated customer cache")
			}
		})
	}
}
