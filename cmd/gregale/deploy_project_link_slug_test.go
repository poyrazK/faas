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

// ChronoLake finding 25.3: with a link naming both a project and an app,
// `deploy --project --environment staging --dry-run` targeted the project
// named after the app ("no environment staging exists in project
// chronolake-gateway").
func TestDeployProjectUsesLinkedProjectNotLinkedApp(t *testing.T) {
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
	if _, err := main.SaveProjectContext(dir, main.LocalProjectContext{Version: 1, Project: "chronolake", App: "chronolake-gateway"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	main.WithResetJSONOutput(t, false)
	var mu sync.Mutex
	var paths, slugs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1/projects/scan" && r.ParseMultipartForm(32<<20) == nil {
			slugs = append(slugs, r.FormValue("project_slug"))
		}
		mu.Unlock()
		if r.URL.Path == "/v1/account" {
			main.WriteJSONTest(w, api.AccountResponse{Plan: string(api.PlanPro)})
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test stub"))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	_, restoreOut := main.CaptureStdout(t)
	_, restoreErr := main.CaptureStderr(t)
	main.Run([]string{"deploy", "--project", "--environment", "staging", "--dry-run", "--path", dir})
	restoreErr()
	restoreOut()
	mu.Lock()
	defer mu.Unlock()
	for _, path := range paths {
		if strings.Contains(path, "/projects/chronolake-gateway") {
			t.Fatalf("project resolved from the linked app: %v", paths)
		}
	}
	if len(slugs) == 0 || slugs[0] != "chronolake" {
		t.Fatalf("project scan slugs = %v (paths %v), want chronolake", slugs, paths)
	}
}
