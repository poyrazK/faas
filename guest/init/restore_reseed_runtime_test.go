// adr: 481
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
});
setInterval(() => {}, 1000);
`

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
	assets := restoreReseedAssetDir(t, false)
	customer := shortSocketDir(t)
	site := "import os\nos.environ['CUSTOMER_SITE_RAN'] = '1'\n"
	if err := os.WriteFile(filepath.Join(customer, "sitecustomize.py"), []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	b, sock := startTestBarrier(t)
	env := baseRuntimeEnv(assets, sock)
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
}

func TestRestoreReseedOptOutInjectsNothing(t *testing.T) {
	env := stampRestoreReseedEnv([]string{"GREGALE_RESTORE_RESEED=OFF"}, "/a", "/b")
	for _, kv := range env {
		if strings.HasPrefix(kv, "NODE_OPTIONS=") || strings.HasPrefix(kv, "PYTHONPATH=") {
			t.Fatalf("opt-out still injected %s", kv)
		}
	}
}
