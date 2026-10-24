// adr: 680
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests run the shipped preloads in real node and python3 processes
// against the barrier. They skip when the runtime is missing. The addon is
// the shipped linux/amd64 ELF on that platform; elsewhere it is rebuilt from
// the same reseed.c for the host with zig, or the Node test skips.

func restoreReseedAssetDir(t *testing.T, needAddon bool) string {
	t.Helper()
	dir := shortSocketDir(t)
	if err := writeRestoreReseedAssets(dir); err != nil {
		t.Fatalf("writeRestoreReseedAssets: %v", err)
	}
	if !needAddon || (runtime.GOOS == "linux" && runtime.GOARCH == "amd64") {
		return dir
	}
	zig, err := exec.LookPath("zig")
	if err != nil {
		t.Skip("host addon needs zig to rebuild reseed.c for " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	target := map[string]string{
		"darwin/arm64": "aarch64-macos", "darwin/amd64": "x86_64-macos", "linux/arm64": "aarch64-linux-gnu",
	}[runtime.GOOS+"/"+runtime.GOARCH]
	if target == "" {
		t.Skip("no zig target for " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	addon := filepath.Join(dir, "reseed.node")
	_ = os.Chmod(addon, 0o644)
	args := []string{"cc", "-target", target, "-shared", "-fPIC", "-fno-stack-protector", "-O2", "-o", addon, "rngpreload/reseed.c"}
	if runtime.GOOS == "darwin" {
		args = append(args, "-Wl,-undefined,dynamic_lookup")
	} else {
		args = append(args, "-nostdlib")
	}
	if out, err := exec.Command(zig, args...).CombinedOutput(); err != nil {
		t.Skipf("zig could not build the host addon: %v\n%s", err, out)
	}
	return dir
}

type runtimeProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

func startRuntime(t *testing.T, name string, env []string, args ...string) *runtimeProc {
	t.Helper()
	bin, err := exec.LookPath(name)
	if err != nil {
		t.Skip(name + " not installed")
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return &runtimeProc{cmd: cmd, stdin: stdin, stdout: bufio.NewScanner(stdout)}
}

func (p *runtimeProc) ask(t *testing.T, command string, into any) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, command+"\n"); err != nil {
		t.Fatal(err)
	}
	p.read(t, into)
}

func (p *runtimeProc) read(t *testing.T, into any) {
	t.Helper()
	line := make(chan string, 1)
	go func() {
		if p.stdout.Scan() {
			line <- p.stdout.Text()
		}
		close(line)
	}()
	select {
	case l, ok := <-line:
		if !ok {
			t.Fatal("runtime exited before answering")
		}
		// The preload must never write to stdout: function adapters frame
		// responses there, so every stdout line must be the script's own JSON.
		if err := json.Unmarshal([]byte(l), into); err != nil {
			t.Fatalf("stdout line %q is not the script's JSON: %v", l, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runtime did not answer")
	}
}

func baseRuntimeEnv(assetDir, sock string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	return stampRestoreReseedEnv(env, assetDir, sock)
}

const nodeProbe = `
const m = globalThis[Symbol.for('gregale.restoreReseed')];
const replaced = !!m && !/native code/.test(Function.prototype.toString.call(Math.random));
process.stdout.write(JSON.stringify({ ready: true, replaced }) + '\n');
require('readline').createInterface({ input: process.stdin }).on('line', (l) => {
  if (l === 'state') process.stdout.write(JSON.stringify({ state: Array.from(m.state).join(',') }) + '\n');
  if (l === 'stale') process.stdout.write(JSON.stringify(m.stale) + '\n');
  const c = require('crypto');
  if (l === 'uuid') { c.randomUUID(); process.stdout.write(JSON.stringify(m.stale) + '\n'); }
  if (l === 'webuuid') { globalThis.crypto.randomUUID(); process.stdout.write(JSON.stringify(m.stale) + '\n'); }
  if (l === 'int') c.randomInt(10, (err) => process.stdout.write(JSON.stringify({ ...m.stale, err: !!err }) + '\n'));
  if (l === 'shape') process.stdout.write(JSON.stringify({ shape: [c.randomUUID.name, c.randomUUID.length, c.randomInt.name, c.randomInt.length,
    globalThis.crypto.randomUUID.name, globalThis.crypto.randomUUID.length].join(',') }) + '\n');
});
setInterval(() => {}, 1000);
`

// nodeThreadProbe blocks its event loop on request, so a test can show the
// thread-mode client answers the barrier without it.
const nodeThreadProbe = `
const m = globalThis[Symbol.for('gregale.restoreReseed')];
const replaced = !!m && !/native code/.test(Function.prototype.toString.call(Math.random));
process.stdout.write(JSON.stringify({ ready: true, replaced }) + '\n');
const block = (ms) => Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
require('readline').createInterface({ input: process.stdin }).on('line', (l) => {
  if (l === 'state') process.stdout.write(JSON.stringify({ state: Array.from(m.state).join(','), ...m.stale }) + '\n');
  if (l === 'block') {
    process.stdout.write(JSON.stringify({ blocking: true }) + '\n');
    block(1500);
    const before = { state: Array.from(m.state).join(','), ...m.stale };
    const value = Math.random();
    const after = { state: Array.from(m.state).join(','), ...m.stale };
    process.stdout.write(JSON.stringify({ before, after, value }) + '\n');
  }
});
setInterval(() => {}, 1000);
`

type nodeThreadState struct {
	State string
	UUID  bool
	Int   bool
}

func threadModeEnv(assets, sock string) []string {
	return stampRestoreReseedMode(baseRuntimeEnv(assets, sock), restoreReseedThreadMode)
}

// ADR-20261024105607: in thread mode the addon's native thread registers the
// process and answers the barrier while the event loop is blocked. Math.random
// adopts the restore before it returns its next value, even when that call
// runs before the event loop has woken for the restore.
func TestRestoreReseedNodeThreadAnswersWithoutEventLoop(t *testing.T) {
	assets := restoreReseedAssetDir(t, true)
	b, sock := startTestBarrier(t)
	p := startRuntime(t, "node", threadModeEnv(assets, sock), "-e", nodeThreadProbe)
	var ready struct{ Ready, Replaced bool }
	p.read(t, &ready)
	if !ready.Replaced {
		t.Fatal("Math.random was not replaced by the preload")
	}
	waitRegistered(t, b, 1)
	if got := registeredRuntimes(b); got != "node-thread" {
		t.Fatalf("registered runtimes = %q, want the addon's thread", got)
	}

	var blocking struct{ Blocking bool }
	p.ask(t, "block", &blocking)
	started := time.Now()
	stats, err := b.ReseedWithStats(time.Second)
	if err != nil {
		t.Fatalf("Reseed with the event loop blocked: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("thread-mode reseed took %s: it waited for the blocked event loop", elapsed)
	}
	if stats.Clients != 1 || stats.SlowestRuntime != "node-thread" {
		t.Fatalf("stats = %+v, want one node-thread client", stats)
	}
	var result struct {
		Before, After nodeThreadState
		Value         float64
	}
	p.read(t, &result)
	if result.Before.UUID || result.Before.Int {
		t.Fatalf("caches stale before the event loop ran: %+v", result.Before)
	}
	if !result.After.UUID || !result.After.Int {
		t.Fatalf("Math.random returned without adopting the restore: %+v", result.After)
	}
	if result.Before.State == result.After.State {
		t.Fatal("Math.random state unchanged by the restore")
	}

	// A second restore: the event loop is idle now, and the addon's wake
	// lets it adopt the restore without any call into the generators.
	var idle nodeThreadState
	p.ask(t, "state", &idle)
	if err := b.Reseed(time.Second); err != nil {
		t.Fatalf("second Reseed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var now nodeThreadState
		p.ask(t, "state", &now)
		if now.State != idle.State {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the event loop never adopted the restore after the addon's wake")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The thread-mode client fails the barrier closed exactly like the
// event-loop client: a nonce too short to be guest-init's is refused.
func TestRestoreReseedNodeThreadRejectsShortNonce(t *testing.T) {
	assets := restoreReseedAssetDir(t, true)
	b, sock := startTestBarrier(t)
	p := startRuntime(t, "node", threadModeEnv(assets, sock), "-e", nodeThreadProbe)
	var ready struct{ Ready, Replaced bool }
	p.read(t, &ready)
	waitRegistered(t, b, 1)
	var client *restoreReseedClient
	b.mu.Lock()
	for c := range b.clients {
		client = c
	}
	b.mu.Unlock()
	if _, err := io.WriteString(client.conn, "reseed 00ff\n"); err != nil {
		t.Fatal(err)
	}
	_ = client.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := readRestoreReseedLine(client.reader)
	_ = client.conn.SetReadDeadline(time.Time{})
	if err != nil || line != "err bad_nonce" {
		t.Fatalf("short nonce reply = %q, %v; want err bad_nonce", line, err)
	}
	if err := b.Reseed(time.Second); err != nil {
		t.Fatalf("Reseed after a refused nonce: %v", err)
	}
}

// Without the addon there is no thread: the preload falls back to the
// event-loop client, which then fails the barrier closed.
func TestRestoreReseedNodeThreadWithoutAddonFailsClosed(t *testing.T) {
	assets := restoreReseedAssetDir(t, false)
	if err := os.Remove(filepath.Join(assets, "reseed.node")); err != nil {
		t.Fatal(err)
	}
	b, sock := startTestBarrier(t)
	p := startRuntime(t, "node", threadModeEnv(assets, sock), "-e", nodeThreadProbe)
	var ready struct{ Ready, Replaced bool }
	p.read(t, &ready)
	waitRegistered(t, b, 1)
	err := b.Reseed(2 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "openssl_reseed_unavailable") {
		t.Fatalf("Reseed err = %v, want openssl_reseed_unavailable", err)
	}
}

// nodeWorkerProbe runs the preload in a worker thread too. The worker
// reports its generators around one Math.random call on request.
const nodeWorkerProbe = `
const { Worker } = require('worker_threads');
const worker = new Worker(` + "`" + `
const { parentPort } = require('worker_threads');
const m = globalThis[Symbol.for('gregale.restoreReseed')];
parentPort.on('message', () => {
  const before = { state: Array.from(m.state).join(','), ...m.stale };
  Math.random();
  const after = { state: Array.from(m.state).join(','), ...m.stale };
  parentPort.postMessage({ before, after });
});
parentPort.postMessage({ ready: !!m });
` + "`" + `, { eval: true });
worker.once('message', (msg) => {
  process.stdout.write(JSON.stringify({ ready: msg.ready }) + '\n');
  require('readline').createInterface({ input: process.stdin }).on('line', (l) => {
    if (l !== 'worker') return;
    worker.once('message', (reply) => process.stdout.write(JSON.stringify(reply) + '\n'));
    worker.postMessage('probe');
  });
});
`

// In thread mode a worker isolate does not join the barrier: the process's
// one addon thread answers for it, and the worker adopts each restore from
// the shared generation before its next value.
func TestRestoreReseedNodeThreadWorkerAdoptsRestore(t *testing.T) {
	assets := restoreReseedAssetDir(t, true)
	b, sock := startTestBarrier(t)
	p := startRuntime(t, "node", threadModeEnv(assets, sock), "-e", nodeWorkerProbe)
	var ready struct{ Ready bool }
	p.read(t, &ready)
	if !ready.Ready {
		t.Fatal("the preload did not run in the worker")
	}
	waitRegistered(t, b, 1)
	time.Sleep(300 * time.Millisecond)
	if got := registeredRuntimes(b); got != "node-thread" {
		t.Fatalf("registered runtimes = %q, want only the process's addon thread", got)
	}
	if err := b.Reseed(time.Second); err != nil {
		t.Fatalf("Reseed: %v", err)
	}
	var result struct{ Before, After nodeThreadState }
	p.ask(t, "worker", &result)
	if result.Before.UUID || result.Before.Int || !result.After.UUID || !result.After.Int {
		t.Fatalf("worker caches before/after Math.random = %+v / %+v, want adoption on that call", result.Before, result.After)
	}
	if result.Before.State == result.After.State {
		t.Fatal("worker Math.random state unchanged by the restore")
	}
}

func registeredRuntimes(b *restoreReseedBarrier) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.clients))
	for c := range b.clients {
		names = append(names, c.runtime)
	}
	return strings.Join(names, ",")
}

func TestRestoreReseedNodePreload(t *testing.T) {
	assets := restoreReseedAssetDir(t, true)
	b, sock := startTestBarrier(t)
	p := startRuntime(t, "node", baseRuntimeEnv(assets, sock), "-e", nodeProbe)
	var ready struct{ Ready, Replaced bool }
	p.read(t, &ready)
	if !ready.Replaced {
		t.Fatal("Math.random was not replaced by the preload")
	}
	waitRegistered(t, b, 1)

	var before, after struct{ State string }
	p.ask(t, "state", &before)
	if err := b.Reseed(2 * time.Second); err != nil {
		t.Fatalf("Reseed: %v", err)
	}
	p.ask(t, "state", &after)
	if before.State == after.State {
		t.Fatalf("Math.random state %s unchanged by the reseed", after.State)
	}
}

// ADR-1110: a restore marks Node's randomUUID and randomInt caches stale
// instead of draining them in the barrier, and the first call that reads a
// cache drains it, through either crypto.randomUUID or webcrypto's, which
// share one batch. The wrappers keep the originals' names and arity.
func TestRestoreReseedNodePreloadDrainsCachesOnFirstUse(t *testing.T) {
	assets := restoreReseedAssetDir(t, true)
	b, sock := startTestBarrier(t)
	p := startRuntime(t, "node", baseRuntimeEnv(assets, sock), "-e", nodeProbe)
	var ready struct{ Ready, Replaced bool }
	p.read(t, &ready)
	waitRegistered(t, b, 1)

	var shape struct{ Shape string }
	p.ask(t, "shape", &shape)
	if shape.Shape != "randomUUID,1,randomInt,3,randomUUID,0" {
		t.Fatalf("wrapped crypto functions = %s, want the originals' names and lengths", shape.Shape)
	}
	type staleState struct{ UUID, Int, Err bool }
	var st staleState
	p.ask(t, "stale", &st)
	if st.UUID || st.Int {
		t.Fatalf("caches stale before any restore: %+v", st)
	}
	for _, calls := range []struct {
		cmds []string
		want staleState
	}{
		{cmds: []string{"uuid"}, want: staleState{Int: true}},
		{cmds: []string{"webuuid"}, want: staleState{Int: true}},
		{cmds: []string{"uuid", "int"}, want: staleState{}},
	} {
		if err := b.Reseed(2 * time.Second); err != nil {
			t.Fatalf("Reseed: %v", err)
		}
		p.ask(t, "stale", &st)
		if !st.UUID || !st.Int {
			t.Fatalf("after Reseed caches = %+v, want both stale until first use", st)
		}
		for _, cmd := range calls.cmds {
			p.ask(t, cmd, &st)
		}
		if st != calls.want {
			t.Fatalf("after %v caches = %+v, want %+v", calls.cmds, st, calls.want)
		}
	}
}

// Production-us Node apps start through `npm run start`. npm only launches
// the app and waits, so the preload must not register it: the barrier then
// waited for npm's idle heap to fault back in on every restore (hunt #9,
// resume_hook p50 111 -> 156 ms on the metal bench). The app it launches
// still registers.
func TestRestoreReseedNodePreloadSkipsPackageManagerLauncher(t *testing.T) {
	assets := restoreReseedAssetDir(t, true)
	b, sock := startTestBarrier(t)
	launcher := filepath.Join(t.TempDir(), "node_modules", "npm", "bin", "npm-cli.js")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `const { spawn } = require('child_process');
const child = spawn(process.execPath, ['-e', ` + "`" + `${process.env.GREGALE_TEST_CHILD}` + "`" + `], { stdio: ['pipe', 'inherit', 'inherit'] });
process.stdin.pipe(child.stdin);
`
	if err := os.WriteFile(launcher, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	// The child exits when the killed launcher's stdin pipe closes, so no
	// grandchild outlives the test holding its output open.
	env := append(baseRuntimeEnv(assets, sock), "GREGALE_TEST_CHILD="+nodeProbe+"\nprocess.stdin.on('end', () => process.exit(0));\n")
	p := startRuntime(t, "node", env, launcher)
	var ready struct{ Ready, Replaced bool }
	p.read(t, &ready)
	if !ready.Replaced {
		t.Fatal("the launched app did not load the preload")
	}
	waitRegistered(t, b, 1)
	time.Sleep(500 * time.Millisecond)
	if got := b.registered(); got != 1 {
		t.Fatalf("registered = %d, want 1: the npm launcher itself joined the barrier", got)
	}
	if err := b.Reseed(2 * time.Second); err != nil {
		t.Fatalf("Reseed: %v", err)
	}
}

// Without the OpenSSL reseed the process may replay the snapshot's crypto
// state, so the preload must refuse and fail the barrier closed.
func TestRestoreReseedNodePreloadFailsClosedWithoutAddon(t *testing.T) {
	assets := restoreReseedAssetDir(t, false)
	if err := os.Remove(filepath.Join(assets, "reseed.node")); err != nil {
		t.Fatal(err)
	}
	b, sock := startTestBarrier(t)
	p := startRuntime(t, "node", baseRuntimeEnv(assets, sock), "-e", nodeProbe)
	var ready struct{ Ready, Replaced bool }
	p.read(t, &ready)
	waitRegistered(t, b, 1)
	err := b.Reseed(2 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "openssl_reseed_unavailable") {
		t.Fatalf("Reseed err = %v, want openssl_reseed_unavailable", err)
	}
}

const pythonProbe = `
import hashlib, json, os, random, ssl, sys, time
ssl.RAND_bytes(16)
def state():
    return hashlib.sha256(repr(random.getstate()).encode()).hexdigest()
print(json.dumps({"ready": True, "customer_site": os.environ.get("CUSTOMER_SITE_RAN", "")}), flush=True)
child = 0
for line in sys.stdin:
    line = line.strip()
    if line == "state":
        print(json.dumps({"state": state()}), flush=True)
    elif line == "fork":
        child = os.fork()
        if child == 0:
            time.sleep(30)
            os._exit(0)
        print(json.dumps({"child": child}), flush=True)
if child:
    os.kill(child, 9)
`

func TestRestoreReseedPythonPreload(t *testing.T) {
	testRestoreReseedPythonPreload(t, "")
}

// ADR-20261024105607: thread mode answers with the minimal reply path and
// keeps every guarantee of the default client, forked children included.
func TestRestoreReseedPythonPreloadThreadMode(t *testing.T) {
	testRestoreReseedPythonPreload(t, restoreReseedThreadMode)
}

func testRestoreReseedPythonPreload(t *testing.T, mode string) {
	assets := restoreReseedAssetDir(t, false)
	customer := shortSocketDir(t)
	site := "import os\nos.environ['CUSTOMER_SITE_RAN'] = '1'\n"
	if err := os.WriteFile(filepath.Join(customer, "sitecustomize.py"), []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	b, sock := startTestBarrier(t)
	env := baseRuntimeEnv(assets, sock)
	if mode != "" {
		env = stampRestoreReseedMode(env, mode)
	}
	env = setEnvValue(env, "PYTHONPATH", envValue(env, "PYTHONPATH")+string(os.PathListSeparator)+customer)
	p := startRuntime(t, "python3", env, "-c", pythonProbe)
	var ready struct {
		Ready        bool
		CustomerSite string `json:"customer_site"`
	}
	p.read(t, &ready)
	if ready.CustomerSite != "1" {
		t.Fatal("the customer's sitecustomize did not run after the preload")
	}
	waitRegistered(t, b, 1)

	var before, after struct{ State string }
	p.ask(t, "state", &before)
	if err := b.Reseed(2 * time.Second); err != nil {
		t.Fatalf("Reseed: %v", err)
	}
	p.ask(t, "state", &after)
	if before.State == after.State {
		t.Fatal("random state unchanged by the reseed")
	}

	// A pre-forking server's children must register too.
	var forked struct{ Child int }
	p.ask(t, "fork", &forked)
	if forked.Child <= 0 {
		t.Fatalf("fork returned %d", forked.Child)
	}
	t.Cleanup(func() {
		if proc, err := os.FindProcess(forked.Child); err == nil {
			_ = proc.Kill()
		}
	})
	waitRegistered(t, b, 2)
	if err := b.Reseed(2 * time.Second); err != nil {
		t.Fatalf("Reseed with a forked child: %v", err)
	}

	// A nonce too short to be guest-init's fails the barrier closed.
	var client *restoreReseedClient
	b.mu.Lock()
	for c := range b.clients {
		client = c
		break
	}
	b.mu.Unlock()
	if _, err := io.WriteString(client.conn, "reseed 00ff\n"); err != nil {
		t.Fatal(err)
	}
	_ = client.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := readRestoreReseedLine(client.reader)
	_ = client.conn.SetReadDeadline(time.Time{})
	if err != nil || line != "err bad_nonce" {
		t.Fatalf("short nonce reply = %q, %v; want err bad_nonce", line, err)
	}
}

func TestRestoreReseedOptOutInjectsNothing(t *testing.T) {
	env := stampRestoreReseedEnv([]string{"GREGALE_RESTORE_RESEED=OFF"}, "/a", "/b")
	for _, kv := range env {
		if strings.HasPrefix(kv, "NODE_OPTIONS=") || strings.HasPrefix(kv, "PYTHONPATH=") {
			t.Fatalf("opt-out still injected %s", kv)
		}
	}
}
