package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
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

type hookLifecycleClient struct {
	app     api.AppResponse
	updates []api.UpdateAppRequest
}

func (c *hookLifecycleClient) GetApp(context.Context, string) (api.AppResponse, error) {
	return c.app, nil
}

func (c *hookLifecycleClient) UpdateApp(_ context.Context, _ string, req api.UpdateAppRequest) (api.AppResponse, error) {
	c.updates = append(c.updates, req)
	return c.app, nil
}

// A single-function deploy points the app's lifecycle at the runner for the
// hooks its handler declares, clears hooks the code dropped, and refuses a
// hook that is also set to an app path of its own (ADR-20261110190000 §5).
func TestSingleFunctionDeployAppliesSnapshotHooks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("export default { fetch() {}, beforeSnapshot() {} };\n")
	c := &hookLifecycleClient{}
	if err := main.ApplyFunctionHookLifecycleForTest(ctx, c, "api", dir, ""); err != nil {
		t.Fatal(err)
	}
	if len(c.updates) != 1 || c.updates[0].BeforeCheckpoint == nil || c.updates[0].BeforeCheckpoint.Path != functionlifetime.FunctionBeforeSnapshotPath || c.updates[0].AfterRestore != nil {
		t.Fatalf("updates = %+v, want before_checkpoint at the reserved path only", c.updates)
	}

	// Already wired: no update.
	c = &hookLifecycleClient{}
	c.app.Manifest.BeforeCheckpoint = &api.BeforeCheckpointHook{Path: functionlifetime.FunctionBeforeSnapshotPath}
	if err := main.ApplyFunctionHookLifecycleForTest(ctx, c, "api", dir, ""); err != nil || len(c.updates) != 0 {
		t.Fatalf("wired app: err %v updates %+v, want none", err, c.updates)
	}

	// Code dropped the hook: it is cleared.
	write("export default { fetch() {} };\n")
	if err := main.ApplyFunctionHookLifecycleForTest(ctx, c, "api", dir, ""); err != nil {
		t.Fatal(err)
	}
	if len(c.updates) != 1 || c.updates[0].BeforeCheckpoint == nil || c.updates[0].BeforeCheckpoint.Path != "" {
		t.Fatalf("updates = %+v, want before_checkpoint cleared", c.updates)
	}

	// An app hook of its own plus the same hook in code is refused.
	write("export default { fetch() {}, afterRestore() {} };\n")
	c = &hookLifecycleClient{}
	c.app.Manifest.AfterRestore = &api.AfterRestoreHook{Path: "/healthz/restore"}
	if err := main.ApplyFunctionHookLifecycleForTest(ctx, c, "api", dir, ""); err == nil || len(c.updates) != 0 {
		t.Fatalf("conflict: err %v updates %+v, want a refusal", err, c.updates)
	}
}
