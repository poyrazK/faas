package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestCmdDebugRegressionsListVerb pins issue #2764: "list" is accepted as an
// explicit verb before a slug, and a lone "list" remains a valid app slug.
func TestCmdDebugRegressionsListVerb(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantPath string
	}{
		{name: "list before slug", args: []string{"list", "my-app", "--since", "24h"}, wantPath: "/v1/apps/my-app/debug/regressions"},
		{name: "bare slug", args: []string{"my-app"}, wantPath: "/v1/apps/my-app/debug/regressions"},
		{name: "app named list", args: []string{"list"}, wantPath: "/v1/apps/list/debug/regressions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.DebugRegressionsResponse{Since: "24h"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test")

			_, _, restore := swapIO(t)
			defer restore()
			oldJSON := jsonOutput
			jsonOutput = true
			defer func() { jsonOutput = oldJSON }()

			if code := cmdDebugRegressions(tc.args); code != 0 {
				t.Fatalf("cmdDebugRegressions(%q) = %d, want 0", tc.args, code)
			}
			if gotPath != tc.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tc.wantPath)
			}
		})
	}
}

func TestCmdDebugRegressionsListWithAllListsEveryApp(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps" {
			_ = json.NewEncoder(w).Encode([]api.AppResponse{{Slug: "my-app"}})
			return
		}
		_ = json.NewEncoder(w).Encode(api.DebugRegressionsResponse{Since: "24h"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	_, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	if code := cmdDebugRegressions([]string{"list", "--all"}); code != 0 {
		t.Fatalf("cmdDebugRegressions(list --all) = %d, want 0 (paths %q)", code, paths)
	}
	if len(paths) == 0 || paths[len(paths)-1] != "/v1/apps/my-app/debug/regressions" {
		t.Fatalf("paths = %q, want fleet listing ending with my-app regressions", paths)
	}
}

func TestCmdDebugRegressionsListNotFoundExplainsSyntax(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(api.Problem{Title: "Not found", Status: http.StatusNotFound, Code: api.CodeNotFound, Detail: "no such app"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	_, readStderr, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	if code := cmdDebugRegressions([]string{"list", "--since", "24h"}); code == 0 {
		t.Fatal("cmdDebugRegressions(list) = 0, want failure for a missing app")
	}
	if got := readStderr(); !strings.Contains(got, debugRegressionsUsage) {
		t.Fatalf("stderr missing usage hint:\n%s", got)
	}
}
