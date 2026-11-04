package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// rc.318 field report: `deploy --dry-run` counted the keys an
// `x-gregale-env-import: true` Compose file imports, but `deploy check
// --project` reported them missing. The check now sends them as provided
// keys, and never a value.
func TestDeployCheckProjectCountsImportedComposeEnv(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"compose.yaml": "x-gregale-env-import: true\nservices:\n  api:\n    build: ./api\n    ports: [\"3000:3000\"]\n" +
			"    environment:\n      LOG_LEVEL: debug\n      API_TOKEN: s3cret-value\n      FROM_HOST:\n",
		"api/package.json": `{"name":"api","scripts":{"start":"node index.js"}}`,
		"api/index.js":     "console.log(1)\n",
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
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	main.WithResetJSONOutput(t, false)
	var got api.ProjectDeploymentCheckRequest
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			main.WriteJSONTest(w, api.AccountResponse{Plan: string(api.PlanHobby)})
		case "/v1/projects/shop/deployment-readiness":
			var err error
			if raw, err = io.ReadAll(r.Body); err != nil || json.Unmarshal(raw, &got) != nil {
				t.Errorf("bad readiness request: %v %s", err, raw)
			}
			checks := []api.ProjectDeploymentCheck{{Code: "env_complete", Status: "passed", Message: "ok"}}
			main.WriteJSONTest(w, api.ProjectDeploymentReadiness{SchemaVersion: 1, ProjectSlug: "shop", Environment: "production", Status: api.DeploymentReadinessStatus(checks), Checks: checks, CheckedAt: time.Now().UTC()})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	stdout, restore := main.CaptureStdout(t)
	code := main.CmdDeployCheck([]string{"--project", "--project-slug", "shop", "--path", dir, "--worktree"})
	restore()
	if code != 0 || len(got.Workloads) != 1 {
		t.Fatalf("exit = %d, workloads = %+v; stdout=%s", code, got.Workloads, stdout.String())
	}
	if keys := got.Workloads[0].ProvidedKeys; !slices.Equal(keys, []string{"LOG_LEVEL"}) {
		t.Fatalf("provided keys = %v, want the importable plain value only", keys)
	}
	if bytes.Contains(raw, []byte("debug")) || bytes.Contains(raw, []byte("s3cret-value")) {
		t.Fatalf("a Compose value left the machine: %s", raw)
	}
}
