//go:build unix

package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A stand-in for an installed framework binary: it owns source updates and
// exposes a WebSocket, and deliberately requires forwarded listening options.
const localHMRFixture = `#!/usr/bin/env node
const http = require('node:http'), fs = require('node:fs'), crypto = require('node:crypto');
const args = process.argv.slice(2), index = args.indexOf('--port');
if (index < 0 || args[args.indexOf('--host') + 1] !== '127.0.0.1') throw Error('missing framework bind options');
const version = () => fs.readFileSync('version.txt', 'utf8');
if (version() === 'crash') throw Error('framework startup fixture failure');
const state = () => ({version:version(), pid:process.pid, database:process.env.DATABASE_URL});
const server = http.createServer((req, res) => {
  res.setHeader('content-type', 'application/json');
  res.end(JSON.stringify(state()));
  if (req.url === '/crash') setTimeout(() => process.exit(1), 10);
});
server.on('upgrade', (req, socket) => {
  const accept = crypto.createHash('sha1').update(req.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
  socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ' + accept + '\r\n\r\n');
  socket.on('error', () => {});
  socket.on('data', () => {
    const body = Buffer.from(JSON.stringify(state()));
    if (body.length >= 126) throw Error('oversized fixture frame');
    socket.write(Buffer.concat([Buffer.from([0x81, body.length]), body]));
  });
});
server.listen(Number(args[index + 1]), '127.0.0.1');
`

func writeLocalHMRProject(t *testing.T, version string) string {
	t.Helper()
	dir := writeDevLocalProject(t, map[string]string{
		"package.json":           `{"scripts":{"dev":"vite dev"},"devDependencies":{"vite":"6"}}`,
		"version.txt":            version,
		"node_modules/.bin/vite": localHMRFixture,
		".gregaleignore":         "package-lock.json\n",
	})
	if err := os.Chmod(filepath.Join(dir, "node_modules", ".bin", "vite"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeLocalHMRFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitLocalHMRLog(t *testing.T, out *localAppTestOutput, text string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), text) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("missing %q: %s", text, out.String())
}

func TestDevLocalHTTPAppFrameworkHMR(t *testing.T) {
	dir := writeLocalHMRProject(t, "first")
	envFile := filepath.Join(t.TempDir(), "external.env")
	writeLocalHMRFile(t, envFile, "DATABASE_URL=first-db\n")
	address, _, out, _ := startLocalAppCLI(t, dir, "--env-file", envFile)
	first := waitLocalAppBody(t, address, "first", out)
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(address, "http")+"/hmr", nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("HMR WebSocket upgrade: %v: %s", err, out.String())
	}
	defer func() { _ = conn.Close() }()
	writeLocalHMRFile(t, filepath.Join(dir, "version.txt"), "second")
	time.Sleep(2 * time.Second) // let Gregale's poll and debounce finish
	second := waitLocalAppBody(t, address, "second", out)
	if second["pid"] != first["pid"] {
		t.Fatalf("source edit restarted the HMR server: %v -> %v: %s", first, second, out.String())
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte("probe after edit")); err != nil {
		t.Fatal(err)
	}
	var socketState map[string]any
	if err := conn.ReadJSON(&socketState); err != nil || socketState["version"] != "second" || socketState["pid"] != first["pid"] {
		t.Fatalf("existing HMR connection did not survive: %v %v", socketState, err)
	}
	writeLocalHMRFile(t, envFile, "DATABASE_URL=second-db\n")
	restarted := waitLocalAppBody(t, address, "second-db", out)
	if restarted["pid"] == first["pid"] {
		t.Fatal("external selected env file did not restart HMR server")
	}
	writeLocalHMRFile(t, envFile, "INVALID ENV LINE\n")
	waitLocalHMRLog(t, out, "Could not reload local config:")
	if next := waitLocalAppBody(t, address, "second-db", out); next["pid"] != restarted["pid"] {
		t.Fatal("invalid selected env file stopped a running HMR server")
	}
	writeLocalHMRFile(t, envFile, "DATABASE_URL=third-db\n")
	next := waitLocalAppBody(t, address, "third-db", out)
	if next["pid"] == restarted["pid"] {
		t.Fatal("repaired selected env file did not restart HMR server")
	}
	restarted = next
	for _, tc := range []struct{ name, content string }{
		{"package-lock.json", `{"lockfileVersion":3}`},
		{"package.json", `{"scripts":{"dev":"vite dev --mode test"},"devDependencies":{"vite":"6"}}`},
		{"gregale.yaml", "dev:\n  watch:\n    command: node node_modules/.bin/vite --host 127.0.0.1 --port $PORT\n"},
	} {
		t.Run(tc.name+" restarts", func(t *testing.T) {
			if tc.name == "gregale.yaml" {
				writeLocalHMRFile(t, filepath.Join(dir, tc.name), "dev: [invalid\n")
				waitLocalHMRLog(t, out, "Could not reload local app:")
				if next := waitLocalAppBody(t, address, "third-db", out); next["pid"] != restarted["pid"] {
					t.Fatal("invalid manifest stopped the running framework server")
				}
			}
			before := strings.Count(out.String(), "Reloading local app (")
			writeLocalHMRFile(t, filepath.Join(dir, tc.name), tc.content)
			deadline := time.Now().Add(10 * time.Second)
			for strings.Count(out.String(), "Reloading local app (") == before && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			next := waitLocalAppBody(t, address, "third-db", out)
			if next["pid"] == restarted["pid"] {
				t.Fatalf("launch input did not restart server: %s", out.String())
			}
			time.Sleep(1500 * time.Millisecond)
			if got := strings.Count(out.String(), "Reloading local app ("); got != before+1 {
				t.Fatalf("expected one restart, got %d: %s", got-before, out.String())
			}
			restarted = next
		})
	}
	// The manifest changed ownership to a custom command. Source saves must
	// now restart it, even though the process was originally framework-owned.
	writeLocalHMRFile(t, filepath.Join(dir, "version.txt"), "custom")
	waitLocalHMRLog(t, out, "Reloading local app (custom command:")
	time.Sleep(2 * time.Second)
	if next := waitLocalAppBody(t, address, "custom", out); next["pid"] == restarted["pid"] {
		t.Fatalf("custom command retained stale HMR ownership: %s", out.String())
	}
}

