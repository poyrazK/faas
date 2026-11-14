package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestEdgeRuleUpdateAcceptsAndChecksApp pins Hunt 32 H32-15: `edge rule
// update <id> --app web` failed with "unknown flag --app" though create
// requires it. The app is now checked against the rule before the PATCH.
func TestEdgeRuleUpdateAcceptsAndChecksApp(t *testing.T) {
	var patches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/rule-1"):
			_, _ = w.Write([]byte(`{"id":"rule-1","app_id":"app-web"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/apps/web"):
			_, _ = w.Write([]byte(`{"id":"app-web","slug":"web"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/apps/other"):
			_, _ = w.Write([]byte(`{"id":"app-other","slug":"other"}`))
		case r.Method == http.MethodPatch:
			patches.Add(1)
			_, _ = w.Write([]byte(`{"id":"rule-1","app_id":"app-web"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	_, _, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"edge", "rule", "update", "rule-1", "--app", "web", "--match-path", "/cacheable"}); code != 0 {
		t.Fatalf("update with the owning app exit = %d", code)
	}
	if code := main.Run([]string{"edge", "rule", "update", "rule-1", "--app", "other", "--match-path", "/x"}); code == 0 {
		t.Fatal("update with another app's slug succeeded")
	}
	if got := patches.Load(); got != 1 {
		t.Fatalf("PATCHes = %d, want 1 (the mismatched app must not reach the API)", got)
	}
}
