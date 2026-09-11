// Package runnerparity owns the §4.9 envelope fixtures shared across the
// guest/runners/{node22,python312,go124} test suites. It is the first
// `internal/` package in the repo — Go's internal/ rule restricts import
// to packages rooted at guest/runners/, so the helper is reachable from
// each runner's main_test.go without changing production visibility.
//
// Production code MUST NOT import this package. The three runner main.go
// files stay in `package main` and are deliberately not extracted into a
// shared runtime package (see guest/runners/go124/main.go:9-14 — splitting
// keeps each runner buildable + lintable on its own). The parity helper
// is test-only: it owns the fake-script constants, the httptest server
// wiring, and the four post-handle assertions every runner's
// TestHandle_RoundTrip duplicates today.
package runnerparity

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal"
)

// Fake-handler filenames and interpreter argv values. Extracted as
// constants because each literal now appears in three-plus fixtures
// (round-trip, with-tail, and the issue #254 stderr variants), which
// trips goconst.
const (
	fileNode = "handler.js"
	filePy   = "handler.py"
	fileExec = "handler"

	interpNode = "node"
	interpPy   = "python3"
)

// FakeHandler is a self-contained executable that reads the §4.9 envelope
// from stdin and writes back a response envelope (status=200,
// X-Echo-Method, X-Echo-Path, body_b64="echo:<path>"). The Interpreter
// slice drives RunRoundTrip's Content-Type policy and PATH skip guard.
type FakeHandler struct {
	// Script is the file contents to write to disk.
	Script string
	// Filename is the file name on disk (e.g. "handler.js", "handler.py",
	// "handler"). The runner's spawn argv uses this path verbatim.
	Filename string
	// Interpreter drives the spawn argv:
	//   - ["node"]     → `node <Filename>` (node22)
	//   - ["python3"]  → `python3 <Filename>` (python312)
	//   - nil          → exec the file directly (go124; POSIX shebang)
	// RunRoundTrip skips the test if any non-nil interpreter binary is
	// not on PATH — mirroring the per-runtime guards the three runners
	// already have today.
	Interpreter []string
}

// FakeNodeScript returns a FakeHandler that runs under `node`. The script
// echoes method + path back via the §4.9 response envelope. Skipped at
// test time if `node` is not on PATH.
func FakeNodeScript() FakeHandler {
	return FakeHandler{
		Script: `#!/usr/bin/env node
let buf = '';
process.stdin.on('data', (c) => { buf += c; });
process.stdin.on('end', () => {
  const env = JSON.parse(buf);
  const out = {
    status: 200,
    headers: { "X-Echo-Method": env.method, "X-Echo-Path": env.path, "content-type": "application/json" },
    body_b64: Buffer.from("echo:" + env.path).toString("base64")
  };
  process.stdout.write(JSON.stringify(out));
});`,
		Filename:    fileNode,
		Interpreter: []string{interpNode},
	}
}

// FakeNodeScriptWithTail is the Node counterpart of
// FakeGoScriptWithTail. Writes a JSONL entry to envelope.TailPipePath
// before returning the response envelope, so the runner's
// drainTailHost reads the pipe after invokeHandler returns. The
// response envelope also echoes the tail_pipe_path and
// wait_until_sec it saw in the input envelope — the per-runtime
// parity test (RunWaitUntilEnvelopeRoundTrip) asserts the
// runner threaded the env var correctly into the envelope.
func FakeNodeScriptWithTail() FakeHandler {
	return FakeHandler{
		Script: `#!/usr/bin/env node
let buf = '';
process.stdin.on('data', (c) => { buf += c; });
process.stdin.on('end', () => {
  const env = JSON.parse(buf);
  if (env.tail_pipe_path) {
    require('fs').appendFileSync(env.tail_pipe_path,
      JSON.stringify({id: 'task-tail-1', wait: false}) + '\n');
  }
  const out = {
    status: 200,
    headers: {
      "X-Echo-Method": env.method,
      "X-Echo-Path": env.path,
      "X-Echo-TailPipe": env.tail_pipe_path || "",
      "X-Echo-WaitUntilSec": String(env.wait_until_sec || 0),
    },
    body_b64: Buffer.from("echo:" + env.path).toString("base64")
  };
  process.stdout.write(JSON.stringify(out));
});`,
		Filename:    fileNode,
		Interpreter: []string{interpNode},
	}
}

