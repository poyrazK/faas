package e2etest

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func startHelloServer(t *testing.T, args ...string) (*exec.Cmd, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "hello-server")
	if err := buildHelloServer(runtime.GOOS, runtime.GOARCH, bin); err != nil {
		t.Fatal(err)
	}
	body := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(body, []byte("hello from faas\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addr := freePort(t)
	cmd := exec.Command(bin, append([]string{"-addr", addr, "-body-file", body}, args...)...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return cmd, addr
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var last error
	for time.Now().Before(time.Now().Add(0)) || ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			last = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	t.Fatalf("GET %s never answered: %v", url, last)
	return 0, ""
}

// The fixture is a real app: it serves the round-trip body and a healthcheck.
func TestHelloServer_ServesBodyAndHealth(t *testing.T) {
	_, addr := startHelloServer(t)
	if code, body := get(t, "http://"+addr+"/"); code != 200 || strings.TrimSpace(body) != "hello from faas" {
		t.Fatalf("GET / = %d %q; the wake test asserts exactly this body", code, body)
	}
	if code, _ := get(t, "http://"+addr+"/healthz"); code != 200 {
		t.Fatalf("GET /healthz = %d, want 200", code)
	}
}

// The wedged variant must stay alive without ever binding the port: vmmd's
// liveness classifies it as conn_refused, which is the case that test wants.
func TestHelloServer_NoListenNeverBinds(t *testing.T) {
	cmd, addr := startHelloServer(t, "-no-listen")
	time.Sleep(300 * time.Millisecond)
	if cmd.ProcessState != nil {
		t.Fatal("-no-listen process exited; a wedged fixture must keep running")
	}
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		_ = c.Close()
		t.Fatal("-no-listen bound the port; liveness would see a healthy listener")
	}
}

// The image build is a static linux binary for this arch: nothing in the
// scratch image can satisfy a dynamic loader.
func TestHelloServerBinary_IsStaticLinux(t *testing.T) {
	b, err := helloServerBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 4 || string(b[1:4]) != "ELF" {
		t.Fatal("fixture binary is not an ELF executable")
	}
	if strings.Contains(string(b), "ld-linux") || strings.Contains(string(b), "/lib64/ld") {
		t.Fatal("fixture binary references a dynamic loader; the image has no libc")
	}
}
