package main_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// A plain `gregale deploy` of a repository that is no single app but a
// Compose project or a workspace of services plans the whole project instead
// of failing with a hint.
func TestDeployOfMultiServiceRepositoryPlansTheProject(t *testing.T) {
	for _, tc := range []struct {
		name        string
		files       map[string]string
		wantProject bool
	}{
		{"compose project", map[string]string{"compose.yaml": "services:\n  api:\n    build: ./api\n", "api/Dockerfile": "FROM scratch\n"}, true},
		{"workspace of services", map[string]string{"services/api/package.json": "{}", "services/web/package.json": "{}"}, true},
		{"root app with a compose file for local dev", map[string]string{"package.json": "{}", "compose.yaml": "services:\n  db:\n    image: postgres:17\n"}, false},
		// A root package.json that only lists workspaces is no app.
		{"npm workspaces with a root package.json", map[string]string{
			"package.json":              `{"private":true,"workspaces":["packages/*"]}`,
			"packages/api/package.json": `{"name":"api","scripts":{"start":"node index.js"}}`,
			"packages/web/package.json": `{"name":"web","scripts":{"start":"node index.js"}}`,
		}, true},
		// A Go module with no main package at its root is no app either.
		{"Go module with services below it", map[string]string{
			"go.mod": "module example.com/shop\n", "lib.go": "package shop\n",
			"services/api/Dockerfile": "FROM scratch\n", "services/web/Dockerfile": "FROM scratch\n",
		}, true},
		{"Go app at the root beside services", map[string]string{
			"go.mod": "module example.com/shop\n", "main.go": "package main\n\nfunc main() {}\n",
			"services/api/Dockerfile": "FROM scratch\n", "services/web/Dockerfile": "FROM scratch\n",
		}, false},
		// ADR-20261025134512: each cmd/ main package of one module is a service.
		{"Go module with several commands", map[string]string{
			"go.mod": "module example.com/shop\n", "internal/db/db.go": "package db\n",
			"cmd/api/main.go": "package main\n\nfunc main() {}\n", "cmd/worker/main.go": "package main\n\nfunc main() {}\n",
		}, true},
		{"Go module with one command", map[string]string{
			"go.mod": "module example.com/shop\n", "cmd/api/main.go": "package main\n\nfunc main() {}\n",
		}, false},
		{"Node app at the root beside packages", map[string]string{
			"package.json":              `{"scripts":{"start":"node server.js"}}`,
			"packages/api/package.json": `{"name":"api","scripts":{"start":"node index.js"}}`,
			"packages/web/package.json": `{"name":"web","scripts":{"start":"node index.js"}}`,
		}, false},
		{"one service below a workspace root", map[string]string{
			"package.json":              `{"private":true,"workspaces":["packages/*"]}`,
			"packages/api/package.json": `{"name":"api","scripts":{"start":"node index.js"}}`,
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tc.files {
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_multi_service_detection")
			stderr, restore := main.CaptureStderr(t)
			code := main.Run([]string{"deploy", "--path", root, "--source", "worktree", "--no-doctor", "--yes"})
			restore()
			if code == 0 {
				t.Fatalf("deploy against a failing API succeeded; stderr=%s", stderr.String())
			}
			mu.Lock()
			planned := slices.Contains(paths, "/v1/projects/scan")
			mu.Unlock()
			announced := strings.Contains(stderr.String(), "deploying it as one project")
			if planned != tc.wantProject || announced != tc.wantProject {
				t.Fatalf("project plan requested = %v, announced = %v; want %v (paths %v, stderr %s)", planned, announced, tc.wantProject, paths, stderr.String())
			}
		})
	}
}