// FakePyScript returns a FakeHandler that runs under `python3`. The
// script echoes method + path back via the §4.9 response envelope.
// Skipped at test time if `python3` is not on PATH.
func FakePyScript() FakeHandler {
	return FakeHandler{
		Script: `#!/usr/bin/env python3
import sys, json, base64
env = json.loads(sys.stdin.read())
out = {
    "status": 200,
    "headers": {"X-Echo-Method": env["method"], "X-Echo-Path": env["path"]},
    "body_b64": base64.b64encode(("echo:" + env["path"]).encode()).decode(),
}
sys.stdout.write(json.dumps(out))
`,
		Filename:    filePy,
		Interpreter: []string{interpPy},
	}
}

// FakePyScriptWithTail writes a JSONL entry to envelope.TailPipePath
// before returning the response envelope. The runner's
// drainTailHost reads the pipe after invokeHandler returns. The
// response envelope also echoes the tail_pipe_path and
// wait_until_sec it saw in the input envelope — the per-runtime
// parity test (RunWaitUntilEnvelopeRoundTrip) asserts the
// runner threaded the env var correctly into the envelope.
func FakePyScriptWithTail() FakeHandler {
	return FakeHandler{
		Script: `#!/usr/bin/env python3
import sys, json, base64
env = json.loads(sys.stdin.read())
if env.get("tail_pipe_path"):
    with open(env["tail_pipe_path"], "a") as f:
        f.write(json.dumps({"id": "task-tail-1", "wait": False}) + "\n")
out = {
    "status": 200,
    "headers": {
        "X-Echo-Method": env["method"],
        "X-Echo-Path": env["path"],
        "X-Echo-TailPipe": env.get("tail_pipe_path", ""),
        "X-Echo-WaitUntilSec": str(env.get("wait_until_sec", 0)),
    },
    "body_b64": base64.b64encode(("echo:" + env["path"]).encode()).decode(),
}
sys.stdout.write(json.dumps(out))
`,
		Filename:    filePy,
		Interpreter: []string{interpPy},
	}
}

// FakeGoScript returns a FakeHandler that is a POSIX-sh script exec'd
// directly — matches how go124 invokes its handler
// (guest/runners/go124/main.go:117). No interpreter required; POSIX-sh
// is on every Linux/macOS.
func FakeGoScript() FakeHandler {
	return FakeHandler{
		Script: `#!/bin/sh
read -r env
method=$(printf '%s' "$env" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
path=$(printf '%s' "$env" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
printf '{"status":200,"headers":{"X-Echo-Method":"%s","X-Echo-Path":"%s"},"body_b64":"%s"}' \
  "$method" "$path" "$(printf '%s' "echo:$path" | base64)"
`,
		Filename:    fileExec,
		Interpreter: nil,
	}
}

// FakeGoScriptWithTail returns a FakeGoScript that additionally
// writes a JSONL entry to envelope.TailPipePath before returning
// the response envelope. The runner's tail host then reads the
// pipe and emits 0x04 DGRAMs. Used by the per-runtime
// TestHandle_WaitUntilEnvelopeRoundTrip to exercise the full
// drain end-to-end (the emit fails because the proxy isn't
// running in unit tests, but the runner's drain doesn't gate on
// the emit — TailErrors stays empty on the happy path).
//
// The response also echoes the tail_pipe_path and wait_until_sec
// it saw in the input envelope (the per-runtime parity test
// asserts the runner threaded the env var correctly into the
// envelope — issue #667 review item #12).
func FakeGoScriptWithTail() FakeHandler {
	return FakeHandler{
		Script: `#!/bin/sh
read -r env
method=$(printf '%s' "$env" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
path=$(printf '%s' "$env" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
# tail_pipe_path + wait_until_sec from the envelope (issue #667 / ADR-078).
tail_pipe=$(printf '%s' "$env" | sed -n 's/.*"tail_pipe_path":"\([^"]*\)".*/\1/p')
wait_until_sec=$(printf '%s' "$env" | sed -n 's/.*"wait_until_sec":\([0-9][0-9]*\).*/\1/p')
if [ -n "$tail_pipe" ]; then
  printf '{"id":"task-tail-1","wait":false}\n' >> "$tail_pipe"
fi
printf '{"status":200,"headers":{"X-Echo-Method":"%s","X-Echo-Path":"%s","X-Echo-TailPipe":"%s","X-Echo-WaitUntilSec":"%s"},"body_b64":"%s"}' \
  "$method" "$path" "$tail_pipe" "$wait_until_sec" "$(printf '%s' "echo:$path" | base64)"
`,
		Filename:    fileExec,
		Interpreter: nil,
	}
}

