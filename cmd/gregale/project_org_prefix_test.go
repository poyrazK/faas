package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// TestProjectOrgPrefix checks the --org prefix (apps/projects review
// 2026-11-10, P3): project commands go to the company's project routes, other
// commands keep their paths, and a missing slug is refused before any request.
func TestProjectOrgPrefix(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		wantCode int
		wantPath string
		wantErr  string
	}{
		{name: "personal", args: []string{"projects", "list"}, wantPath: "/v1/projects"},
		{name: "company", args: []string{"--org", "acme", "projects", "list"}, wantPath: "/v1/orgs/acme/projects"},
		{name: "company equals form", args: []string{"--org=acme", "--json", "projects", "info", "shop"}, wantPath: "/v1/orgs/acme/projects/shop"},
		{name: "app commands keep their paths", args: []string{"--org", "acme", "park", "demo"}, wantPath: "/v1/apps/demo/park"},
		{name: "missing slug", args: []string{"--org"}, wantCode: 1, wantErr: "--org requires an organization slug"},
		{name: "flag as slug", args: []string{"--org", "--non-interactive", "projects", "list"}, wantCode: 1, wantErr: "--org requires an organization slug"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			main.SetupCLIRegression(t)
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				if strings.HasSuffix(r.URL.Path, "/projects") {
					_ = json.NewEncoder(w).Encode([]any{})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"slug": "shop"})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			stderr := captureStderr(t)
			code := main.Run(tt.args)
			if tt.wantErr != "" {
				if code != tt.wantCode || !strings.Contains(stderr.String(), tt.wantErr) {
					t.Fatalf("exit = %d, stderr:\n%s\nwant %d and %q", code, stderr, tt.wantCode, tt.wantErr)
				}
				if len(paths) != 0 {
					t.Fatalf("refused prefix sent requests: %v", paths)
				}
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if len(paths) == 0 || paths[0] != tt.wantPath {
				t.Fatalf("exit = %d, paths = %v, want first %s; stderr:\n%s", code, paths, tt.wantPath, stderr)
			}
		})
	}
}
