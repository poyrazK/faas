//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/profiling"
)

func TestParseNodeInspector(t *testing.T) {
	for in, want := range map[string]*nodeInspector{
		"sigusr1:9229": {port: 9229},
		"open:40123":   {open: true, port: 40123},
		"sigusr1:0":    nil,
		"open:70000":   nil,
		"listen:9229":  nil,
		"sigusr1":      nil,
		"":             nil,
	} {
		got, ok := parseNodeInspector(in)
		if (want == nil) == ok || (want != nil && (got.open != want.open || got.port != want.port)) {
			t.Errorf("parseNodeInspector(%q) = %+v, %v; want %+v", in, got, ok, want)
		}
	}
}

func TestBridgeRemembersNodeInspectorsPastRegistration(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	h := b.handler()
	poll := func(query string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/control?"+query, nil))
	}
	poll("pid=7&inspector=sigusr1%3A9229")
	poll("pid=8&inspector=bogus")
	b.mu.Lock()
	b.processes["7"] = time.Now().Add(-time.Minute) // stuck: stopped polling
	ni, ok := b.inspectors["7"]
	_, bogus := b.inspectors["8"]
	b.mu.Unlock()
	if !ok || ni.open || ni.port != 9229 || bogus {
		t.Fatalf("inspectors = %+v (bogus kept: %v); want pid 7 on 9229 only", b.inspectors, bogus)
	}
	poll("pid=9")
	b.mu.Lock()
	_, kept := b.inspectors["7"]
	b.mu.Unlock()
	if !kept {
		t.Fatal("a process that stopped polling lost its inspector; a stuck thread could not be sampled")
	}
	poll("pid=7")
	b.mu.Lock()
	_, kept = b.inspectors["7"]
	b.mu.Unlock()
	if kept {
		t.Fatal("an inspector the collector no longer reports was kept")
	}
}

func viewOf(t *testing.T, raw []byte) api.ProfileCaptureView {
	t.Helper()
	merged, skipped, err := profiling.MergeCapture([][]byte{raw}, api.ProfileKindStacks)
	if err != nil || skipped != 0 || merged == nil {
		t.Fatalf("stack profile rejected: %v skipped=%d", err, skipped)
	}
	view, err := profiling.CaptureView(merged, api.ProfileKindStacks)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func functionTotal(view api.ProfileCaptureView, name string) (api.ProfileCaptureFunction, bool) {
	for _, f := range view.Functions {
		if f.Name == name {
			return f, true
		}
	}
	return api.ProfileCaptureFunction{}, false
}

func TestEncodeStackProfileParsesAsStacks(t *testing.T) {
	raw := encodeStackProfile([]stackSample{
		{frames: nodeCallFrames([]nodeCallFrame{{FunctionName: "spin", URL: "file:///app/a.js"}, {URL: "file:///app/a.js"}}, nil), count: 1},
		{frames: []stackFrame{{name: "waiting on Timeout"}, {name: "<event loop>"}}, count: 2},
	}, time.Now())
	view := viewOf(t, raw)
	if view.Total != 3 || view.Unit != "count" {
		t.Fatalf("view total=%d unit=%q, want 3 count", view.Total, view.Unit)
	}
	if f, ok := functionTotal(view, "spin"); !ok || f.File != "/app/a.js" || f.Line != 1 || f.Self != 1 {
		t.Fatalf("spin = %+v (found %v)", f, ok)
	}
	if f, ok := functionTotal(view, "waiting on Timeout"); !ok || f.Total != 2 {
		t.Fatalf("handles = %+v (found %v)", f, ok)
	}
	if _, ok := functionTotal(view, "(anonymous)"); !ok {
		t.Fatal("an unnamed frame lost its placeholder name")
	}
}

// fakeInspector speaks just enough of the inspector protocol: it answers the
// pid check, answers the event-loop probe only when idle, and pauses with
// fixed call frames.
type fakeInspector struct {
	pid  int
	idle bool
	mu   sync.Mutex
	seen []string
	snap string
}

func (f *fakeInspector) start(t *testing.T) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	var addr string
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{"webSocketDebuggerUrl": "ws://" + addr + "/devtools"}})
	})
	mux.HandleFunc("/devtools", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			var m struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if ws.ReadJSON(&m) != nil {
				return
			}
			expr, _ := m.Params["expression"].(string)
			f.mu.Lock()
			f.seen = append(f.seen, m.Method+" "+expr)
			f.mu.Unlock()
			reply := func(result any) { _ = ws.WriteJSON(map[string]any{"id": m.ID, "result": result}) }
			switch {
			case m.Method == "Runtime.evaluate" && expr == "process.pid":
				reply(map[string]any{"result": map[string]any{"type": "number", "value": f.pid}})
			case m.Method == "Runtime.evaluate" && expr == nodeLivenessExpr:
				if f.idle {
					reply(map[string]any{"result": map[string]any{"type": "number", "value": 1}})
				}
			case m.Method == "Debugger.enable":
				_ = ws.WriteJSON(map[string]any{"method": "Debugger.scriptParsed", "params": map[string]any{"scriptId": "1", "url": "file:///app/server.js"}})
				reply(map[string]any{"debuggerId": "d"})
			case m.Method == "Debugger.pause":
				reply(map[string]any{})
				_ = ws.WriteJSON(map[string]any{"method": "Debugger.paused", "params": map[string]any{"reason": "other", "callFrames": []map[string]any{
					// Current V8 leaves url empty; the script id resolves it.
					{"callFrameId": "f0", "functionName": "spinForever", "url": "", "location": map[string]any{"scriptId": "1", "lineNumber": 41}},
					{"callFrameId": "f1", "functionName": "", "url": "file:///app/server.js", "location": map[string]any{"scriptId": "1", "lineNumber": 9}},
				}}})
			case m.Method == "Debugger.evaluateOnCallFrame":
				f.mu.Lock()
				f.snap = expr
				f.mu.Unlock()
				reply(map[string]any{"result": map[string]any{"type": "string", "value": `["TCPServerWrap","Timeout","Timeout"]`}})
			default:
				reply(map[string]any{})
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	addr = srv.Listener.Addr().String()
	return addr
}

func (f *fakeInspector) methods() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.seen, "\n")
}