// WriteMaterialize writes the FakeHandler's Script to a fresh temp file
// and returns the absolute path. The caller passes the returned path to
// its local `handle` function as the handler path. Helper-exposed so
// the per-runtime TestHandle_RoundTrip can keep the production
// visibility of `handle` — the runner's main_test.go wraps `handle` in
// a closure that captures this path.
func (f FakeHandler) WriteMaterialize(t *testing.T) string {
	t.Helper()
	if len(f.Interpreter) > 0 {
		if _, err := exec.LookPath(f.Interpreter[0]); err != nil {
			t.Skipf("%s not on PATH; skipping runtime round-trip", f.Interpreter[0])
		}
	}
	dir := t.TempDir()
	script := dir + "/" + f.Filename
	if err := os.WriteFile(script, []byte(f.Script), 0o755); err != nil {
		t.Fatalf("write handler: %v", err)
	}
	return script
}

// RunRoundTrip wires up an httptest.Server that delegates / to handler,
// fires GET /hello?x=1, and asserts: status=200, X-Echo-Method=="GET",
// X-Echo-Path=="/hello", body contains "echo:/hello". When
// fake.Interpreter == []string{"node"}, the helper also asserts that the
// handler's application/json content type survives the runner boundary.
//
// The handler closure is invoked with the materialized handler script
// path AND a RunnerSignal so the runner's per-package `handle`
// signature (added an issue #470 / PR #470-FU-B fourth arg) stays
// unchanged — production visibility of `handle` is preserved.
// RunRoundTrip wires up an httptest.Server that delegates / to handler,
// fires GET /hello?x=1, and asserts: status=200, X-Echo-Method=="GET",
// X-Echo-Path=="/hello", body contains "echo:/hello". When
// fake.Interpreter == []string{"node"}, the helper also asserts that the
// handler's application/json content type survives the runner boundary.
//
// The handler closure is invoked with the materialized handler script
// path AND a RunnerSignal so the runner's per-package `handle`
// signature (added an issue #470 / PR #470-FU-B fourth arg) stays
// unchanged — production visibility of `handle` is preserved.
//
// PR 3 (issue #667 follow-up): the runner's `handle` signature
// grew two new args (tailWaitSec, tailPipePath). The helper's
// signature deliberately keeps the 4-arg shape so the 5
// round-trip smoke tests don't need to know about the tail
// primitive — the wrapper in each runner's main_test.go
// bridges the signature change with 0/empty defaults (feature
// disabled). See guest/runners/go124/main_test.go for the
// pattern.
func RunRoundTrip(t *testing.T, fake FakeHandler, handler func(http.ResponseWriter, *http.Request, string, *internal.RunnerSignal, int, string)) {
	t.Helper()
	script := fake.WriteMaterialize(t)

	// Issue #470 / PR #470-FU-B: pass a real signal so the
	// production code path is exercised end-to-end. The signal
	// tries to dial the proxy at /run/guest-init/framework-ready.sock;
	// on a test box that file doesn't exist, the signal's
	// sync.Once absorbs the error and the runner's request
	// handling proceeds normally. We pass a per-runner id so the
	// parity test asserts the wire-line shape ("ok\n" / "err ...").
	signal := internal.NewRunnerSignal("parity", time.Now())

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, script, signal, 0, "")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hello?x=1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Echo-Method"); got != "GET" {
		t.Errorf("X-Echo-Method = %q, want GET", got)
	}
	if got := resp.Header.Get("X-Echo-Path"); got != "/hello" {
		t.Errorf("X-Echo-Path = %q, want /hello", got)
	}
	// Node handlers frequently return JSON. Both supported Node runners must
	// preserve that explicit value instead of replacing it with their binary
	// fallback. The lower-case key in FakeNodeScript also pins case-insensitivity.
	if len(fake.Interpreter) > 0 && fake.Interpreter[0] == "node" {
		if got := resp.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	}
	body := new(bytes.Buffer)
	if _, err := io.Copy(body, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(body.String(), "echo:/hello") {
		t.Errorf("body = %q, want contains echo:/hello", body.String())
	}
}

