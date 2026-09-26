package templates

import (
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

func runNodeStarter(t *testing.T, name, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is not installed")
	}
	dest := filepath.Join(t.TempDir(), name)
	if err := Materialize(name, dest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, filepath.Join(dest, "handler.js"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s starter failed: %v\n%s", name, err, output)
	}
}

func TestEventWorkerStarterReadsParsedAndRawEnvelope(t *testing.T) {
	runNodeStarter(t, "event-worker", `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { handler } = await import(pathToFileURL(process.argv[1]).href);
const envelope = { id: "event-1", source: "billing.test", type: "invoice.paid", data: { amount: 150 } };
for (const body of [envelope, JSON.stringify(envelope)]) {
  const logs = [];
  const result = await handler({ body }, { log: { info: (...args) => logs.push(args) } });
  assert.equal(result.statusCode, 202);
  assert.deepEqual(logs[0][1], { event_id: "event-1", source: "billing.test", type: "invoice.paid" });
}
await assert.rejects(() => handler({ body: { data: {} } }, { log: { info() {} } }), /missing id, source, or type/);
await assert.rejects(() => handler({ body: "not-json" }, { log: { info() {} } }), /not valid JSON/);
`)
}

func TestQueueWorkerStarterReadsParsedAndRawPayload(t *testing.T) {
	runNodeStarter(t, "queue-worker", `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { handler } = await import(pathToFileURL(process.argv[1]).href);
for (const body of [{ job_id: "order-123" }, JSON.stringify({ job_id: "order-123" })]) {
  const logs = [];
  const result = await handler({ body }, { invocation_id: "inv-1", log: { info: (...args) => logs.push(args) } });
  assert.equal(result.statusCode, 202);
  assert.deepEqual(JSON.parse(result.body), { accepted: true, job_id: "order-123" });
  assert.equal(logs[0][1].job_id, "order-123");
}
await assert.rejects(() => handler({ body: "not-json" }, { invocation_id: "inv-1", log: { info() {} } }), /not valid JSON/);
`)
}

// TestCronWorkerStarterVerifiesQStashJWT — the starter verified a "v1=<hex>"
// HMAC keyed by the QStash API token over event.body, but QStash signs
// deliveries with an HS256 JWT keyed by its signing keys, and the event form
// hands JSON bodies over already parsed (the raw bytes were ""). No genuine
// QStash delivery could pass. The Fetch form sees the exact bytes.
func TestCronWorkerStarterVerifiesQStashJWT(t *testing.T) {
	runNodeStarter(t, "cron-worker", `
import assert from "node:assert/strict";
import crypto from "node:crypto";
import { pathToFileURL } from "node:url";
process.env.QSTASH_CURRENT_SIGNING_KEY = "sig_current";
process.env.QSTASH_NEXT_SIGNING_KEY = "sig_next";
process.env.UPSTASH_REDIS_REST_URL = "https://redis.example";
process.env.UPSTASH_REDIS_REST_TOKEN = "redis-token";
let redisCalls = 0;
globalThis.fetch = async () => { redisCalls++; return new Response(JSON.stringify({ result: redisCalls }), { status: 200 }); };
const mod = await import(pathToFileURL(process.argv[1]).href);
const b64u = (b) => Buffer.from(b).toString("base64url");
const now = Math.floor(Date.now() / 1000);
function sign(key, body, claims = {}) {
  const header = b64u(JSON.stringify({ alg: "HS256", typ: "JWT" }));
  const payload = b64u(JSON.stringify({ iss: "Upstash", sub: "https://x.gregale.dev", nbf: now - 5, exp: now + 300,
    body: b64u(crypto.createHash("sha256").update(body).digest()), ...claims }));
  const sig = b64u(crypto.createHmac("sha256", key).update(header + "." + payload).digest());
  return header + "." + payload + "." + sig;
}
async function call(body, signature) {
  const req = new Request("http://faas.local/", { method: "POST", body,
    headers: { "content-type": "application/json", "upstash-signature": signature, "x-faas-invocation-id": "inv-1" } });
  return mod.default.fetch(req, {}, {});
}
const body = JSON.stringify({ task: "tick", n: 1 });
for (const key of ["sig_current", "sig_next"]) {
  const res = await call(body, sign(key, body));
  assert.equal(res.status, 200, key);
  assert.equal((await res.json()).ok, true);
}
assert.equal((await call(body + " ", sign("sig_current", body))).status, 401, "tampered body");
assert.equal((await call(body, sign("wrong_key", body))).status, 401, "wrong key");
assert.equal((await call(body, sign("sig_current", body, { exp: now - 3600, nbf: now - 7200 }))).status, 401, "expired");
assert.equal((await call(body, sign("sig_current", body, { iss: "someone" }))).status, 401, "issuer");
assert.equal((await call(body, "v1=" + crypto.createHmac("sha256", "sig_current").update(body).digest("hex"))).status, 401, "legacy v1 format");
assert.equal(redisCalls, 2, "only verified deliveries touch Redis");
`)
}

// TestAIChatStarterUsesAServedClaudeModel — the ai-chat starter defaulted to
// claude-3-5-sonnet-latest, a retired model: every chat request made with an
// ANTHROPIC_API_KEY failed until the customer overrode ANTHROPIC_MODEL. Pin the
// default away from retired Claude 3.x ids, and keep max_tokens large enough
// for current models' adaptive thinking to leave room for the reply.
func TestAIChatStarterUsesAServedClaudeModel(t *testing.T) {
	for _, name := range []string{"ai-chat/handler.js", "ai-chat/README.md"} {
		body, err := FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, retired := range []string{"claude-3-5-sonnet", "claude-3-opus", "claude-3-sonnet", "claude-3-haiku"} {
			if strings.Contains(string(body), retired) {
				t.Errorf("%s references retired model %q", name, retired)
			}
		}
	}
	handler, err := FS.ReadFile("ai-chat/handler.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(handler)
	for _, want := range []string{`process.env.ANTHROPIC_MODEL || "claude-opus-5"`, "max_tokens: 16000", `body.fallbacks = "default"`, "server-side-fallback-2026-07-01", `stop_reason === "refusal"`} {
		if !strings.Contains(src, want) {
			t.Errorf("ai-chat/handler.js missing %q", want)
		}
	}
}

// TestGoFunctionStarterEncodesRequestFields — the Go starter spliced the
// request path and method into a JSON string by concatenation, so a path
// containing a quote or backslash produced invalid (or field-injected) JSON.
func TestGoFunctionStarterEncodesRequestFields(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain is not installed")
	}
	dest := filepath.Join(t.TempDir(), "function-go")
	if err := Materialize("function-go", dest); err != nil {
		t.Fatal(err)
	}
	path := `/a"b\c","admin":true,"x":"`
	envelope, err := json.Marshal(map[string]any{"method": "POST", "path": path, "headers": map[string]string{}, "query": "", "body_b64": ""})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "run", filepath.Join(dest, "handler.go"))
	cmd.Dir = dest
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "GOFLAGS=")
	cmd.Stdin = bytes.NewReader(envelope)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run starter: %v", err)
	}
	var resp struct {
		Status  int    `json:"status"`
		BodyB64 string `json:"body_b64"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("response envelope: %v (%s)", err, out)
	}
	raw, err := base64.StdEncoding.DecodeString(resp.BodyB64)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("starter body is not valid JSON: %v (%s)", err, raw)
	}
	if body["path"] != path || body["method"] != "POST" || body["admin"] != nil || len(body) != 3 {
		t.Fatalf("body = %v, want exactly {ok, path, method} echoing the request", body)
	}
}

// TestS3UploaderStarterKeepsRawBodyForEveryContentType — the uploader parsed
// bodies with express.text(), which only handles text/plain. The README's own
// `curl --data` example (form-encoded) and every binary upload reached the
// route as an empty object, so PutObject failed. Express itself is not
// installed in CI, so this pins the parser configuration statically; the
// behaviour was checked against express 4: express.text() yields {} for
// application/x-www-form-urlencoded and image/png.
func TestS3UploaderStarterKeepsRawBodyForEveryContentType(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "s3-uploader")
	if err := Materialize("s3-uploader", dest); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(dest, "handler.js"))
	if err != nil {
		t.Fatal(err)
	}
	var codeLines []string
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			codeLines = append(codeLines, line)
		}
	}
	code := strings.Join(codeLines, "\n")
	if strings.Contains(code, "express.text(") || strings.Contains(code, "express.json(") || strings.Contains(code, "express.urlencoded(") {
		t.Fatal("s3-uploader parses bodies with a content-type-specific parser; uploads of other types arrive empty")
	}
	if !strings.Contains(code, "express.raw({ type: () => true") {
		t.Fatal("s3-uploader must read every request body as raw bytes (express.raw with a match-all type)")
	}
}
