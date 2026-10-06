package e2etest

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
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

// The image build is a static Linux/amd64 binary matching OCI metadata: nothing in the
// scratch image can satisfy a dynamic loader.
func TestHelloServerBinary_IsStaticLinux(t *testing.T) {
	b, err := helloServerBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 4 || string(b[1:4]) != "ELF" {
		t.Fatal("fixture binary is not an ELF executable")
	}
	image, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	if image.Machine != elf.EM_X86_64 {
		t.Fatalf("fixture architecture %s, want x86_64", image.Machine)
	}
	if strings.Contains(string(b), "ld-linux") || strings.Contains(string(b), "/lib64/ld") {
		t.Fatal("fixture binary references a dynamic loader; the image has no libc")
	}
}

func TestHelloServer_ProcessContract(t *testing.T) {
	t.Setenv("FIXTURE_MARKER", "container-contract")
	t.Setenv("UNRELATED_SECRET", "must-not-be-reported")
	_, addr := startHelloServer(t, "-contract")
	status, body := get(t, "http://"+addr+"/contract")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	var got struct {
		UID, GID   int
		WorkingDir string `json:"working_dir"`
		Marker     string
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got.UID != os.Getuid() || got.GID != os.Getgid() || got.WorkingDir != cwd || got.Marker != "container-contract" {
		t.Fatalf("unexpected process evidence: %+v", got)
	}
	if strings.Contains(body, "must-not-be-reported") {
		t.Fatal("unrelated environment leaked")
	}
}

func TestHelloServer_ExecProbeContract(t *testing.T) {
	t.Setenv("FIXTURE_MARKER", "container-contract")
	t.Setenv("DEPLOYMENT_MARKER", "runtime-deployment")
	t.Setenv("UNRELATED_SECRET", "must-not-be-reported")
	server, addr := startHelloServer(t, "-contract")
	_, _ = get(t, "http://"+addr+"/contract")
	probeDir := t.TempDir()
	for count := 1; count <= 2; count++ {
		cmd := exec.Command(server.Path, "-addr", addr, "-probe-contract")
		cmd.Dir = probeDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture exec probe: %v: %s", err, out)
		}
		status, body := get(t, "http://"+addr+"/contract")
		if status != http.StatusOK {
			t.Fatalf("contract status=%d body=%s", status, body)
		}
		var got struct {
			Probe struct {
				UID, GID, Count  int
				WorkingDir       string `json:"working_dir"`
				Marker           string
				DeploymentMarker string `json:"deployment_marker"`
			}
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		probe := got.Probe
		if probe.UID != os.Getuid() || probe.GID != os.Getgid() || probe.Count != count || probe.WorkingDir != probeDir || probe.Marker != "container-contract" || probe.DeploymentMarker != "runtime-deployment" {
			t.Fatalf("incorrect exec-probe evidence: %+v", probe)
		}
		if strings.Contains(body, "must-not-be-reported") {
			t.Fatal("unrelated environment leaked into probe evidence")
		}
	}
}
