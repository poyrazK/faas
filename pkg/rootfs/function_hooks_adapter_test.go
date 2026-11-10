package rootfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hookWorker drives one generated adapter over the persistent protocol.
type hookWorker struct {
	t      *testing.T
	stdin  interface{ Write([]byte) (int, error) }
	scan   *bufio.Scanner
	stderr *bytes.Buffer
	ready  map[string]any
}

type hookResponse struct {
	Status  int    `json:"status"`
	BodyB64 string `json:"body_b64"`
}

func startHookWorker(t *testing.T, bin string, args []string, dir string) *hookWorker {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FAAS_PERSISTENT_WORKER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	w := &hookWorker{t: t, stdin: stdin, scan: bufio.NewScanner(stdout), stderr: &stderr}
	if !w.scan.Scan() {
		t.Fatalf("no ready frame: %v; stderr=%s", w.scan.Err(), stderr.String())
	}
	if err := json.Unmarshal(w.scan.Bytes(), &w.ready); err != nil || w.ready["__faas_ready"] != true {
		t.Fatalf("ready frame = %q (%v)", w.scan.Text(), err)
	}
	return w
}

func (w *hookWorker) send(envelope map[string]any) hookResponse {
	w.t.Helper()
	line, _ := json.Marshal(envelope)
	if _, err := w.stdin.Write(append(line, '\n')); err != nil {
		w.t.Fatalf("write: %v", err)
	}
	if !w.scan.Scan() {
		w.t.Fatalf("read: %v; stderr=%s", w.scan.Err(), w.stderr.String())
	}
	var got hookResponse
	if err := json.Unmarshal(w.scan.Bytes(), &got); err != nil {
		w.t.Fatalf("decode %q: %v", w.scan.Text(), err)
	}
	return got
}

func (w *hookWorker) hook(name string) int {
	return w.send(map[string]any{"method": "POST", "path": "/__gregale/function/hook", "headers": map[string]string{}, "function_hook": name}).Status
}

func (w *hookWorker) body() string {
	w.t.Helper()
	got := w.send(map[string]any{"method": "GET", "path": "/", "headers": map[string]string{}, "body_b64": ""})
	raw, _ := base64.StdEncoding.DecodeString(got.BodyB64)
	return string(raw)
}

func hooksIn(ready map[string]any) string {
	list, _ := ready["function_hooks"].([]any)
	names := make([]string, 0, len(list))
	for _, name := range list {
		names = append(names, name.(string))
	}
	return strings.Join(names, ",")
}

// ADR-20261110190000: init runs before ready, the ready line advertises the
// hooks, and function_hook envelopes run beforeSnapshot / afterRestore
// without calling the handler; a throwing hook answers 500.
func TestNodeAdapterRunsFunctionSnapshotHooks(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := `const state = { init: 0, before: 0, after: 0, calls: 0 };
export default {
  async init() { await new Promise((r) => setTimeout(r, 20)); state.init++; },
  beforeSnapshot() { state.before++; },
  async afterRestore(ctx) { if (process.env.HOOK_FAIL === "1") throw new Error("boom"); state.after++; },
  async fetch() { state.calls++; return Response.json(state); },
};`
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.mjs")
	if err := os.WriteFile(adapter, []byte(nodeFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	w := startHookWorker(t, node, []string{adapter}, dir)
	if got := hooksIn(w.ready); got != "init,before_snapshot,after_restore" || w.ready["function_hooks_version"] != float64(1) {
		t.Fatalf("ready = %v", w.ready)
	}
	if got := w.body(); !strings.Contains(got, `"init":1`) || !strings.Contains(got, `"calls":1`) {
		t.Fatalf("init did not run before the first request: %s", got)
	}
	if s := w.hook("before_snapshot"); s != 204 {
		t.Fatalf("before_snapshot status = %d", s)
	}
	if s := w.hook("after_restore"); s != 204 {
		t.Fatalf("after_restore status = %d", s)
	}
	if s := w.hook("init"); s != 400 {
		t.Fatalf("unknown hook status = %d, want 400", s)
	}
	if got := w.body(); !strings.Contains(got, `"before":1`) || !strings.Contains(got, `"after":1`) || !strings.Contains(got, `"calls":2`) {
		t.Fatalf("hooks ran the handler or did not run: %s", got)
	}

	t.Setenv("HOOK_FAIL", "1")
	failing := startHookWorker(t, node, []string{adapter}, dir)
	if s := failing.hook("after_restore"); s != 500 {
		t.Fatalf("throwing afterRestore status = %d, want 500", s)
	}
}

// A function without hooks advertises none and answers hook calls at once.
func TestNodeAdapterWithoutHooksAnswersHookCalls(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "handler.js"), []byte(`export async function handler() { return {body: "ok"}; }`), 0o644)
	adapter := filepath.Join(dir, "adapter.mjs")
	_ = os.WriteFile(adapter, []byte(nodeFunctionAdapter), 0o644)
	w := startHookWorker(t, node, []string{adapter}, dir)
	if got := hooksIn(w.ready); got != "" {
		t.Fatalf("hooks = %q, want none", got)
	}
	if s := w.hook("after_restore"); s != 204 {
		t.Fatalf("after_restore status = %d", s)
	}
}

func TestPythonAdapterRunsFunctionSnapshotHooks(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	dir := t.TempDir()
	impl := `import asyncio, json, os
state = {"init": 0, "before": 0, "after": 0, "calls": 0}

async def init():
    await asyncio.sleep(0.01)
    state["init"] += 1

def before_snapshot(ctx):
    assert ctx.hook == "before_snapshot"
    state["before"] += 1

async def after_restore():
    if os.environ.get("HOOK_FAIL") == "1":
        raise RuntimeError("boom")
    state["after"] += 1

def handler(event, ctx):
    state["calls"] += 1
    return {"body": json.dumps(state)}
`
	if err := os.WriteFile(filepath.Join(dir, ".faas-handler.py"), []byte(impl), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.py")
	if err := os.WriteFile(adapter, []byte(pythonFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	w := startHookWorker(t, python, []string{adapter}, dir)
	if got := hooksIn(w.ready); got != "init,before_snapshot,after_restore" {
		t.Fatalf("ready = %v", w.ready)
	}
	if got := w.body(); !strings.Contains(got, `"init": 1`) || !strings.Contains(got, `"calls": 1`) {
		t.Fatalf("init did not run before the first request: %s", got)
	}
	if s := w.hook("before_snapshot"); s != 204 {
		t.Fatalf("before_snapshot status = %d; stderr=%s", s, w.stderr.String())
	}
	if s := w.hook("after_restore"); s != 204 {
		t.Fatalf("after_restore status = %d", s)
	}
	if s := w.hook("nope"); s != 400 {
		t.Fatalf("unknown hook status = %d, want 400", s)
	}
	if got := w.body(); !strings.Contains(got, `"before": 1`) || !strings.Contains(got, `"after": 1`) || !strings.Contains(got, `"calls": 2`) {
		t.Fatalf("hooks ran the handler or did not run: %s", got)
	}

	t.Setenv("HOOK_FAIL", "1")
	failing := startHookWorker(t, python, []string{adapter}, dir)
	if s := failing.hook("after_restore"); s != 500 {
		t.Fatalf("raising after_restore status = %d, want 500", s)
	}
}
