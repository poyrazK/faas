package internal

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"golang.org/x/net/http2"
)

// Run the generated adapter through the actual Serve and H2C listener. Gates
// are explicit: response EOF and admission are checked before releasing work.
const backgroundFunctionSource = `
import fs from "node:fs";
const gates = new Map(), completed = [], aborted = [];
let calls = 0;
const wait = (key) => new Promise((resolve) => gates.set(key, resolve));
const abort = (ctx) => new Promise((resolve) => {
  if (ctx.signal.aborted) resolve();
  else ctx.signal.addEventListener("abort", resolve, { once: true });
});
const snapshot = (ctx) => ({ pid: process.pid, invocation: ctx.invocation_id,
  signal_aborted: ctx.signal.aborted, completed: [...completed], aborted: [...aborted], calls });
export async function handler(event, ctx) {
  calls++;
  const query = new URLSearchParams(event.query);
  const key = query.get("key") || ctx.invocation_id;
  const mode = query.get("mode") || "whole";
  if (event.path === "/release-all") {
    for (const release of gates.values()) release();
    gates.clear();
  } else if (event.path === "/release") {
    gates.get(key)?.(); gates.delete(key);
  } else if (event.path === "/kill") {
    setTimeout(() => process.exit(1), 50);
  } else if (event.path === "/tail") {
    for (let n = 0; n < Number(query.get("tasks") || 1); n++) {
    const gate = wait(key);
    ctx.waitUntil((async () => {
      if (mode === "abort" || mode === "cancel" || mode === "pre-response") {
        await abort(ctx); aborted.push(ctx.invocation_id);
      }
      if (mode === "file") {
        while (!fs.existsSync(process.env.BACKGROUND_FIXTURE_RELEASE_FILE)) {
          await new Promise((resolve) => setTimeout(resolve, 5));
        }
      } else await gate;
      if (mode === "reject") throw new Error("fixture background rejection");
      if (mode === "nested") {
        ctx.waitUntil(wait(key + "-child").then(() => completed.push(ctx.invocation_id + ":child")));
      }
      completed.push(ctx.invocation_id);
    })());
    }
    if (mode === "pre-response") await abort(ctx);
  }
  const body = JSON.stringify(snapshot(ctx));
  if (event.path === "/tail" && mode === "cancel") {
    return { body: (async function* () { yield "started"; await abort(ctx); })() };
  }
  if (mode === "stream") {
    return { body: (async function* () { yield body.slice(0, 5); await new Promise(setImmediate); yield body.slice(5); })() };
  }
  return { body };
}
`

const backgroundFetchSource = backgroundFunctionSource + `
export default { async fetch(request, env, ctx) {
  const url = new URL(request.url);
  const result = await handler({ path: url.pathname, query: url.search.slice(1) }, ctx);
  return new Response(result.body);
} };
`

type backgroundSnapshot struct {
	PID           int      `json:"pid"`
	Invocation    string   `json:"invocation"`
	SignalAborted bool     `json:"signal_aborted"`
	Completed     []string `json:"completed"`
	Aborted       []string `json:"aborted"`
	Calls         int      `json:"calls"`
}

type backgroundFunction struct {
	client  *http.Client
	base    string
	runtime *BackgroundRuntime
	cfg     ServeConfig
	release string
	h2      bool
	mu      sync.Mutex
	events  []BackgroundEvent
}

func startBackgroundFunction(t *testing.T, plan api.Plan, slots int, h2 bool, source string, timeout time.Duration, configure ...func(*ServeConfig)) *backgroundFunction {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is not installed")
	}
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	if err := os.Mkdir(app, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"package.json": `{"type":"module"}`, "handler.js": source} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := rootfs.NormalizeFunctionHandlerFrom(dir, "/app/handler.js", "/app/node22.js"); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewBackgroundRuntime(plan)
	if err != nil {
		t.Fatal(err)
	}
	if timeout > 0 {
		// Shorten wall time only in package-internal deadline tests. Public
		// construction and production policy always use central plan limits.
		runtime.timeout = timeout
	}
	f := &backgroundFunction{runtime: runtime, release: filepath.Join(dir, "released"), h2: h2}
	runtime.Observe = func(e BackgroundEvent) { f.mu.Lock(); f.events = append(f.events, e); f.mu.Unlock() }
	adapter := filepath.Join(app, "node22.js")
	cfg := ServeConfig{Runtime: "node22", Background: runtime, Spec: workerpool.Spec{
		Executable: node, Args: []string{adapter}, HandlerPath: adapter,
		Env: []string{"PATH=" + os.Getenv("PATH"), "FAAS_FUNCTION_CONCURRENCY=" + strconv.Itoa(slots), "BACKGROUND_FIXTURE_RELEASE_FILE=" + f.release},
	}}
	for _, apply := range configure {
		apply(&cfg)
	}
	f.cfg = cfg
	srv, ln, err := ListenAndServeLoopback(H2CListenerConfig{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { Serve(w, r, cfg) })})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	f.base = "http://" + ln.Addr().String()
	f.client = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{}}
	if h2 {
		f.client.Transport = &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}}
	}
	t.Cleanup(func() {
		_ = os.WriteFile(f.release, nil, 0o600)
		if resp, err := f.client.Get(f.base + "/release-all"); err == nil {
			_ = resp.Body.Close()
		}
		_ = srv.Close()
		f.client.CloseIdleConnections()
		workerpool.Recycle()
	})
	return f
}