func TestSampleNodeInspectorPausesAStuckLoop(t *testing.T) {
	f := &fakeInspector{pid: 4242}
	raw, err := sampleNodeInspector(context.Background(), f.start(t), 4242, true, strings.Repeat("e", 32))
	if err != nil || len(raw) == 0 {
		t.Fatalf("sample = %d bytes, %v; want a stack", len(raw), err)
	}
	view := viewOf(t, raw)
	if fn, ok := functionTotal(view, "spinForever"); !ok || fn.File != "/app/server.js" || fn.Line != 42 || fn.Self != 1 {
		t.Fatalf("spinForever = %+v (found %v)", fn, ok)
	}
	if fn, ok := functionTotal(view, "waiting on Timeout"); !ok || fn.Total != 2 {
		t.Fatalf("handles = %+v (found %v)", fn, ok)
	}
	seen := f.methods()
	resume, disable, closeAt := strings.Index(seen, "Debugger.resume"), strings.Index(seen, "Debugger.disable"), strings.Index(seen, "Runtime.evaluate "+nodeCloseExpr)
	if resume < 0 || disable < resume || closeAt < disable {
		t.Fatalf("protocol order:\n%s\nwant resume, then disable, then the close request", seen)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(f.snap, `"`+strings.Repeat("e", 32)+`"`) {
		t.Fatalf("snapshot expression %q does not mark the capture epoch", f.snap)
	}
}

func TestSampleNodeInspectorLeavesAnIdleLoop(t *testing.T) {
	f := &fakeInspector{pid: 4242, idle: true}
	raw, err := sampleNodeInspector(context.Background(), f.start(t), 4242, false, strings.Repeat("e", 32))
	if err != nil || raw != nil {
		t.Fatalf("idle loop sampled: %d bytes, %v", len(raw), err)
	}
	if seen := f.methods(); strings.Contains(seen, "Debugger.") || strings.Contains(seen, nodeCloseExpr) {
		t.Fatalf("an idle loop was paused, or an inspector this capture did not open was closed:\n%s", seen)
	}
}

func TestSampleNodeInspectorRefusesAnotherProcess(t *testing.T) {
	f := &fakeInspector{pid: 99}
	if _, err := sampleNodeInspector(context.Background(), f.start(t), 4242, true, strings.Repeat("e", 32)); !errors.Is(err, errNodeOtherProcess) {
		t.Fatalf("err = %v, want errNodeOtherProcess", err)
	}
	if seen := f.methods(); strings.Contains(seen, "Debugger.pause") || strings.Contains(seen, nodeCloseExpr) {
		t.Fatalf("another process was paused, or its own inspector closed:\n%s", seen)
	}
}

func TestCaptureAddsTheStackOfAStuckNodeProcess(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	h := b.handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/control?pid=7&inspector=sigusr1%3A9229", nil))
	b.mu.Lock()
	b.processes["7"] = time.Now().Add(-time.Minute)
	stack := encodeStackProfile([]stackSample{{frames: []stackFrame{{name: "spinForever"}, {name: "<main thread>"}}, count: 1}}, time.Now())
	b.nodeStack = func(_ context.Context, pid int, ni nodeInspector, epoch string) ([]byte, error) {
		if pid != 7 || ni.port != 9229 || epoch == "" {
			return nil, errors.New("unexpected target")
		}
		return stack, nil
	}
	b.mu.Unlock()
	req := captureRequest("stacks")
	result, _ := b.runCapture(req, func(d time.Duration) {
		if d == req.Duration() {
			waitFor(t, "the sampled stack", b.locked(func() bool { return b.capture != nil && len(b.capture.profiles) == 1 }))
		}
	})
	if len(result.Profiles) != 1 || result.Profiles[0].Kind != "stacks" || result.Profiles[0].ProcessID != "7" || result.Processes != 1 || result.Reason != "" {
		t.Fatalf("capture = %+v, want the stuck process's stack counted as its process", result)
	}
}

// TestNodeInspectorSnapshotsARealStuckLoop drives a real Node process with the
// managed preload: SIGUSR1 opens its inspector, the probe finds the loop
// stuck, the pause names the spinning function, and the inspector is closed
// once the loop frees. Set NODE_BIN or put node on PATH.
func TestNodeInspectorSnapshotsARealStuckLoop(t *testing.T) {
	node := os.Getenv("NODE_BIN")
	if node == "" {
		var err error
		if node, err = exec.LookPath("node"); err != nil {
			t.Skip("node not found; set NODE_BIN")
		}
	}
	preload, err := filepath.Abs("../profiling/node.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	dir := t.TempDir()
	app, marker := filepath.Join(dir, "server.js"), filepath.Join(dir, "spinning")
	if err := os.WriteFile(app, []byte(`
function spinForever(ms) { const end = Date.now() + ms; let x = 0; while (Date.now() < end) x++; return x; }
require('node:http').createServer(() => {}).listen(0);
setTimeout(() => { require('node:fs').writeFileSync(`+strconv.Quote(marker)+`, ''); spinForever(4000); }, 800);
setTimeout(() => process.exit(0), 20000);
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--inspect-port="+strconv.Itoa(port), "--require", preload, app)
	cmd.Env = append(os.Environ(), "FAAS_PROFILING_ENABLED=1", "FAAS_PROFILING_ENDPOINT=http://127.0.0.1:1")
	var stderr lockedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitFor(t, "the application to start spinning", func() bool { _, err := os.Stat(marker); return err == nil })
	time.Sleep(300 * time.Millisecond)
	ni := nodeInspector{port: port}
	raw, err := captureNodeStack(context.Background(), cmd.Process.Pid, ni, strings.Repeat("e", 32))
	if err != nil || len(raw) == 0 {
		status, _ := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/status")
		masks := []string{}
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "Sig") {
				masks = append(masks, line)
			}
		}
		t.Fatalf("stuck loop sample = %d bytes, %v\nnode stderr: %s\nsignals: %v", len(raw), err, stderr.String(), masks)
	}
	view := viewOf(t, raw)
	if fn, ok := functionTotal(view, "spinForever"); !ok || !strings.HasSuffix(fn.File, "/server.js") {
		top := []string{}
		for _, f := range view.Functions {
			top = append(top, f.Name)
		}
		t.Fatalf("stack lacks spinForever: %v", top)
	}
	if _, ok := functionTotal(view, "waiting on TCPServerWrap"); !ok {
		t.Fatal("stack lacks the handles the paused loop holds")
	}
	// Once the loop frees, the inspector this capture opened closes.
	closed := false
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := nodeInspectorTarget(context.Background(), &http.Client{Timeout: 200 * time.Millisecond}, "127.0.0.1:"+strconv.Itoa(port)); err != nil {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("the inspector opened for the capture stayed open")
	}
	if cmd.ProcessState != nil || cmd.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("the application did not survive the snapshot")
	}
	// An idle loop is left to the collector.
	if raw, err := captureNodeStack(context.Background(), cmd.Process.Pid, ni, strings.Repeat("f", 32)); err != nil || raw != nil {
		t.Fatalf("idle loop sample = %d bytes, %v; want none", len(raw), err)
	}
}
