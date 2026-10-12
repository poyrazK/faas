package rootfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// adapterFrame is one response line from a generated adapter (ADR-999).
type adapterFrame struct {
	Frame   string          `json:"frame"`
	Status  int             `json:"status"`
	Headers json.RawMessage `json:"headers"`
	BodyB64 string          `json:"body_b64"`
	Message string          `json:"message"`
	at      time.Duration
}

func (f adapterFrame) body(t *testing.T) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(f.BodyB64)
	if err != nil {
		t.Fatalf("frame body: %v", err)
	}
	return string(b)
}

func (f adapterFrame) header(t *testing.T, name string) any {
	t.Helper()
	var headers map[string]any
	if err := json.Unmarshal(f.Headers, &headers); err != nil {
		t.Fatalf("frame headers %s: %v", f.Headers, err)
	}
	return headers[name]
}

// adapterWorker drives a generated adapter as the runner's worker pool does.
type adapterWorker struct {
	t      *testing.T
	stdin  io.WriteCloser
	scan   *bufio.Scanner
	stderr *bytes.Buffer
	cmd    *exec.Cmd
}

func startAdapterWorker(t *testing.T, interpreter, adapter string) *adapterWorker {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, interpreter, adapter)
	cmd.Env = append(os.Environ(), "FAAS_PERSISTENT_WORKER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w := &adapterWorker{t: t, stdin: stdin, scan: bufio.NewScanner(stdout), stderr: stderr, cmd: cmd}
	if !w.scan.Scan() || !strings.Contains(w.scan.Text(), `"__faas_ready"`) {
		t.Fatalf("ready line = %q; stderr = %s", w.scan.Text(), stderr)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	return w
}

// send writes one request and reads frames until the response is complete:
// one whole-response line, or a streamed response through end or error.
func (w *adapterWorker) send(path string) []adapterFrame {
	w.t.Helper()
	env, _ := json.Marshal(map[string]any{"method": "GET", "path": path, "headers": map[string]string{}, "body_b64": ""})
	started := time.Now()
	if _, err := w.stdin.Write(append(env, '\n')); err != nil {
		w.t.Fatalf("write request: %v", err)
	}
	var frames []adapterFrame
	for w.scan.Scan() {
		var f adapterFrame
		if err := json.Unmarshal(w.scan.Bytes(), &f); err != nil {
			w.t.Fatalf("decode %q: %v", w.scan.Text(), err)
		}
		f.at = time.Since(started)
		frames = append(frames, f)
		if f.Frame == "" || f.Frame == "end" || f.Frame == "error" {
			return frames
		}
	}
	w.t.Fatalf("adapter stopped after %d frames: %v; stderr = %s", len(frames), w.scan.Err(), w.stderr)
	return nil
}

func frameKinds(frames []adapterFrame) []string {
	kinds := make([]string, len(frames))
	for i, f := range frames {
		kinds[i] = f.Frame
		if kinds[i] == "" {
			kinds[i] = "whole"
		}
	}
	return kinds
}

// adr: 999
func TestNodeFunctionAdapterStreams(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := `export default {
  async fetch(request) {
    const path = new URL(request.url).pathname;
    if (path === "/sse") {
      const body = new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode("data: one\n\n"));
          setTimeout(() => {
            controller.enqueue(new TextEncoder().encode("data: two\n\n"));
            controller.close();
          }, 300);
        },
      });
      return new Response(body, { headers: { "content-type": "text/event-stream" } });
    }
    if (path === "/whole") {
      const headers = new Headers([["content-type", "text/plain"], ["set-cookie", "a=1"], ["set-cookie", "b=2"]]);
      return new Response("whole body", { headers });
    }
    return new Response(null, { status: 204 });
  },
};
`
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.mjs")
	if err := os.WriteFile(adapter, []byte(nodeFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	w := startAdapterWorker(t, node, adapter)

	sse := w.send("/sse")
	if got := frameKinds(sse); !reflect.DeepEqual(got, []string{"head", "chunk", "chunk", "end"}) {
		t.Fatalf("/sse frames = %v; stderr = %s", got, w.stderr)
	}
	if sse[1].body(t) != "data: one\n\n" || sse[2].body(t) != "data: two\n\n" {
		t.Fatalf("/sse chunks = %q, %q", sse[1].body(t), sse[2].body(t))
	}
	if sse[1].at > 250*time.Millisecond {
		t.Fatalf("first chunk waited %v for the rest of the stream", sse[1].at)
	}

	// A body complete at once stays one response line, as before.
	whole := w.send("/whole")
	if got := frameKinds(whole); !reflect.DeepEqual(got, []string{"whole"}) {
		t.Fatalf("/whole frames = %v", got)
	}
	if whole[0].body(t) != "whole body" {
		t.Fatalf("/whole body = %q", whole[0].body(t))
	}
	if cookies := whole[0].header(t, "set-cookie"); !reflect.DeepEqual(cookies, []any{"a=1", "b=2"}) {
		t.Fatalf("/whole set-cookie = %v, want both", cookies)
	}

	empty := w.send("/empty")
	if len(empty) != 1 || empty[0].Status != 204 || empty[0].body(t) != "" {
		t.Fatalf("/empty = %+v", empty)
	}
}

// adr: 999
func TestNodeFunctionAdapterStreamsAsyncIterableAndAbortsOnError(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := `export async function handler(event) {
  async function* tokens(fail) {
    yield "alpha ";
    if (fail) throw new Error("model went away");
    yield "beta";
  }
  return {
    statusCode: 200,
    headers: { "content-type": "text/plain", "set-cookie": ["s=1", "t=2"] },
    body: tokens(event.path === "/fail"),
  };
}
`
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.mjs")
	if err := os.WriteFile(adapter, []byte(nodeFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	w := startAdapterWorker(t, node, adapter)

	ok := w.send("/ok")
	if got := frameKinds(ok); !reflect.DeepEqual(got, []string{"head", "chunk", "chunk", "end"}) {
		t.Fatalf("/ok frames = %v; stderr = %s", got, w.stderr)
	}
	if cookies := ok[0].header(t, "set-cookie"); !reflect.DeepEqual(cookies, []any{"s=1", "t=2"}) {
		t.Fatalf("head set-cookie = %v", cookies)
	}
	failed := w.send("/fail")
	if got := frameKinds(failed); !reflect.DeepEqual(got, []string{"head", "chunk", "error"}) {
		t.Fatalf("/fail frames = %v", got)
	}
	if !strings.Contains(failed[2].Message, "model went away") {
		t.Fatalf("error frame message = %q", failed[2].Message)
	}
	// The worker keeps serving after a failed stream.
	if again := w.send("/ok"); frameKinds(again)[0] != "head" {
		t.Fatalf("request after the failed stream = %v", frameKinds(again))
	}
}

// adr: 999
func TestPythonFunctionAdapterStreams(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	dir := t.TempDir()
	handler := `import asyncio

def handler(event, ctx):
    if event["path"] == "/async":
        async def tokens():
            yield "alpha "
            await asyncio.sleep(0.3)
            yield b"beta"
        return {"statusCode": 200, "headers": {"content-type": "text/plain"}, "body": tokens()}
    if event["path"] == "/fail":
        def broken():
            yield "partial"
            raise RuntimeError("upstream closed")
        return {"body": broken()}
    if event["path"] == "/whole":
        return {"statusCode": 200, "headers": {"set-cookie": ["a=1", "b=2"]}, "body": {"ok": True}}
    def lines():
        yield "one\n"
        yield "two\n"
    return {"statusCode": 200, "body": lines()}
`
	if err := os.WriteFile(filepath.Join(dir, ".faas-handler.py"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "handler.py")
	if err := os.WriteFile(adapter, []byte(pythonFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	w := startAdapterWorker(t, python, adapter)

	gen := w.send("/gen")
	if got := frameKinds(gen); !reflect.DeepEqual(got, []string{"head", "chunk", "chunk", "end"}) {
		t.Fatalf("/gen frames = %v; stderr = %s", got, w.stderr)
	}
	async := w.send("/async")
	if got := frameKinds(async); !reflect.DeepEqual(got, []string{"head", "chunk", "chunk", "end"}) {
		t.Fatalf("/async frames = %v; stderr = %s", got, w.stderr)
	}
	if async[1].at > 250*time.Millisecond || async[2].body(t) != "beta" {
		t.Fatalf("/async first chunk at %v, second %q", async[1].at, async[2].body(t))
	}
	failed := w.send("/fail")
	if got := frameKinds(failed); !reflect.DeepEqual(got, []string{"head", "chunk", "error"}) || !strings.Contains(failed[2].Message, "upstream closed") {
		t.Fatalf("/fail frames = %v (%q)", got, failed[len(failed)-1].Message)
	}
	whole := w.send("/whole")
	if got := frameKinds(whole); !reflect.DeepEqual(got, []string{"whole"}) || whole[0].body(t) != `{"ok": true}` {
		t.Fatalf("/whole = %v %q", got, whole[0].body(t))
	}
	if cookies := whole[0].header(t, "set-cookie"); !reflect.DeepEqual(cookies, []any{"a=1", "b=2"}) {
		t.Fatalf("/whole set-cookie = %v, want both", cookies)
	}
}