func (f *backgroundFunction) request(t *testing.T, path, id string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", f.base+path, nil)
	req.Header.Set(api.InvocationIDHeader, id)
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if (resp.ProtoMajor == 2) != f.h2 {
		_ = resp.Body.Close()
		t.Fatalf("HTTP protocol = %s, h2 = %t", resp.Proto, f.h2)
	}
	return resp
}

func (f *backgroundFunction) get(t *testing.T, path, id string) backgroundSnapshot {
	t.Helper()
	resp := f.request(t, path, id)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("HTTP response: status %d, body %s, error %v", resp.StatusCode, data, err)
	}
	var got backgroundSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("response %s: %v", data, err)
	}
	if got.Invocation != id || got.PID == 0 {
		t.Fatalf("wrong invocation context: %+v", got)
	}
	return got
}

func (f *backgroundFunction) saw(id, phase, outcome string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.InvocationID == id && e.Phase == phase && e.Outcome == outcome {
			return true
		}
	}
	return false
}

func awaitBackground(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("background lifecycle did not reach the expected state")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBackgroundResponseEndsBeforeExecution(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, mode := range []string{"whole", "stream", "fetch"} {
			t.Run(mode+"/h2="+strconv.FormatBool(h2), func(t *testing.T) {
				source := backgroundFunctionSource
				if mode == "fetch" {
					source = backgroundFetchSource
				}
				f := startBackgroundFunction(t, api.PlanFree, 4, h2, source, 0)
				first := f.get(t, "/tail?mode="+mode, "tail")
				if f.runtime.Active() != 1 {
					t.Fatal("HTTP EOF released a pending background task")
				}
				probe := f.get(t, "/state", "neighbor")
				if probe.PID != first.PID || probe.SignalAborted || f.runtime.Active() != 1 {
					t.Fatalf("pending tail blocked or contaminated its neighbor: %+v", probe)
				}
				f.get(t, "/release?key=tail", "release")
				awaitBackground(t, func() bool { return f.runtime.Active() == 0 && f.saw("tail", "settled", "completed") })
			})
		}
	}
}

func TestBackgroundNestedTasksAndRejection(t *testing.T) {
	f := startBackgroundFunction(t, api.PlanFree, 4, false, backgroundFunctionSource, 0)
	f.get(t, "/tail?mode=nested", "parent")
	f.get(t, "/release?key=parent", "release")
	awaitBackground(t, func() bool { return f.saw("parent", "settled", "completed") })
	if f.runtime.Active() != 1 {
		t.Fatal("execution completed before its registered child")
	}
	f.get(t, "/release?key=parent-child", "release-child")
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 })
	f.get(t, "/tail?mode=reject", "rejected")
	f.get(t, "/release?key=rejected", "release-rejection")
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 && f.saw("rejected", "settled", "failed") })
	if f.get(t, "/state", "after-rejection").SignalAborted {
		t.Fatal("background rejection aborted a later invocation")
	}
}

func TestBackgroundDeadlineRetainsCleanup(t *testing.T) {
	f := startBackgroundFunction(t, api.PlanFree, 4, true, backgroundFunctionSource, 150*time.Millisecond)
	first := f.get(t, "/tail?mode=abort", "deadline")
	awaitBackground(t, func() bool { return f.saw("deadline", "timeout", "timeout") })
	awaitBackground(t, func() bool { return len(f.get(t, "/state", "probe").Aborted) == 1 })
	if f.runtime.Active() != 1 {
		t.Fatal("deadline freed admission before abort cleanup finished")
	}
	f.get(t, "/release?key=deadline", "cleanup")
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 })
	probe := f.get(t, "/state", "after-cleanup")
	if probe.PID != first.PID || probe.SignalAborted {
		t.Fatal("cooperative cancellation killed or contaminated the shared worker")
	}
}

func TestBackgroundTaskDeadlineStartsAtRegistration(t *testing.T) {
	f := startBackgroundFunction(t, api.PlanFree, 4, false, backgroundFunctionSource, 500*time.Millisecond)
	// The foreground handler waits for abort. Only the adapter's per-task
	// timer can release it; the post-response reader has not started yet.
	first := f.get(t, "/tail?mode=pre-response", "registered-deadline")
	if !first.SignalAborted || f.runtime.Active() != 1 || !f.saw("registered-deadline", "timeout", "timeout") {
		t.Fatal("task deadline did not start at registration or released cleanup")
	}
	f.get(t, "/release?key=registered-deadline", "cleanup")
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 && f.saw("registered-deadline", "settled", "timeout") })
}

