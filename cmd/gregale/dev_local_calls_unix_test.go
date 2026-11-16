//go:build unix

package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// ADR-20261111150000 under `gregale dev --local`: a function calls its sibling
// through the SDK's wire contract (GREGALE_FUNCTIONS and the
// GREGALE_SERVICE_<NAME>_URL the local server sets), the sibling's input
// schema is checked by its local adapter, and a send answers 202 at once and
// runs the call in the background. The sender reads the send's result back
// and gets its completion (ADR-20261112160000).
func TestDevLocalFunctionsCallSiblings(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	record := filepath.Join(t.TempDir(), "users.log")
	done := filepath.Join(t.TempDir(), "done.log")
	caller := `import { appendFileSync } from "node:fs";
const fns = JSON.parse(process.env.GREGALE_FUNCTIONS || "{}");
const key = (slug) => "GREGALE_SERVICE_" + slug.toUpperCase().replace(/[^A-Z0-9]/g, "_") + "_URL";
async function users(input, send) {
  const headers = { "content-type": "application/json", "X-Gregale-Function-Call": "1" };
  if (send) headers["X-Gregale-Invoke"] = "async";
  const res = await fetch(process.env[key(fns.users)] + "/", { method: "POST", headers, body: JSON.stringify(input) });
  return { status: res.status, body: await res.json() };
}
async function sendAndWait() {
  const headers = { "content-type": "application/json", "X-Gregale-Function-Call": "1", "X-Gregale-Invoke": "async", "X-Gregale-On-Complete": "self" };
  const res = await fetch(process.env[key(fns.users)] + "/", { method: "POST", headers, body: JSON.stringify({ email: "waited@b" }) });
  const { id } = await res.json();
  const st = await fetch(process.env.GREGALE_PLATFORM_API_URL + "/v1/invocations/" + id + "?wait=10");
  return { status: st.status, body: await st.json() };
}
export default { async fetch(request) {
  if (request.headers.get("x-gregale-invocation-completion")) {
    appendFileSync(process.env.DONE, (await request.text()) + "\n");
    return new Response("ok");
  }
  const path = new URL(request.url).pathname;
  if (path === "/wait") return Response.json(await sendAndWait());
  if (path === "/fns") return Response.json(fns);
  if (path === "/call") return Response.json(await users({ email: "a@b" }, false));
  if (path === "/bad") return Response.json(await users({}, false));
  return Response.json(await users({ email: "sent@b" }, true));
} };
`
	callee := `import { appendFileSync } from "node:fs";
export default async function handler(event) {
  appendFileSync(process.env.RECORD, JSON.stringify(event.body) + "\n");
  return { statusCode: 200, body: { hello: event.body.email } };
}
`
	dir := writeDevLocalProject(t, map[string]string{
		"gregale.yaml": `functions:
  - name: web
    root: functions/web
    runtime: node24
    env: {DONE: ` + done + `}
  - name: users
    root: functions/users
    runtime: node24
    env: {RECORD: ` + record + `}
    input:
      type: object
      required: [email]
      properties:
        email: {type: string}
`,
		"functions/web/handler.js":     caller,
		"functions/web/package.json":   `{"type":"module"}`,
		"functions/users/handler.js":   callee,
		"functions/users/package.json": `{"type":"module"}`,
	})
	base, err := main.DevLocalListeningServerForTest(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.Host = "web.localhost"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, raw)
		}
		return doc
	}

	if fns := get("/fns"); fns["users"] != "users" {
		t.Fatalf("GREGALE_FUNCTIONS = %v, want users named", fns)
	}
	call := get("/call")
	if call["status"] != float64(200) || call["body"].(map[string]any)["hello"] != "a@b" {
		t.Fatalf("call = %v, want the sibling's output", call)
	}
	bad := get("/bad")
	if bad["status"] != float64(422) || bad["body"].(map[string]any)["code"] != "function_input_invalid" {
		t.Fatalf("bad input = %v, want 422 function_input_invalid from the local adapter", bad)
	}
	sent := get("/send")
	if sent["status"] != float64(202) || sent["body"].(map[string]any)["id"] == "" {
		t.Fatalf("send = %v, want 202 with an invocation id", sent)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, _ := os.ReadFile(record)
		if strings.Contains(string(got), `"sent@b"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sent call never ran; users saw:\n%s", got)
		}
		time.Sleep(100 * time.Millisecond)
	}

	waited := get("/wait")
	st, _ := waited["body"].(map[string]any)
	if waited["status"] != float64(200) || st["state"] != "completed" || st["done"] != true ||
		st["result"].(map[string]any)["hello"] != "waited@b" {
		t.Fatalf("wait = %v, want the completed send's result", waited)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		got, _ := os.ReadFile(done)
		if strings.Contains(string(got), `"status":"succeeded"`) && strings.Contains(string(got), `"waited@b"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the completion never reached the sender; it saw:\n%s", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