func TestDevLocalHTTPAppFrameworkNoReload(t *testing.T) {
	dir := writeLocalHMRProject(t, "first")
	envFile := filepath.Join(t.TempDir(), "external.env")
	writeLocalHMRFile(t, envFile, "DATABASE_URL=original-db\n")
	address, _, out, _ := startLocalAppCLI(t, dir, "--env-file", envFile, "--no-reload")
	first := waitLocalAppBody(t, address, "first", out)
	writeLocalHMRFile(t, filepath.Join(dir, "version.txt"), "second")
	writeLocalHMRFile(t, filepath.Join(dir, "package-lock.json"), `{"lockfileVersion":3}`)
	writeLocalHMRFile(t, envFile, "DATABASE_URL=changed-db\n")
	time.Sleep(2 * time.Second)
	next := waitLocalAppBody(t, address, "second", out)
	if next["pid"] != first["pid"] || next["database"] != "original-db" {
		t.Fatalf("--no-reload restarted the framework or changed its env: %v -> %v", first, next)
	}
}

func TestDevLocalHTTPAppFrameworkCrashRecovery(t *testing.T) {
	dir := writeLocalHMRProject(t, "crash")
	address, _, out, _ := startLocalAppCLI(t, dir)
	waitLocalHMRLog(t, out, "Fix the source and save")
	writeLocalHMRFile(t, filepath.Join(dir, "version.txt"), "recovered")
	waitLocalAppBody(t, address, "recovered", out)
	resp, err := (&http.Client{Timeout: time.Second}).Get(address + "/crash")
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	decodeErr := json.NewDecoder(resp.Body).Decode(&state)
	_ = resp.Body.Close()
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for strings.Count(out.String(), "Fix the source and save") < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Count(out.String(), "Fix the source and save") < 2 {
		t.Fatalf("runtime crash was not reported: %s", out.String())
	}
	writeLocalHMRFile(t, filepath.Join(dir, "version.txt"), "recovered-again")
	if next := waitLocalAppBody(t, address, "recovered-again", out); next["pid"] == state["pid"] {
		t.Fatal("crashed framework process was not replaced")
	}
}
