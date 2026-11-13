//go:build unix

package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// devLocalWorkflowSDKStub speaks the code-workflow protocol for step, call and
// waitForEvent, standing in for @gregale/sdk-node/workflows so the test needs
// no SDK build.
const devLocalWorkflowSDKStub = `export function defineWorkflow(name, run) { return { name, run }; }
export function serveWorkflows(defs) {
  return async (request) => {
    if (request.headers.get("x-faas-workflow-mode") !== "code") return Response.json({ error: "refused" }, { status: 403 });
    const req = await request.json();
    const def = defs.find((d) => d.name === req.workflow);
    const ops = []; let seq = 0; const SUSPEND = Symbol("suspend");
    const claim = () => { const s = seq++; return { s, e: req.journal[s] }; };
    const wf = {
      async call(fn, input) {
        const { s, e } = claim();
        if (e && e.status === "succeeded") return e.output;
        if (e && e.status === "failed") throw new Error(e.error);
        if (!e) ops.push({ seq: s, name: fn, kind: "call", function: fn, input });
        throw SUSPEND;
      },
      async waitForEvent(name, o) {
        const event = (o && o.event) || name;
        const { s, e } = claim();
        if (e && e.status === "succeeded") return e.timed_out ? null : (e.output ?? null);
        if (!e) ops.push({ seq: s, name, kind: "event", event });
        throw SUSPEND;
      },
    };
    let result = { status: "running" };
    try { result = { status: "succeeded", output: await def.run(req.input, wf) }; }
    catch (err) { if (err !== SUSPEND) result = { status: "failed", error: String(err) }; }
    return Response.json({ protocol: req.protocol, ops, result });
  };
}
`

// ADR-20261113120824 §4: under `gregale dev --local` a function starts a
// workflow function with the platform's workflow-runs route; the local driver
// runs its wf.call against the sibling, parks it on an event the starter
// sends, and the starter's wait returns the run's output.
func TestDevLocalDrivesWorkflowFunctions(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	starter := `const api = () => process.env.GREGALE_PLATFORM_API_URL + "/v1/workflow-runs";
const json = async (res) => ({ status: res.status, body: res.status === 202 ? null : await res.json() });
export default { async fetch(request) {
  const url = new URL(request.url);
  const id = url.searchParams.get("id");
  if (url.pathname === "/start") return Response.json(await json(await fetch(api(), { method: "POST",
    headers: { "content-type": "application/json" }, body: JSON.stringify({ function: "orders", input: { email: "a@b" } }) })));
  if (url.pathname === "/signal") return Response.json(await json(await fetch(api() + "/" + id + "/events", { method: "POST",
    headers: { "content-type": "application/json" }, body: JSON.stringify({ event: "approved", payload: { ok: true } }) })));
  return Response.json(await json(await fetch(api() + "/" + id + "?wait=10")));
} };
`
	orders := `export default async function handler(input, wf) {
  const user = await wf.call("users", { email: input.email });
  const approval = await wf.waitForEvent("approval", { event: "approved" });
  return { user, approval };
}
`
	users := `export default async function handler(event) { return { statusCode: 200, body: { hello: event.body.email } }; }
`
	dir := writeDevLocalProject(t, map[string]string{
		"gregale.yaml": `functions:
  - name: web
    root: functions/web
    runtime: node24
  - name: orders
    root: functions/orders
    runtime: node24
    workflow: true
  - name: users
    root: functions/users
    runtime: node24
`,
		"package.json":                                                 `{"type":"module","dependencies":{"@gregale/sdk-node":"*"}}`,
		"functions/web/handler.js":                                     starter,
		"functions/web/package.json":                                   `{"type":"module"}`,
		"functions/orders/handler.js":                                  orders,
		"functions/orders/package.json":                                `{"type":"module"}`,
		"functions/orders/node_modules/@gregale/sdk-node/package.json": `{"name":"@gregale/sdk-node","type":"module","exports":{"./workflows":"./workflows.js"}}`,
		"functions/orders/node_modules/@gregale/sdk-node/workflows.js": devLocalWorkflowSDKStub,
		"functions/users/handler.js":                                   users,
		"functions/users/package.json":                                 `{"type":"module"}`,
	})
	base, err := main.DevLocalListeningServerForTest(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) (float64, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.Host = "web.localhost"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var doc struct {
			Status float64        `json:"status"`
			Body   map[string]any `json:"body"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, raw)
		}
		return doc.Status, doc.Body
	}
	status, run := get("/start")
	id, _ := run["id"].(string)
	if status != 201 || id == "" {
		t.Fatalf("start = %v %v", status, run)
	}
	if status, body := get("/signal?id=" + id); status != 202 {
		t.Fatalf("signal = %v %v", status, body)
	}
	status, done := get("/wait?id=" + id)
	output, _ := done["output"].(map[string]any)
	user, _ := output["user"].(map[string]any)
	approval, _ := output["approval"].(map[string]any)
	if status != 200 || done["status"] != "succeeded" || user["hello"] != "a@b" || approval["ok"] != true {
		t.Fatalf("wait = %v %v, want the run's output with the sibling's answer and the event", status, done)
	}
}
