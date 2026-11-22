package main_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// boa multi-service deploy (2026-11-21): the first `deploy --project` writes
// a project-only link (no app). With an explicit --project-slug the next
// deploy skipped the link-tolerant branch and failed with "project has
// multiple or no workloads; link again with --app <slug>" before any request.
func TestDeployProjectSlugWithProjectOnlyLink(t *testing.T) {
	for _, args := range [][]string{
		{"deploy", "--project", "--project-slug", "boa", "--dry-run"},
		{"deploy", "--project-slug", "boa", "--only", "gateway", "--dry-run"},
		// R6-1 (deploy-path probe 2026-11-21): a plain redeploy of the Compose
		// project the first plain deploy linked.
		{"deploy", "--dry-run"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"compose.yaml":         "services:\n  gateway:\n    build: ./gateway\n    ports: [\"3000:3000\"]\n  worker:\n    build: ./worker\n",
				"gateway/package.json": `{"name":"gateway","scripts":{"start":"node index.js"}}`,
				"gateway/index.js":     "console.log(1)\n",
				"worker/package.json":  `{"name":"worker","scripts":{"start":"node index.js"}}`,
				"worker/index.js":      "console.log(1)\n",
			}
			for name, body := range files {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := main.SaveProjectContext(dir, main.LocalProjectContext{Version: 1, Project: "boa"}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_TOKEN", "test")
			main.WithResetJSONOutput(t, false)
			var mu sync.Mutex
			var slugs []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/projects/scan" && r.ParseMultipartForm(32<<20) == nil {
					mu.Lock()
					slugs = append(slugs, r.FormValue("project_slug"))
					mu.Unlock()
				}
				if r.URL.Path == "/v1/account" {
					main.WriteJSONTest(w, api.AccountResponse{Plan: string(api.PlanPro)})
					return
				}
				api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test stub"))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			_, restoreOut := main.CaptureStdout(t)
			stderr, restoreErr := main.CaptureStderr(t)
			main.Run(append(args, "--path", dir))
			restoreErr()
			restoreOut()
			if got := stderr.String(); strings.Contains(got, "multiple or no workloads") {
				t.Fatalf("project-only link refused: %s", got)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(slugs) == 0 || slugs[0] != "boa" {
				t.Fatalf("project scan slugs = %v, want boa", slugs)
			}
		})
	}
}
