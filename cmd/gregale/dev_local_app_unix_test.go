//go:build unix

package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestDevLocalAppCLIHelper(t *testing.T) {
	if os.Getenv("GREGALE_LOCAL_APP_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(main.Run(os.Args[i+1:]))
		}
	}
	os.Exit(2)
}

type localAppTestOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *localAppTestOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *localAppTestOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func localAppTestPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func startLocalAppCLI(t *testing.T, dir string, extra ...string) (string, *exec.Cmd, *localAppTestOutput, <-chan error) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	port := localAppTestPort(t)
	args := []string{"-test.run=^TestDevLocalAppCLIHelper$", "--", "dev", "--local", "--no-remote", "--path", dir, "--port", port}
	args = append(args, extra...)
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "GREGALE_LOCAL_APP_CLI_HELPER=1", "XDG_CACHE_HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir())
	out := &localAppTestOutput{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Errorf("CLI did not shut down: %s", out.String())
		}
	})
	return "http://127.0.0.1:" + port, cmd, out, done
}

func waitLocalAppBody(t *testing.T, address, want string, out *localAppTestOutput) map[string]any {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(address)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), want) {
				var parsed map[string]any
				if err := json.Unmarshal(body, &parsed); err != nil {
					t.Fatal(err)
				}
				return parsed
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("local app did not return %q: %s", want, out.String())
	return nil
}

func localNodeApp(version string) string {
	return fmt.Sprintf(`const http = require('node:http');
const version = %q;
http.createServer((req,res) => {
  res.setHeader('content-type','application/json');
  res.end(JSON.stringify({version, pid:process.pid, path:req.url,
    database:process.env.DATABASE_URL, bucket:process.env.GREGALE_S3_UPLOADS_BUCKET,
    local:process.env.GREGALE_LOCAL, id:req.headers['x-faas-invocation-id'],
    source:req.headers['x-faas-invocation-source'], token:req.headers['x-gregale-local-token']}));
}).listen(Number(process.env.PORT), process.env.HOST);
`, version)
}

func TestDevLocalHTTPAppReloadAndResourceEnv(t *testing.T) {
	dir := writeDevLocalProject(t, map[string]string{
		"package.json": `{"scripts":{"dev":"node server.cjs"}}`,
		"server.cjs":   localNodeApp("first"),
		"app.env":      "DATABASE_URL=postgresql://local-fixture\nGREGALE_S3_UPLOADS_BUCKET=uploads\n",
	})
	address, _, out, _ := startLocalAppCLI(t, dir, "--env-file", filepath.Join(dir, "app.env"))
	first := waitLocalAppBody(t, address+"/api?test=1", "first", out)
	if first["database"] != "postgresql://local-fixture" || first["bucket"] != "uploads" || first["local"] != "1" || first["path"] != "/api?test=1" {
		t.Fatalf("app lost environment or routing: %v", first)
	}
	req, _ := http.NewRequest(http.MethodGet, address, nil)
	req.Header.Set("X-Faas-Invocation-Id", "spoofed")
	req.Header.Set("X-Faas-Invocation-Source", "cron")
	req.Header.Set(main.DevLocalTokenHeader, "do-not-forward")
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]any
	err = json.NewDecoder(resp.Body).Decode(&headers)
	_ = resp.Body.Close()
	if err != nil || headers["id"] == "spoofed" || headers["id"] == nil || headers["source"] != nil || headers["token"] != nil {
		t.Fatalf("proxy forwarded untrusted platform headers: %v %v", headers, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.cjs"), []byte(localNodeApp("second")), 0o600); err != nil {
		t.Fatal(err)
	}
	second := waitLocalAppBody(t, address, "second", out)
	if second["pid"] == first["pid"] {
		t.Fatal("source change did not restart the app")
	}
}

func TestDevLocalHTTPAppRecoversAfterStartupCrash(t *testing.T) {
	dir := writeDevLocalProject(t, map[string]string{
		"package.json": `{"scripts":{"start":"node server.cjs"}}`,
		"server.cjs":   "throw new Error('startup fixture failure');\n",
	})
	address, _, out, _ := startLocalAppCLI(t, dir)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "Fix the source and save") && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "Fix the source and save") {
		t.Fatalf("startup crash was not reported: %s", out.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "server.cjs"), []byte(localNodeApp("recovered")), 0o600); err != nil {
		t.Fatal(err)
	}
	waitLocalAppBody(t, address, "recovered", out)
}

func TestDevLocalHTTPAppNoReload(t *testing.T) {
	dir := writeDevLocalProject(t, map[string]string{"server.cjs": localNodeApp("original")})
	address, _, out, _ := startLocalAppCLI(t, dir, "--watch-command", "node server.cjs", "--no-reload")
	waitLocalAppBody(t, address, "original", out)
	if err := os.WriteFile(filepath.Join(dir, "server.cjs"), []byte(localNodeApp("changed")), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	waitLocalAppBody(t, address, "original", out)
}

func TestDevLocalHTTPAppStopsDescendants(t *testing.T) {
	childPort := localAppTestPort(t)
	child := `const http=require('node:http'); process.on('SIGTERM',()=>{}); http.createServer((q,s)=>s.end('child')).listen(` + childPort + `,'127.0.0.1');`
	dir := writeDevLocalProject(t, map[string]string{
		"package.json": `{"scripts":{"dev":"node server.cjs"}}`,
		"server.cjs":   `require('node:child_process').spawn(process.execPath,['-e',` + strconv.Quote(child) + `],{stdio:'inherit'});` + localNodeApp("parent"),
	})
	address, cmd, out, done := startLocalAppCLI(t, dir)
	waitLocalAppBody(t, address, "parent", out)
	childAddr := "127.0.0.1:" + childPort
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", childAddr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	conn, err := net.DialTimeout("tcp", childAddr, time.Second)
	if err != nil {
		t.Fatalf("descendant did not start: %v %s", err, out.String())
	}
	_ = conn.Close()
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CLI shutdown: %v %s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("CLI did not terminate its app: %s", out.String())
	}
	conn, err = net.DialTimeout("tcp", childAddr, time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("descendant that ignored SIGTERM survived CLI shutdown")
	}
}
