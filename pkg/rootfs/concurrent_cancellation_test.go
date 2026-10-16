package rootfs

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func adapterWithSource(t *testing.T, runtime, source string) *sharedAdapter {
	t.Helper()
	interpreter, handlerName, adapterName, adapter := "node", "handler.js", "adapter.mjs", nodeFunctionAdapter
	if runtime == "python" {
		interpreter, handlerName, adapterName, adapter = "python3", ".faas-handler.py", "handler.py", pythonFunctionAdapter
	}
	bin, err := exec.LookPath(interpreter)
	if err != nil {
		t.Skipf("%s unavailable", interpreter)
	}
	dir := t.TempDir()
	for file, content := range map[string]string{
		"package.json": `{"type":"module"}`,
		handlerName:    source,
		adapterName:    adapter,
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := startSharedAdapter(t, bin, filepath.Join(dir, adapterName), 4)
	return a
}

func responseObject(t *testing.T, line sharedLine) map[string]any {
	t.Helper()
	body, err := base64.StdEncoding.DecodeString(line.BodyB64)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("response %s: %v", body, err)
	}
	return result
}

// The handler deliberately keeps its cleanup blocked until a neighbouring
// request releases it. A cancel acknowledgement before that release would let
// the runner reuse a slot while abandoned work is still executing.
func TestSharedAdaptersAcknowledgeAfterCleanup(t *testing.T) {
	for _, tc := range []struct{ runtime, source string }{
		{"node", `let release, cleaned = false;
const gate = new Promise(resolve => { release = resolve; });
export async function handler(event, ctx) {
  if (event.path === "/hold") return {body: (async function* () {
    try { yield "started"; await new Promise((_, reject) => ctx.signal.addEventListener("abort", () => reject(ctx.signal.reason), {once:true})); }
    finally { await gate; cleaned = true; }
  })()};
  if (event.path === "/release") release();
  return {body: {cleaned, invocation:ctx.invocation_id, token:event.headers["x-token"]}};
}`},
		{"python", `import asyncio
gate = asyncio.Event()
cleaned = False
async def handler(event, ctx):
    global cleaned
    if event["path"] == "/hold":
        async def body():
            global cleaned
            try:
                yield "started"
                await asyncio.Event().wait()
            except asyncio.CancelledError:
                await gate.wait()
                cleaned = True
                yield "must be suppressed"
        return {"body": body()}
    if event["path"] == "/release":
        gate.set()
    return {"body": {"cleaned":cleaned, "invocation":ctx.invocation_id, "token":event["headers"].get("x-token")}}
`},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			a := adapterWithSource(t, tc.runtime, tc.source)
			a.request("hold", "/hold", "")
			if head, chunk := a.next(), a.next(); head.Frame != "head" || chunk.Frame != "chunk" {
				t.Fatalf("hold did not start: %+v %+v", head, chunk)
			}
			a.send(map[string]any{"__faas_cancel": "hold"})
			a.send(map[string]any{"__faas_id": "probe", "path": "/probe", "headers": map[string]string{"X-Faas-Invocation-Id": "neighbour", "x-token": "own-token"}})
			probe := a.next()
			if probe.ID != "probe" || probe.Cancelled != "" {
				t.Fatalf("cancel acknowledged before cleanup completed: %+v", probe)
			}
			got := responseObject(t, probe)
			if got["cleaned"] != false || got["invocation"] != "neighbour" || got["token"] != "own-token" {
				t.Fatalf("neighbour context = %v", got)
			}
			a.request("release", "/release", "")
			seen := map[string]bool{}
			for len(seen) < 2 {
				line := a.next()
				switch {
				case line.Cancelled == "hold":
					seen["ack"] = true
				case line.ID == "release":
					seen["release"] = true
				default:
					t.Fatalf("unexpected frame after cancellation: %+v", line)
				}
			}
			a.request("after", "/probe", "")
			if got := responseObject(t, a.next()); got["cleaned"] != true {
				t.Fatalf("acknowledgement preceded cleanup: %v", got)
			}
		})
	}
}

func TestNodeFunctionAdapterCancelsFetchSignalAndBody(t *testing.T) {
	a := adapterWithSource(t, "node", `let signalAborted = false, bodyCancelled = false, release;
const gate = new Promise(resolve => { release = resolve; });
export default {fetch(request) {
  if (new URL(request.url).pathname === "/hold") {
    request.signal.addEventListener("abort", () => { signalAborted = true; }, {once:true});
    return new Response(new ReadableStream({
      start(controller) { controller.enqueue(new TextEncoder().encode("started")); },
      async cancel() { await gate; bodyCancelled = true; }
    }));
  }
  if (new URL(request.url).pathname === "/release") release();
  return Response.json({signalAborted, bodyCancelled});
}};`)
	a.request("hold", "/hold", "")
	if head, chunk := a.next(), a.next(); head.Frame != "head" || chunk.Frame != "chunk" {
		t.Fatalf("fetch did not stream: %+v %+v", head, chunk)
	}
	a.send(map[string]any{"__faas_cancel": "hold"})
	a.request("probe", "/check", "")
	probe := a.next()
	if probe.ID != "probe" {
		t.Fatalf("Fetch cancellation acknowledged before body cleanup: %+v", probe)
	}
	if got := responseObject(t, probe); got["signalAborted"] != true || got["bodyCancelled"] != false {
		t.Fatalf("Fetch cleanup state=%v", got)
	}
	a.request("release", "/release", "")
	seen := map[string]bool{}
	for len(seen) < 2 {
		line := a.next()
		switch {
		case line.Cancelled == "hold":
			seen["ack"] = true
		case line.ID == "release":
			seen["release"] = true
		default:
			t.Fatalf("unexpected Fetch frame after cancellation: %+v", line)
		}
	}
	a.request("check", "/check", "")
	if got := responseObject(t, a.next()); got["signalAborted"] != true || got["bodyCancelled"] != true {
		t.Fatalf("fetch resources survived cancel: %v", got)
	}
}

func TestSharedAdaptersExposeTrustedLifecycleContext(t *testing.T) {
	for _, tc := range []struct{ name, runtime, source string }{
		{"node", "node", `export function handler(event,ctx){return {body:{hook:ctx.lifecycle_hook}};}`},
		{"python async", "python", `async def handler(event,ctx):
    return {"body":{"hook":ctx.lifecycle_hook}}
`},
		{"python sync", "python", `def handler(event,ctx):
    return {"body":{"hook":ctx.lifecycle_hook,"cancelled":ctx.cancelled.is_set()}}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := adapterWithSource(t, tc.runtime, tc.source)
			a.send(map[string]any{"__faas_id": "trusted", "path": "/hook", "lifecycle_hook": "before_checkpoint"})
			if got := responseObject(t, a.next()); got["hook"] != "before_checkpoint" {
				t.Fatalf("trusted context missing: %v", got)
			}
			a.send(map[string]any{"__faas_id": "public", "method": "POST", "path": "/hook", "headers": map[string]string{"X-Faas-Before-Checkpoint": "1"}, "body_b64": base64.StdEncoding.EncodeToString([]byte(`{"lifecycle_hook":"before_checkpoint"}`))})
			if got := responseObject(t, a.next()); got["hook"] != "" {
				t.Fatalf("public fields forged context: %v", got)
			}
		})
	}
}
