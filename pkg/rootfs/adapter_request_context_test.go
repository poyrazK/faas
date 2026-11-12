// adr: 20261102082606
package rootfs

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// ADR-20261102082606 amendment 2: a function adapter runs the handler, and any
// body it streams, inside the SDK's request context when the handler loaded
// the SDK. These stand-in SDK modules record the X-Faas-Deadline-Ms they were
// entered with, so the test does not depend on the real SDK packages.

const fakeNodeSDK = `import { AsyncLocalStorage } from "node:async_hooks";
export const store = new AsyncLocalStorage();
globalThis[Symbol.for("gregale.sdk.requestContext.v1")] = (headers, run) =>
  store.run(new Headers(headers).get("x-faas-deadline-ms") || "none", run);
`

const nodeContextHandler = `import { store } from "./fake-sdk.mjs";
export async function handler(event) {
  if (event.path === "/stream") {
    return { statusCode: 200, body: (async function* () {
      await new Promise((resolve) => setTimeout(resolve, 20));
      yield String(store.getStore());
    })() };
  }
  await new Promise((resolve) => setTimeout(resolve, 20));
  return { statusCode: 200, body: String(store.getStore()) };
}
`

const fakePythonSDK = `import contextlib
import contextvars

current = contextvars.ContextVar("deadline", default="none")

@contextlib.contextmanager
def with_gregale_request_context(headers):
    token = current.set({k.lower(): v for k, v in headers.items()}.get("x-faas-deadline-ms", "none"))
    try:
        yield
    finally:
        current.reset(token)
`

const pythonSyncContextHandler = `import faas_sdk.release_context as rc

def handler(event, ctx):
    if event["path"] == "/stream":
        def body():
            yield rc.current.get()
        return {"statusCode": 200, "body": body()}
    if event["path"] == "/awaitable":
        async def later():
            return {"statusCode": 200, "body": rc.current.get()}
        return later()
    return {"statusCode": 200, "body": rc.current.get()}
`

const pythonAsyncContextHandler = `import asyncio
import faas_sdk.release_context as rc

async def handler(event, ctx):
    await asyncio.sleep(0.01)
    if event["path"] == "/stream":
        async def body():
            await asyncio.sleep(0.01)
            yield rc.current.get()
        return {"statusCode": 200, "body": body()}
    return {"statusCode": 200, "body": rc.current.get()}
`

// contextBody sends one request with deadline ms and returns its whole body,
// joining a streamed body's chunks.
func contextBody(t *testing.T, a *sharedAdapter, id, path, ms string) string {
	t.Helper()
	a.send(map[string]any{"__faas_id": id, "method": "GET", "path": path, "query": "", "body_b64": "",
		"headers": map[string]string{"X-Faas-Deadline-Ms": ms}})
	var body []byte
	for {
		line := a.next()
		if line.Status >= 400 {
			t.Fatalf("%s %s: status %d; stderr=%s", id, path, line.Status, a.stderr)
		}
		piece, _ := base64.StdEncoding.DecodeString(line.BodyB64)
		body = append(body, piece...)
		if line.Frame == "" || line.Frame == "end" {
			return string(body)
		}
	}
}

func writeAdapterFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestNodeFunctionAdapterRunsHandlersInTheSDKRequestContext(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := writeAdapterFiles(t, map[string]string{
		"package.json": `{"type":"module"}`, "fake-sdk.mjs": fakeNodeSDK,
		"handler.js": nodeContextHandler, "adapter.mjs": nodeFunctionAdapter,
	})
	for _, concurrency := range []int{1, 2} {
		a, _ := startSharedAdapter(t, node, filepath.Join(dir, "adapter.mjs"), concurrency)
		for i, path := range []string{"/", "/stream"} {
			ms := strconv.Itoa(4000 + i)
			if got := contextBody(t, a, strconv.Itoa(i), path, ms); got != ms {
				t.Fatalf("concurrency %d %s: handler saw deadline %q, want %q", concurrency, path, got, ms)
			}
		}
	}
}

func TestNodeFunctionAdapterWithoutTheSDKIsUnchanged(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := writeAdapterFiles(t, map[string]string{
		"package.json": `{"type":"module"}`, "adapter.mjs": nodeFunctionAdapter,
		"handler.js": `export async function handler(event) { return { statusCode: 200, body: "plain" }; }`,
	})
	a, _ := startSharedAdapter(t, node, filepath.Join(dir, "adapter.mjs"), 1)
	if got := contextBody(t, a, "1", "/", "4000"); got != "plain" {
		t.Fatalf("body = %q", got)
	}
}

func TestPythonFunctionAdapterRunsHandlersInTheSDKRequestContext(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	for _, tc := range []struct {
		name    string
		handler string
		paths   []string
	}{
		{name: "sync", handler: pythonSyncContextHandler, paths: []string{"/", "/stream", "/awaitable"}},
		{name: "async", handler: pythonAsyncContextHandler, paths: []string{"/", "/stream"}},
	} {
		dir := writeAdapterFiles(t, map[string]string{
			"faas_sdk/__init__.py": "from . import release_context\n", "faas_sdk/release_context.py": fakePythonSDK,
			".faas-handler.py": tc.handler, "handler.py": pythonFunctionAdapter,
		})
		// Concurrency 1 serves requests one by one; 2 shares the worker on
		// the event loop (async) or on threads (sync).
		for _, concurrency := range []int{1, 2} {
			a, _ := startSharedAdapter(t, python, filepath.Join(dir, "handler.py"), concurrency)
			for i, path := range tc.paths {
				ms := strconv.Itoa(5000 + i)
				if got := contextBody(t, a, strconv.Itoa(i), path, ms); got != ms {
					t.Fatalf("%s handler, concurrency %d %s: saw deadline %q, want %q", tc.name, concurrency, path, got, ms)
				}
			}
		}
	}
}