// AssertEnvelopeJSONTags marshals an envelope-shaped value and asserts
// the §4.9 JSON tags spell out method/path/headers/query/body_b64, and
// that the encoded body_b64 decodes back to wantBody via
// base64.StdEncoding. The caller passes the runner's local `envelope`
// struct as `any` and the original byte slice that was encoded into
// envelope.BodyB64 — the helper never imports a runner package, so
// the value flows through the `encoding/json` reflection path. Replaces
// the three near-identical TestEnvelopeRoundTrip bodies (one per
// runner).
//
// The body_b64 round-trip is the load-bearing check: a swap from
// base64.StdEncoding to base64.URLEncoding (or vice versa) would
// survive a tag-presence check but fail this decode step.
func AssertEnvelopeJSONTags(t *testing.T, env any, wantBody []byte) {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Tag presence: a typo on body_b64 (e.g. `json:"body"` vs
	// `json:"body_b64"`) would break the runner's decoder without
	// compile-time signal. The other tags are caught by
	// RunRoundTrip's headers / path assertions.
	if !bytes.Contains(b, []byte(`"body_b64":"`)) {
		t.Errorf("body_b64 tag missing or empty: %s", b)
	}
	if !bytes.Contains(b, []byte(`"method":"`)) {
		t.Errorf("method tag missing: %s", b)
	}
	if !bytes.Contains(b, []byte(`"path":"`)) {
		t.Errorf("path tag missing: %s", b)
	}
	// Issue #667 / ADR-078: pin the waitUntil envelope field tags.
	// A typo (e.g. `json:"wait_until"`) would break the runner's
	// tail host silently — the field would marshal as 0 and the
	// handler's waitUntil calls would no-op. This pin is the only
	// compile-time signal for the wire spelling.
	if !bytes.Contains(b, []byte(`"wait_until_sec":`)) {
		t.Errorf("wait_until_sec tag missing: %s", b)
	}
	if !bytes.Contains(b, []byte(`"tail_pipe_path":"`)) {
		t.Errorf("tail_pipe_path tag missing or empty: %s", b)
	}
	// Decode round-trip: pull the body_b64 string out of the marshaled
	// JSON (the helper is reflection-free) and decode via StdEncoding.
	// A StdEncoding ↔ URLEncoding swap on the encoding side would
	// emit a different wire string for the same input bytes; this
	// step pins the encoding choice the runner's decoder expects.
	if wantBody != nil {
		bodyB64 := extractBodyB64(t, b)
		AssertBase64BodyRoundTrip(t, bodyB64, wantBody)
	}
}

// AssertBase64BodyRoundTrip decodes b64 via base64.StdEncoding and
// compares to want. The runner's decode path uses StdEncoding (per the
// §4.9 envelope contract); the caller's encoding side must match.
func AssertBase64BodyRoundTrip(t *testing.T, b64 string, want []byte) {
	t.Helper()
	got, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("decoded = %q, want %q", got, want)
	}
}

// extractBodyB64 pulls the body_b64 string value out of a marshaled
// envelope JSON blob without depending on the runner's struct shape.
// The helper deliberately avoids reflection so a runner-side field
// rename does not break parity tests.
func extractBodyB64(t *testing.T, marshaled []byte) string {
	t.Helper()
	var probe struct {
		BodyB64 string `json:"body_b64"`
	}
	if err := json.Unmarshal(marshaled, &probe); err != nil {
		t.Fatalf("unmarshal envelope for body_b64 probe: %v", err)
	}
	return probe.BodyB64
}

