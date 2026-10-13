// adr: 1253
package mcpmanagement

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWatchHTTPDispatchPreservesBodiesAndBounds(t *testing.T) {
	handler := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			w.Header().Set("X-Request-Body", string(data))
			_, _ = w.Write([]byte(name))
		})
	}
	router := watchProgressTransport(handler("ordinary"), handler("watch_json"), handler("watch_stream"))
	for _, scenario := range []struct{ body, want string }{
		{`{"method":"subscriptions/listen","params":{"notifications":{"resourceSubscriptions":["gregale://default/apps/alpha/health"]}}}`, "watch_stream"},
		{`{"method":"tools/call","params":{"name":"gregale_context","_meta":{"progressToken":"one"}}}`, "ordinary"},
		{`{"method":"tools/call","params":{"name":"gregale_watch_rollout"}}`, "watch_json"},
		{`{"method":"tools/call","params":{"name":"gregale_watch_rollout","_meta":{"progressToken":null}}}`, "watch_json"},
		{`{"method":"tools/call","params":{"name":"gregale_watch_rollout","_meta":{"progressToken":0}}}`, "watch_stream"},
		{`{"method":"tools/call","params":{"name":"gregale_watch_rollout","_meta":{"progressToken":"one"}}}`, "watch_stream"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(scenario.body))
		router.ServeHTTP(w, req)
		if w.Body.String() != scenario.want || w.Header().Get("X-Request-Body") != scenario.body {
			t.Fatal("dispatch changed wire request or selected wrong response")
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", MaxFrameBytes+1))))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("progress dispatch bypassed frame limit")
	}
}
