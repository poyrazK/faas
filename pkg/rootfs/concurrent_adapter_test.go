package rootfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// sharedAdapter drives a generated adapter as a shared worker (ADR-1342):
// requests carry __faas_id and the adapter answers them as they finish.
type sharedAdapter struct {
	t      *testing.T
	stdin  io.Writer
	lines  chan []byte
	stderr *adapterLog
}

type adapterLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *adapterLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *adapterLog) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type sharedLine struct {
	ID        string            `json:"__faas_id"`
	Cancelled string            `json:"__faas_cancelled"`
	Frame     string            `json:"frame"`
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	BodyB64   string            `json:"body_b64"`
	Ready     bool              `json:"__faas_ready"`
	Slots     int               `json:"concurrency"`
}

func startSharedAdapter(t *testing.T, interpreter, adapter string, concurrency int) (*sharedAdapter, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, interpreter, adapter)
	cmd.Env = append(os.Environ(), "FAAS_PERSISTENT_WORKER=1", "FAAS_FUNCTION_CONCURRENCY="+strconv.Itoa(concurrency))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	a := &sharedAdapter{t: t, stdin: stdin, lines: make(chan []byte, 64), stderr: &adapterLog{}}
	cmd.Stderr = a.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	go func() {
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 64*1024), 16<<20)
		for scan.Scan() {
			a.lines <- append([]byte(nil), scan.Bytes()...)
		}
		close(a.lines)
	}()
	ready := a.next()
	if !ready.Ready {
		t.Fatalf("first line is not the ready handshake: %+v; stderr=%s", ready, a.stderr)
	}
	return a, ready.Slots
}

func (a *sharedAdapter) send(v map[string]any) {
	a.t.Helper()
	line, _ := json.Marshal(v)
	if _, err := a.stdin.Write(append(line, '\n')); err != nil {
		a.t.Fatalf("write: %v", err)
	}
}

func (a *sharedAdapter) request(id, path, query string) {
	a.send(map[string]any{"__faas_id": id, "method": "GET", "path": path, "query": query, "headers": map[string]string{}, "body_b64": ""})
}

func (a *sharedAdapter) next() sharedLine {
	a.t.Helper()
	select {
	case line, ok := <-a.lines:
		if !ok {
			a.t.Fatalf("adapter exited; stderr=%s", a.stderr)
		}
		var got sharedLine
		if err := json.Unmarshal(line, &got); err != nil {
			a.t.Fatalf("line %s: %v", line, err)
		}
		if got.ID != "" && !bytes.HasPrefix(line, []byte(`{"__faas_id":"`+got.ID+`"`)) {
			a.t.Fatalf("id is not the line's first field: %s", line)
		}
		return got
	case <-time.After(10 * time.Second):
		a.t.Fatalf("no line from the adapter; stderr=%s", a.stderr)
	}
	return sharedLine{}
}

// exerciseSharedAdapter checks a shared worker answers concurrently, routes
// streamed frames by id, and drops a cancelled request while acknowledging
// the cancel.
func exerciseSharedAdapter(t *testing.T, a *sharedAdapter) {
	t.Helper()
	started := time.Now()
	a.request("1", "/sleep", "ms=600")
	a.request("2", "/sleep", "ms=50")
	if got := a.next(); got.ID != "2" || got.Status != 200 {
		t.Fatalf("first answer = %+v, want the short request 2", got)
	}
	if got := a.next(); got.ID != "1" || got.Status != 200 {
		t.Fatalf("second answer = %+v, want request 1", got)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("two concurrent requests took %s", elapsed)
	}

	a.request("3", "/hang", "")
	a.request("4", "/stream", "")
	var frames []string
	for len(frames) < 4 {
		got := a.next()
		if got.ID != "4" {
			t.Fatalf("frame for request %q while only 4 should answer: %+v", got.ID, got)
		}
		frames = append(frames, got.Frame)
	}
	if want := []string{"head", "chunk", "chunk", "end"}; !slices.Equal(frames, want) {
		t.Fatalf("stream frames = %v, want %v", frames, want)
	}
	a.send(map[string]any{"__faas_cancel": "3"})
	if got := a.next(); got.Cancelled != "3" {
		t.Fatalf("cancel acknowledgement = %+v", got)
	}
	a.request("5", "/sleep", "ms=0")
	if got := a.next(); got.ID != "5" || got.Status != 200 {
		t.Fatalf("after cancel = %+v, want request 5", got)
	}
}