// RunWaitUntilEnvelopeRoundTrip wires up an httptest.Server that
// delegates / to handler, fires GET /hello?x=1, and asserts:
//
//  1. The runner's tail host reads the JSONL pipe at
//     envelope.TailPipePath (FakeGoScriptWithTail writes one line).
//  2. The runner's drain returns within the per-task ceiling
//     (eliminate per-runtime infinite-drain bugs).
//  3. The response envelope is intact (status=200, body unchanged).
//
// Unlike RunRoundTrip, this helper passes NON-ZERO tailWaitSec and
// a non-empty tailPipePath so the runner's drainTailHost path is
// exercised end-to-end. The 0x04 DGRAM emit fails in unit tests
// (the proxy isn't running) — that's expected; the runner keeps
// draining and the response stays clean (TailErrors empty on the
// happy path because the runner's taskFn is a no-op, so the
// per-task context doesn't fire).
//
// Per-runtime caller pattern (mirrors RunRoundTrip):
//
//	func TestHandle_WaitUntilEnvelopeRoundTrip(t *testing.T) {
//	    runnerparity.RunWaitUntilEnvelopeRoundTrip(t, fake, handle)
//	}
//
// PR 3 (issue #667 follow-up): this is the per-runtime counterpart
// to the hermetic TestParity_AllRuntimesHonorWaitUntil file-walk
// (that one pins the structural shape; this one pins the
// per-runtime execution).
func RunWaitUntilEnvelopeRoundTrip(t *testing.T, fake FakeHandler, handler func(http.ResponseWriter, *http.Request, string, *internal.RunnerSignal, int, string)) {
	t.Helper()
	script := fake.WriteMaterialize(t)

	signal := internal.NewRunnerSignal("parity-tail", time.Now())

	// Per-test tempdir for the JSONL pipe. The runner's
	// drainTailHost reads it after invokeHandler returns;
	// the FakeHandlerWithTail writes one line before exiting.
	dir := t.TempDir()
	pipePath := filepath.Join(dir, "tail.jsonl")

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 30s ceiling — long enough that the test never blocks
		// on the per-task timeout (the runner's taskFn is a
		// no-op, so contexts don't fire). Short enough that a
		// hung drain would surface as a test timeout.
		handler(w, r, script, signal, 30, pipePath)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	start := time.Now()
	resp, err := http.Get(srv.URL + "/hello?x=1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	elapsed := time.Since(start)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Echo-Method"); got != "GET" {
		t.Errorf("X-Echo-Method = %q, want GET", got)
	}
	if got := resp.Header.Get("X-Echo-Path"); got != "/hello" {
		t.Errorf("X-Echo-Path = %q, want /hello", got)
	}
	// Pin the env-var → envelope round-trip: the FakeHandler's
	// JSON output echoes back the tail_pipe_path it observed.
	// If the runner forgot to thread the env var into the
	// envelope (issue #667 review item #12), this assertion
	// catches the drift. The handler subprocess reads its
	// envelope from stdin and writes the tail_pipe_path value
	// into the X-Echo-TailPipe response header.
	if got := resp.Header.Get("X-Echo-TailPipe"); got != pipePath {
		t.Errorf("X-Echo-TailPipe = %q, want %q (envelope.TailPipePath must equal the FAAS_TAIL_PIPE_PATH the runner was started with)", got, pipePath)
	}
	if got := resp.Header.Get("X-Echo-WaitUntilSec"); got != "30" {
		t.Errorf("X-Echo-WaitUntilSec = %q, want 30 (envelope.WaitUntilSec must thread the per-task ceiling)", got)
	}
	body := new(bytes.Buffer)
	if _, err := io.Copy(body, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(body.String(), "echo:/hello") {
		t.Errorf("body = %q, want contains echo:/hello", body.String())
	}
	// Drain must complete in << the 30s ceiling. A regression
	// where the runner's drainTailHost hangs would surface here.
	if elapsed > 5*time.Second {
		t.Errorf("drain did not return promptly: elapsed=%v, ceiling=5s", elapsed)
	}
	// The JSONL pipe was read by the runner's drain. If the
	// runner never touched it, the file stays empty (the
	// FakeHandlerWithTail writes one line, the runner reads it).
	// We assert the file exists — the runner's ReadPipe
	// tolerates the pipe being missing (treats as no-tasks),
	// so an empty file is the "happy path" state.
	if _, err := os.Stat(pipePath); err != nil {
		t.Errorf("tail pipe stat: %v (runner may have unlinked the pipe before stat)", err)
	}
}

// FakeScriptWritingStderr returns a shell FakeHandler that writes one
// line to stderr before emitting the §4.9 response envelope on stdout.
// Interpreter is nil (POSIX shebang, executed directly) — this is the
// go124-shaped fake, for runners that exec the handler file directly.
//
// Node and Python runners spawn `node <file>` / `python3 <file>` and
// would try to parse this shell script as source, so they use
// FakeNodeScriptWritingStderr / FakePyScriptWritingStderr instead.
//
// The stdout envelope is byte-identical to FakeGoScript's, so a caller
// can assert BOTH halves of the issue #254 contract in one round trip:
// stderr escapes to the host, and stdout still decodes cleanly.
//
// stderrLine is base64-encoded in Go and decoded inside the script
// (printf | base64 -d) so the marker is never interpreted as shell
// syntax. Today every caller passes a static ASCII marker, but the
// helper is public — any future caller passing a marker containing
// `"`, `$`, or backticks would otherwise produce a malformed fake
// (or, with `$(...)`, execute test-host commands).
func FakeScriptWritingStderr(stderrLine string) FakeHandler {
	enc := base64.StdEncoding.EncodeToString([]byte(stderrLine))
	return FakeHandler{
		Script: `#!/bin/sh
read -r env
printf '%s\n' "$(printf '%s' '` + enc + `' | base64 -d)" >&2
method=$(printf '%s' "$env" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
path=$(printf '%s' "$env" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
printf '{"status":200,"headers":{"X-Echo-Method":"%s","X-Echo-Path":"%s"},"body_b64":"%s"}' \
  "$method" "$path" "$(printf '%s' "echo:$path" | base64)"
`,
		Filename:    fileExec,
		Interpreter: nil,
	}
}

// FakeNodeScriptWritingStderr is the Node counterpart of
// FakeScriptWritingStderr: writes one line to stderr via
// console.error, then emits the §4.9 envelope on stdout.
//
// stderrLine is base64-encoded in Go and decoded inside the script
// (Buffer.from(..., 'base64').toString()), so the marker is never
// interpreted as JavaScript syntax. Backticks in the marker would
// otherwise enable template-literal interpolation (`${process.exit(1)}`);
// double quotes would let a marker break out of the source string.
// The base64 round trip makes the script safe for any UTF-8 marker.
func FakeNodeScriptWritingStderr(stderrLine string) FakeHandler {
	enc := base64.StdEncoding.EncodeToString([]byte(stderrLine))
	return FakeHandler{
		Script: `#!/usr/bin/env node
let buf = '';
process.stdin.on('data', (c) => { buf += c; });
process.stdin.on('end', () => {
  const env = JSON.parse(buf);
  console.error(Buffer.from('` + enc + `', 'base64').toString());
  const out = {
    status: 200,
    headers: { "X-Echo-Method": env.method, "X-Echo-Path": env.path },
    body_b64: Buffer.from("echo:" + env.path).toString("base64")
  };
  process.stdout.write(JSON.stringify(out));
});`,
		Filename:    fileNode,
		Interpreter: []string{interpNode},
	}
}

// FakePyScriptWritingStderr is the Python counterpart of
// FakeScriptWritingStderr: writes one line to sys.stderr, then
// emits the §4.9 envelope on stdout.
//
// stderrLine is base64-encoded in Go and decoded inside the script
// (base64.b64decode), so the marker is never interpreted as Python
// source. A marker containing `") or __import__("os").system(...)`
// would otherwise break out of the literal and execute test-host
// commands; the base64 round trip makes the script safe for any
// UTF-8 marker.
func FakePyScriptWritingStderr(stderrLine string) FakeHandler {
	enc := base64.StdEncoding.EncodeToString([]byte(stderrLine))
	return FakeHandler{
		Script: `#!/usr/bin/env python3
import sys, json, base64
env = json.loads(sys.stdin.read())
print(base64.b64decode('` + enc + `').decode(), file=sys.stderr)
out = {
    "status": 200,
    "headers": {"X-Echo-Method": env["method"], "X-Echo-Path": env["path"]},
    "body_b64": base64.b64encode(("echo:" + env["path"]).encode()).decode(),
}
sys.stdout.write(json.dumps(out))
`,
		Filename:    filePy,
		Interpreter: []string{interpPy},
	}
}

// RunStderrReachesHost pins the issue #254 fix: a runner MUST tee the
// customer handler's stderr to its own os.Stderr, because the runner's
// os.Stderr is inherited from guest-init (PID1), which tees it into the
// supervisor ring buffer that vmmd drains into pkg/fcvm/logbuf and the
// customer reads over `faas logs`.
//
// Before the fix, every runner wired `cmd.Stderr = &stderr` (a bare
// in-memory bytes.Buffer that was only ever folded into the exec-error
// string). Customer stderr therefore never left the microVM, and
// `faas logs` showed platform noise only.
//
// The helper redirects the process-wide os.Stderr through an os.Pipe
// for the duration of the round trip, then asserts:
//
//  1. the handler's stderr line appears on the captured os.Stderr, AND
//  2. the response still decodes (status 200 + echo body) — i.e. the
//     tee did not corrupt the protocol-bearing stdout path.
//
// Assertion (2) is the load-bearing half: a contributor who "fixes"
// logging by teeing stdout as well would satisfy (1) and break (2),
// because the §4.9 envelope is json.Unmarshal'd from that same buffer.
//
// os.Stderr is process-global, so this helper must not run under
// t.Parallel(). No runner test calls t.Parallel() today.
func RunStderrReachesHost(t *testing.T, fake FakeHandler, wantLine string, handler func(http.ResponseWriter, *http.Request, string, *internal.RunnerSignal, int, string)) {
	t.Helper()
	script := fake.WriteMaterialize(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	// Drain concurrently: a handler that writes more than the pipe
	// buffer (64 KiB on Linux/macOS) would deadlock on a lazy read.
	// The drain goroutine reads until EOF, which is generated when
	// we close w below.
	type readResult struct {
		out []byte
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		var buf bytes.Buffer
		_, cErr := io.Copy(&buf, r)
		done <- readResult{out: buf.Bytes(), err: cErr}
	}()
	// restoreStderr reverts os.Stderr to the captured original so any
	// t.Fatalf / t.Errorf below reports to the real stderr rather than
	// into the captured pipe. Deferred so a panic in httptest.NewServer,
	// http.Get, or anything between mutation and the success-path close
	// still unwinds the process-global — otherwise the next test in
	// the same `go test` run inherits a leaked pipe write end.
	//
	// It does NOT close w: closing w must happen AFTER the handler's
	// subprocess has finished writing, otherwise the drain goroutine
	// returns early on EOF and we miss bytes. The close is paired
	// with the drain's <-done receive below.
	restoreStderr := func() { os.Stderr = orig }
	defer restoreStderr()

	signal := internal.NewRunnerSignal("parity-stderr", time.Now())
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
		handler(rw, req, script, signal, 0, "")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hello?x=1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body := new(bytes.Buffer)
	_, copyErr := io.Copy(body, resp.Body)
	_ = resp.Body.Close()
	if copyErr != nil {
		t.Fatalf("read body: %v", copyErr)
	}

	// Close the pipe write end so the drain goroutine's io.Copy sees
	// EOF and exits. Safe: the handler's subprocess has already
	// returned (we just read its response body), so no further writes
	// are coming through os.Stderr.
	_ = w.Close()
	got := <-done
	if got.err != nil {
		t.Fatalf("drain captured stderr: %v", got.err)
	}

	// (1) customer stderr escaped to the host.
	if !strings.Contains(string(got.out), wantLine) {
		t.Errorf("customer stderr did not reach the runner's os.Stderr.\n"+
			"want line: %q\ncaptured:  %q\n"+
			"This is the issue #254 regression: cmd.Stderr must be "+
			"io.MultiWriter(&stderr, os.Stderr), not a bare buffer — "+
			"otherwise customer stack traces never leave the microVM.",
			wantLine, string(got.out))
	}

	// (2) the protocol-bearing stdout path still decodes.
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 — the response envelope failed to decode; "+
			"did stdout get teed as well? stdout is protocol-bearing (§4.9).",
			resp.StatusCode)
	}
	if !strings.Contains(body.String(), "echo:/hello") {
		t.Errorf("body = %q, want contains echo:/hello — the §4.9 envelope did not "+
			"survive the round trip; stdout must stay a bare buffer.", body.String())
	}
}
