// adr: 215 — idempotent async replays preserve their admitted request intent.
// adr: 122 — origin cache privacy directives apply to capture and replay.
package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAsyncIntentReplayAtBacklogCap(t *testing.T) {
	for _, cap := range []struct {
		name    string
		pending int
		bytes   int64
	}{{name: "depth", pending: 1}, {name: "bytes", bytes: 7}} {
		t.Run(cap.name, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "quota-replay@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "quota-replay", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			req := AsyncRouteRequest{AppID: app.ID, AccountID: account.ID, Method: "POST", Path: "/work", Payload: []byte(`{"v":1}`), IdempotencyKey: "original", MaxPending: cap.pending, MaxBacklogBytes: cap.bytes}
			first, err := EnqueueAsyncRoute(ctx, store, req)
			if err != nil {
				t.Fatal(err)
			}
			other := req
			other.IdempotencyKey = "another"
			if _, err := EnqueueAsyncRoute(ctx, store, other); !errors.Is(err, state.ErrQuotaExceeded) {
				t.Fatalf("queue cap did not refuse new work: %v", err)
			}
			if replay, err := EnqueueAsyncRoute(ctx, store, req); err != nil || replay != first {
				t.Fatalf("full queue lost the original receipt: %+v, %v", replay, err)
			}
			req.Payload = []byte(`{"v":2}`)
			if _, err := EnqueueAsyncRoute(ctx, store, req); !errors.Is(err, ErrAsyncRouteIdempotencyConflict) {
				t.Fatalf("full queue accepted changed intent: %v", err)
			}
		})
	}
}

func TestRegressionAsyncChangedIntent(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "root-cause@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "root-cause", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"body", "path", "query", "method", "header"} {
		t.Run(field, func(t *testing.T) {
			req := AsyncRouteRequest{AppID: app.ID, AccountID: account.ID, Method: "POST", Path: "/work?x=1", Payload: []byte(`{"v":1}`), Headers: map[string]string{"X-Mode": "a"}, IdempotencyKey: field}
			first, err := EnqueueAsyncRoute(ctx, store, req)
			if err != nil {
				t.Fatal(err)
			}
			same, err := EnqueueAsyncRoute(ctx, store, req)
			if err != nil || first.ID != same.ID {
				t.Fatalf("identical replay control: %+v, %v", same, err)
			}
			switch field {
			case "body":
				req.Payload = []byte(`{"v":2}`)
			case "path":
				req.Path = "/other?x=1"
			case "query":
				req.Path = "/work?x=2"
			case "method":
				req.Method = "PUT"
			case "header":
				req.Headers = map[string]string{"X-Mode": "b"}
			}
			changed, err := EnqueueAsyncRoute(ctx, store, req)
			if err == nil {
				t.Errorf("changed %s silently accepted original receipt: original=%s returned=%s", field, first.ID, changed.ID)
			}
			original, err := store.InvocationByID(ctx, first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if string(original.Payload) != `{"v":1}` || original.Path != "/work?x=1" || original.Method != "POST" {
				t.Fatalf("original receipt mutated: %+v", original)
			}
		})
	}
}

func TestRegressionCacheNoCache(t *testing.T) {
	for _, directive := range []string{"no-cache", "No-Cache, max-age=60", "no-store", "private"} {
		t.Run(directive, func(t *testing.T) {
			now := time.Now()
			cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
			rule := EdgeRuleCacheResolved{ID: "test", MaxAgeSeconds: 60}
			key := CacheKey{AppID: "app", RuleID: rule.ID, Method: "GET", NormalizedPath: "/data"}
			rec := newTestStatusRecorder(httptest.NewRecorder())
			writer := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
			writer.Header().Set("Cache-Control", directive)
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("origin-version-1"))
			stored := writer.finishCacheCapture(cache, key, now)
			outcome, _ := cache.Get(key)
			if stored || outcome == "fresh" {
				t.Errorf("response requiring validation stored=%v cache_outcome=%s; no validation path ran", stored, outcome)
			}
		})
	}
}

