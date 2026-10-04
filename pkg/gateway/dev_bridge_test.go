// adr: 378
package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevBridgeBypassesSharedCacheAndEdgeAnswers(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(NewResponseCache())
	rule := EdgeRuleCacheResolved{ID: "cache-rule", PathGlob: "/catalog", MaxAgeSeconds: 60}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	now := time.Now()
	h.responseCache.Put(CacheKey{AppID: "app-1", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, 200, nil, []byte("shared response"), now.Add(time.Minute), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction())
	request := httptest.NewRequest("GET", "http://jane-api.apps.dom/catalog", nil)
	request = request.WithContext(WithDevBridgeScope(request.Context(), "account", "development", "frontend"))
	response := httptest.NewRecorder()
	served, matched := h.applyEdgeRuleCache(response, request, App{ID: "app-1", Plan: api.PlanPro}, newTestStatusRecorder(response))
	if served || matched != nil || response.Body.Len() != 0 {
		t.Fatal("scoped response used shared cache or enabled its writer")
	}
	request.URL.Path = "/favicon.ico"
	if h.serveEdgeAnswer(httptest.NewRecorder(), request, App{ID: "app-1"}) {
		t.Fatal("scoped request replaced with an edge answer")
	}
	ordinary := httptest.NewRequest("GET", "http://jane-api.apps.dom/favicon.ico", nil)
	if !h.serveEdgeAnswer(httptest.NewRecorder(), ordinary, App{ID: "app-1"}) {
		t.Fatal("ordinary edge answer changed")
	}
}

func TestDevBridgeRetainsAppRateLimitBeforeLocalForwarding(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.RequestRateLimitRPS = 1
	backend.app.RequestRateLimitBurst = 1
	forwarded := 0
	h.WithDevBridge(func(r *http.Request) *api.Problem {
		*r = *r.WithContext(WithDevBridgeScope(r.Context(), "account", "development", "payments"))
		return nil
	}, func(w http.ResponseWriter, r *http.Request, app App) bool {
		forwarded++
		w.WriteHeader(204)
		return true
	})
	for n, want := range []int{204, 429} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", "http://jane-api.apps.dom/charge", nil))
		if response.Code != want {
			t.Fatalf("request=%d status=%d want=%d body=%s", n, response.Code, want, response.Body)
		}
	}
	if forwarded != 1 || backend.admits != 0 {
		t.Fatalf("forwarded=%d VM admissions=%d", forwarded, backend.admits)
	}
}

func TestDevBridgePublicToInternalProxyStreamsDuplex(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).EnableFullDuplex()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		_, _ = io.Copy(w, r.Body)
	}))
	defer upstream.Close()
	proxy := NewInternalReverseProxy(&stubDialer{server: upstream}, &url.URL{Scheme: "http", Host: "internal"}, nil, false)
	edge := httptest.NewServer(proxy)
	defer edge.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	go func() { <-ctx.Done(); _ = writer.CloseWithError(ctx.Err()) }()
	defer func() { _ = writer.Close() }()
	request, _ := http.NewRequestWithContext(ctx, "POST", edge.URL+"/echo", reader)
	request.Header.Set("X-Gregale-Dev-Bridge-Session", "session")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("duplex", 10000)
	go func() { _, _ = io.WriteString(writer, payload); _ = writer.Close() }()
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != payload {
		t.Fatalf("status=%d bytes=%d err=%v", response.StatusCode, len(body), err)
	}
}
