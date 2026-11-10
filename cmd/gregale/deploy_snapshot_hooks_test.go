package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ADR-20261110190000: `gregale deploy --plan` lists the snapshot hooks each
// function declares in code, in text and JSON, without running the code.
func TestDeployPlanListsSnapshotHooks(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"gregale.yaml":             "functions:\n  - name: api\n    root: functions/api\n  - name: job\n    root: functions/job\n",
		"functions/api/handler.js": "export default {\n  async fetch() { return new Response('ok'); },\n  async afterRestore() {},\n  beforeSnapshot() {},\n};\n",
		"functions/job/handler.py": "def handler(event, ctx):\n    return {}\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAAS_TOKEN", "")

	code, stdout, stderr := runCLI(t, "deploy", "--plan", "--path", root)
	if code != 0 || !strings.Contains(stdout, "Snapshot hooks in code:") || !strings.Contains(stdout, "before_snapshot, after_restore") {
		t.Fatalf("deploy --plan = %d:\n%s%s", code, stdout, stderr)
	}

	code, stdout, _ = runCLI(t, "--json", "deploy", "--plan", "--path", root)
	var plan struct {
		Functions []struct {
			Name  string   `json:"name"`
			Hooks []string `json:"hooks"`
		} `json:"functions"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &plan) != nil {
		t.Fatalf("deploy --plan --json = %d: %s", code, stdout)
	}
	for _, fn := range plan.Functions {
		want := map[string][]string{"api": {"before_snapshot", "after_restore"}, "job": nil}[fn.Name]
		if !slices.Equal(fn.Hooks, want) {
			t.Errorf("%s hooks = %v, want %v", fn.Name, fn.Hooks, want)
		}
	}
}
