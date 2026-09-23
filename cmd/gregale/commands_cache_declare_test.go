package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdCacheDeclare_ExactShorthandUsesLinkedApp(t *testing.T) {
	resetJSONOut(t)
	root := t.TempDir()
	if _, err := saveProjectContext(root, localProjectContext{
		Version: projectContextVersion,
		Project: "shop",
		App:     "products",
	}); err != nil {
		t.Fatalf("save project context: %v", err)
	}
	t.Chdir(filepath.Join(root, ".gregale"))

	var got api.CreateEdgeRuleRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/products":
			_ = json.NewEncoder(w).Encode(api.AppResponse{
				ID:           "app-products",
				Slug:         "products",
				URL:          "https://products.apps.gregale.test",
				CanonicalURL: "https://api.example.com",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/products/edge-rules":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.EdgeRuleResponse{
				ID:       "rule-cache-1",
				AppID:    "app-products",
				Kind:     "cache",
				Priority: 100,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	if code := cmdCache([]string{
		"GET", "/products/:id", "for", "30s",
		"--stale-while-revalidate", "1m",
		"--stale-if-error", "0s",
		"--vary-on", "Accept-Language",
	}); code != 0 {
		t.Fatalf("cache shorthand exit = %d, want 0", code)
	}
	if got.MatchHost != "api.example.com" || got.MatchPath != "/products/*" {
		t.Fatalf("match = %s %s, want api.example.com /products/*", got.MatchHost, got.MatchPath)
	}
	if !reflect.DeepEqual(got.MatchMethods, []string{"GET"}) {
		t.Fatalf("match methods = %v, want [GET]", got.MatchMethods)
	}
	var action api.EdgeRuleCacheAction
	if err := json.Unmarshal(got.Action, &action); err != nil {
		t.Fatalf("decode cache action: %v", err)
	}
	if action.MaxAgeSeconds != 30 || action.StaleWhileRevalidateSeconds != 60 || action.StaleIfErrorSeconds != 0 {
		t.Fatalf("cache windows = %+v", action)
	}
	if !reflect.DeepEqual(action.Methods, []string{"GET"}) || !reflect.DeepEqual(action.VaryOn, []string{"Accept-Language"}) {
		t.Fatalf("cache dimensions = %+v", action)
	}
}

func TestCacheRoutePattern(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/products/:id", "/products/*"},
		{"/orgs/:org/products/:id", "/orgs/*/products/*"},
		{"/products/*", "/products/*"},
	}
	for _, test := range tests {
		got, err := cacheRoutePattern(test.in)
		if err != nil {
			t.Fatalf("cacheRoutePattern(%q): %v", test.in, err)
		}
		if got != test.want {
			t.Errorf("cacheRoutePattern(%q) = %q, want %q", test.in, got, test.want)
		}
	}
	for _, invalid := range []string{"products/:id", "/products/:", "/products?sort=new"} {
		if _, err := cacheRoutePattern(invalid); err == nil {
			t.Errorf("cacheRoutePattern(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestCacheDurationSeconds(t *testing.T) {
	if got, err := cacheDurationSeconds("duration", "30s", 60, false); err != nil || got != 30 {
		t.Fatalf("duration = %d, %v; want 30, nil", got, err)
	}
	for _, invalid := range []string{"0s", "1500ms", "61s", "forever"} {
		if _, err := cacheDurationSeconds("duration", invalid, 60, false); err == nil {
			t.Errorf("duration %q unexpectedly succeeded", invalid)
		}
	}
}