func TestNodeFunctionAdapterSharedWorker(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := `const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
export async function handler(event, ctx) {
  if (event.path === "/hang") await new Promise((_, reject) => ctx.signal.addEventListener("abort", () => reject(ctx.signal.reason), {once:true}));
  if (event.path === "/stream") {
    return { statusCode: 200, body: (async function* () { yield "a"; await sleep(20); yield "b"; })() };
  }
  const ms = Number(event.query.replace("ms=", "")) || 0;
  await sleep(ms);
  return { statusCode: 200, body: { ms } };
}`
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.mjs")
	if err := os.WriteFile(adapter, []byte(nodeFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	a, slots := startSharedAdapter(t, node, adapter, 8)
	if slots != 8 {
		t.Fatalf("advertised concurrency %d, want 8", slots)
	}
	exerciseSharedAdapter(t, a)
}

const pythonSharedHandler = `import asyncio

async def handler(event, ctx):
    if event["path"] == "/hang":
        await asyncio.Event().wait()
    if event["path"] == "/stream":
        async def body():
            yield "a"
            await asyncio.sleep(0.02)
            yield "b"
        return {"statusCode": 200, "body": body()}
    ms = int(event["query"].replace("ms=", "") or 0)
    await asyncio.sleep(ms / 1000)
    return {"statusCode": 200, "body": {"ms": ms}}
`

func TestPythonFunctionAdapterSharedWorker(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".faas-handler.py"), []byte(pythonSharedHandler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "handler.py")
	if err := os.WriteFile(adapter, []byte(pythonFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	a, slots := startSharedAdapter(t, python, adapter, 8)
	if slots != 8 {
		t.Fatalf("advertised concurrency %d, want 8", slots)
	}
	exerciseSharedAdapter(t, a)
}

const pythonSyncSharedHandler = `import time

def handler(event, ctx):
    if event["path"] == "/stream":
        def body():
            for piece in ("a", "b", "c", "d"):
                yield piece
                time.sleep(0.15)
        return {"statusCode": 200, "body": body()}
    ms = int(event["query"].replace("ms=", "") or 0)
    time.sleep(ms / 1000)
    return {"statusCode": 200, "body": {"ms": ms}}
`

// A sync Python handler shares its worker on threads (ADR-1342). A cancel
// cannot interrupt the thread: it is acknowledged once the handler returns,
// the cancelled request sends nothing more, and a streamed body stops.
func TestPythonFunctionAdapterSyncHandlerSharesOnThreads(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".faas-handler.py"), []byte(pythonSyncSharedHandler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "handler.py")
	if err := os.WriteFile(adapter, []byte(pythonFunctionAdapter), 0o644); err != nil {
		t.Fatal(err)
	}
	a, slots := startSharedAdapter(t, python, adapter, 4)
	if slots != 4 {
		t.Fatalf("sync handler advertised concurrency %d, want 4", slots)
	}
	started := time.Now()
	a.request("1", "/sleep", "ms=600")
	a.request("2", "/sleep", "ms=50")
	if got := a.next(); got.ID != "2" || got.Status != 200 {
		t.Fatalf("first answer = %+v, want the short request 2", got)
	}
	if got := a.next(); got.ID != "1" || got.Status != 200 {
		t.Fatalf("second answer = %+v, want request 1", got)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("two concurrent sync requests took %s", elapsed)
	}

	cancelSent := time.Now()
	a.request("3", "/sleep", "ms=400")
	a.send(map[string]any{"__faas_cancel": "3"})
	if got := a.next(); got.Cancelled != "3" {
		t.Fatalf("cancel acknowledgement = %+v, want request 3 cancelled with no answer", got)
	}
	if waited := time.Since(cancelSent); waited < 300*time.Millisecond {
		t.Fatalf("cancel acknowledged after %s, before the handler returned", waited)
	}

	a.request("4", "/stream", "")
	if got := a.next(); got.ID != "4" || got.Frame != "head" {
		t.Fatalf("stream start = %+v", got)
	}
	if got := a.next(); got.ID != "4" || got.Frame != "chunk" {
		t.Fatalf("first chunk = %+v", got)
	}
	a.send(map[string]any{"__faas_cancel": "4"})
	if got := a.next(); got.Cancelled != "4" {
		t.Fatalf("after cancelling the stream = %+v, want only its acknowledgement", got)
	}
	a.request("5", "/sleep", "ms=0")
	if got := a.next(); got.ID != "5" || got.Status != 200 {
		t.Fatalf("after cancels = %+v, want request 5", got)
	}
}