func TestAsyncIntentCanonicalReplayAndTransportChanges(t *testing.T) {
	a := state.Invocation{Method: "POST", Path: "/work", Payload: []byte(`{"n":9007199254740993,"value":1.0}`), Headers: []byte(`{"X-Mode":"a","Cf-Ray":"old","Traceparent":"old"}`)}
	b := a
	b.Payload = []byte(`{ "value":1e0, "n":9007199254740993 }`)
	b.Headers = []byte(`{"x-mode":"a","CF-Ray":"new","traceparent":"new"}`)
	if !sameAsyncRouteIntent(a, b) {
		t.Fatal("equivalent retry was rejected")
	}
	b.Payload = []byte(`{"value":1,"n":9007199254740992}`)
	if sameAsyncRouteIntent(a, b) {
		t.Fatal("integers above 2^53 were rounded together")
	}
}

func TestAsyncIdempotencyConflictResponse(t *testing.T) {
	h := &Handler{asyncRoutes: &recordingAsyncRouteEnqueuer{err: ErrAsyncRouteIdempotencyConflict}}
	r := httptest.NewRequest("POST", "http://api.example.com/work", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.applyEdgeRuleAsync(w, r, App{ID: "app", AccountID: "account", Plan: api.PlanHobby, RequestInvocationsEnabled: true}, &EdgeRuleAsyncResolved{ID: "async"})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "idempotency_conflict") {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
}

func TestCachePrivacyDirectiveParsing(t *testing.T) {
	for _, tc := range []struct {
		value string
		deny  bool
	}{
		{`public, max-age=60`, false},
		{`extension="public, private, no-cache", max-age=60`, false},
		{`extension="escaped\",comma", no-cache="ETag"`, true},
		{`max-age=60, NO-CACHE="Set-Cookie"`, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := responseForbidsSharedCaching(http.Header{"Cache-Control": {tc.value}}); got != tc.deny {
				t.Fatalf("privacy veto=%v, want %v", got, tc.deny)
			}
		})
	}
}

func TestCachedPrivacyVetoAppliesToFreshAndStaleEntries(t *testing.T) {
	for _, directive := range []string{"no-cache", "private", "no-store"} {
		for _, path := range []string{"fresh", "wake_error", "while_waking"} {
			t.Run(directive+"/"+path, func(t *testing.T) {
				now := time.Now()
				cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
				h, _, _ := newTestHandler(t)
				h.WithResponseCache(cache)
				rule := EdgeRuleCacheResolved{ID: "privacy", PathGlob: "/catalog", MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}
				seedCacheRule(t, h, "jane-api.apps.dom", rule)
				app := App{ID: "app-1", Plan: api.PlanPro}
				key := CacheKey{AppID: app.ID, RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}
				freshUntil := now.Add(-time.Second)
				if path == "fresh" {
					freshUntil = now.Add(time.Minute)
				}
				// Model entries created before the writer-side privacy fix.
				cache.Put(key, http.StatusOK, http.Header{"Cache-Control": {directive}}, []byte("must-revalidate"), freshUntil, now.Add(5*time.Minute), rule.toStateEdgeRuleCacheAction())
				req := httptest.NewRequest("GET", "http://jane-api.apps.dom/catalog", nil)
				req = req.WithContext(withCacheRuleContext(req.Context(), &rule, app.ID, "GET", "/catalog", "", hashStable("")))
				w := httptest.NewRecorder()
				rec := newTestStatusRecorder(w)
				var served bool
				switch path {
				case "fresh":
					served, _ = h.applyEdgeRuleCache(w, req, app, rec)
				case "wake_error":
					served, _ = h.tryServeStaleOnWakeError(w, req, app, rec)
				case "while_waking":
					served, _ = h.serveStaleWhileWaking(w, req, app, rec)
				}
				if served || w.Body.Len() != 0 {
					t.Fatalf("privacy veto bypassed: served=%v body=%q", served, w.Body.String())
				}
			})
		}
	}
}
