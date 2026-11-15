package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// `app restart --project P` restarts every workload of P, keeps going past a
// failing one, and exits non-zero naming it.
func TestAppRestartProjectRunsEveryWorkload(t *testing.T) {
	var mu sync.Mutex
	var restarted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/shop":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"slug":"shop","workloads":[{"slug":"shop-web","workload_name":"web","status":"active"},{"slug":"shop-cart","workload_name":"cart","status":"active"},{"slug":"shop-mail","workload_name":"mail","status":"active"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/restart"):
			slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/restart")
			mu.Lock()
			restarted = append(restarted, slug)
			mu.Unlock()
			if slug == "shop-cart" {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"type":"about:blank","title":"busy","status":409,"code":"conflict","detail":"restart in progress"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"wake_id":"w-` + slug + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	out, restore := captureStdout(t)
	code := run([]string{"app", "restart", "--project", "shop"})
	restore()
	if code == 0 {
		t.Fatalf("a failed restart exited 0:\n%s", out.String())
	}
	if strings.Join(restarted, ",") != "shop-web,shop-cart,shop-mail" {
		t.Fatalf("restarted %v, want every workload in order", restarted)
	}
	if !strings.Contains(out.String(), "restart: 1 of 3 apps failed: shop-cart") {
		t.Fatalf("summary missing:\n%s", out.String())
	}
}

func TestProjectFlagValue(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		rest string
		ok   bool
	}{
		{[]string{"--project", "shop", "--fresh"}, "shop", "--fresh", true},
		{[]string{"--fresh", "--project=shop"}, "shop", "--fresh", true},
		{[]string{"--fresh"}, "", "--fresh", false},
		{[]string{"--", "--project", "shop"}, "", "-- --project shop", false},
	} {
		got, rest, ok := projectFlagValue(tc.args)
		if got != tc.want || strings.Join(rest, " ") != tc.rest || ok != tc.ok {
			t.Errorf("projectFlagValue(%v) = %q %v %v", tc.args, got, rest, ok)
		}
	}
	if !isAppProjectCommand([]string{"health", "--project", "shop"}) || isAppProjectCommand([]string{"exec", "--project", "shop"}) {
		t.Error("isAppProjectCommand verb gate wrong")
	}
}