func TestBackgroundDisconnectRetainsCleanup(t *testing.T) {
	f := startBackgroundFunction(t, api.PlanFree, 4, false, backgroundFunctionSource, 0)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.base+"/tail?mode=cancel", nil)
	req.Header.Set(api.InvocationIDHeader, "disconnected")
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, len("started"))
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "started" {
		t.Fatalf("first chunk: %q, %v", buf, err)
	}
	cancel()
	awaitBackground(t, func() bool { return len(f.get(t, "/state", "probe").Aborted) == 1 })
	if f.runtime.Active() != 1 {
		t.Fatal("disconnect released background work before cleanup")
	}
	f.get(t, "/release?key=disconnected", "cleanup")
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 && f.saw("disconnected", "interrupted", "interrupted") })
}

type cancelAfterResponseWrite struct {
	http.ResponseWriter
	cancel context.CancelFunc
}

func (w cancelAfterResponseWrite) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err == nil {
		w.cancel()
	}
	return n, err
}

func TestBackgroundCompletedResponseSurvivesCallerCancellation(t *testing.T) {
	f := startBackgroundFunction(t, api.PlanFree, 4, false, backgroundFunctionSource, 0)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequest("GET", "/tail", nil).WithContext(ctx)
	req.Header.Set(api.InvocationIDHeader, "completed-response")
	rec := httptest.NewRecorder()
	Serve(cancelAfterResponseWrite{ResponseWriter: rec, cancel: cancel}, req, f.cfg)
	if rec.Code != 200 || ctx.Err() == nil || f.runtime.Active() != 1 {
		t.Fatal("completed response did not transfer background ownership")
	}
	probe := f.get(t, "/state", "neighbor")
	if len(probe.Aborted) != 0 {
		t.Fatal("HTTP completion cancellation reached the background signal")
	}
	f.get(t, "/release?key=completed-response", "release")
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 && f.saw("completed-response", "settled", "completed") })
}

func TestBackgroundRecycleWaitsForExecution(t *testing.T) {
	f := startBackgroundFunction(t, api.PlanFree, 1, false, backgroundFunctionSource, 0)
	first := f.get(t, "/tail?mode=file", "old-generation")
	workerpool.Recycle()
	probe := f.get(t, "/state", "new-generation")
	if probe.PID == first.PID || f.runtime.Active() != 1 {
		t.Fatal("recycle reused or released an old invocation")
	}
	if err := os.WriteFile(f.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 && f.saw("old-generation", "settled", "completed") })
}

func TestBackgroundWorkerCrashReleasesWithoutReplay(t *testing.T) {
	f := startBackgroundFunction(t, api.PlanFree, 4, false, backgroundFunctionSource, 0)
	first := f.get(t, "/tail", "crashed")
	f.get(t, "/kill", "killer")
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 && f.saw("crashed", "interrupted", "interrupted") })
	probe := f.get(t, "/state", "replacement")
	if probe.PID == first.PID || probe.Calls != 1 {
		t.Fatalf("worker crash replayed completed HTTP work: %+v", probe)
	}
}

func TestBackgroundUncooperativeTaskRetiresWorker(t *testing.T) {
	f := startBackgroundFunction(t, api.PlanFree, 4, false, backgroundFunctionSource, 100*time.Millisecond)
	first := f.get(t, "/tail", "uncooperative")
	awaitBackground(t, func() bool { return f.saw("uncooperative", "timeout", "timeout") })
	if f.runtime.Active() != 1 {
		t.Fatal("timeout released an uncooperative task")
	}
	awaitBackground(t, func() bool { return f.runtime.Active() == 0 && f.saw("uncooperative", "interrupted", "interrupted") })
	if f.get(t, "/state", "replacement").PID == first.PID {
		t.Fatal("uncooperative worker remained reusable")
	}
}

func TestBackgroundCapacityRefusalIsTerminal(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			f := startBackgroundFunction(t, plan, 4, false, backgroundFunctionSource, 0)
			path, wantActive := "/tail?mode=file&tasks="+strconv.Itoa(plan.TailCapMax()+1), plan.TailCapMax()
			if plan == api.PlanFree {
				wantActive = plan.ConcurrentTailsPerInstance()
				for n := range wantActive {
					f.get(t, "/tail?mode=file", "accepted-"+strconv.Itoa(n))
				}
				// A fifth request starts a second interpreter. Both interpreters
				// must share the Free instance's four-task budget.
				path = "/tail?mode=file"
			}
			resp := f.request(t, path, "refused")
			defer func() { _ = resp.Body.Close() }()
			var body struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != 500 || body.Error != "handler_error" {
				t.Fatalf("admission failure was not terminal: HTTP %d, %+v, %v", resp.StatusCode, body, err)
			}
			if f.runtime.Active() != wantActive {
				t.Fatalf("active tasks %d, want cap %d", f.runtime.Active(), wantActive)
			}
			if err := os.WriteFile(f.release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			awaitBackground(t, func() bool { return f.runtime.Active() == 0 })
		})
	}
}
