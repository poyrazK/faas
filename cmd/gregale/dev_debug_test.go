package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeDebugSecrets struct {
	keys  map[string]string
	unset []string
}

func (f *fakeDebugSecrets) SetSecretWithScope(_ context.Context, _, key, value, _ string) error {
	f.keys[key] = value
	return nil
}

func (f *fakeDebugSecrets) ListSecretsWithScope(context.Context, string, string) (api.AppSecretListResponse, error) {
	var list api.AppSecretListResponse
	for key := range f.keys {
		list.Secrets = append(list.Secrets, api.AppSecretResponse{Key: key})
	}
	return list, nil
}

func (f *fakeDebugSecrets) UnsetSecretWithScope(_ context.Context, _, key, _ string) error {
	f.unset = append(f.unset, key)
	delete(f.keys, key)
	return nil
}

func TestConfigureDevDebugTurnsInspectorOnAndOff(t *testing.T) {
	secrets := &fakeDebugSecrets{keys: map[string]string{"OTHER": "x"}}
	if err := configureDevDebug(context.Background(), secrets, "dev-api", true); err != nil {
		t.Fatal(err)
	}
	if secrets.keys[api.DevDebugEnv] != api.DevDebugRuntimeNode {
		t.Fatalf("secrets after enable = %v", secrets.keys)
	}
	if err := configureDevDebug(context.Background(), secrets, "dev-api", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets.keys[api.DevDebugEnv]; ok || len(secrets.unset) != 1 || secrets.keys["OTHER"] != "x" {
		t.Fatalf("secrets after disable = %v unset %v; want only the debug setting removed", secrets.keys, secrets.unset)
	}
	if err := configureDevDebug(context.Background(), secrets, "dev-api", false); err != nil || len(secrets.unset) != 1 {
		t.Fatalf("disable without a setting unset %v, err %v; want no call", secrets.unset, err)
	}
}

func TestDevDebugTunnelURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://api.gregale.dev":       "wss://api.gregale.dev/v1/apps/dev-api-1/debug",
		"http://127.0.0.1:8080/prefix/": "ws://127.0.0.1:8080/prefix/v1/apps/dev-api-1/debug",
	} {
		if got, err := devDebugTunnelURL(base, "dev-api-1"); err != nil || got != want {
			t.Fatalf("devDebugTunnelURL(%q) = %q, %v; want %q", base, got, err, want)
		}
	}
}

func TestDevDebugSupportedOnlyForNode(t *testing.T) {
	nodeDir, pythonDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(nodeDir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !devDebugSupported(nodeDir, devSourceConfig{}) || devDebugSupported(pythonDir, devSourceConfig{}) {
		t.Fatal("app detection should accept Node and reject other runtimes")
	}
	if !devDebugSupported(pythonDir, devSourceConfig{shape: shapeFunction, runtime: "node22"}) ||
		devDebugSupported(nodeDir, devSourceConfig{shape: shapeFunction, runtime: "python312"}) {
		t.Fatal("function detection should follow the runtime")
	}
}

func TestDevDebugProxyJoinsLocalConnectionToTunnel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dial := func(context.Context) (net.Conn, error) {
		local, remote := net.Pipe()
		go func() {
			defer func() { _ = remote.Close() }()
			buf := make([]byte, 64)
			n, _ := remote.Read(buf)
			_, _ = remote.Write(append([]byte("inspector:"), buf[:n]...))
		}()
		return local, nil
	}
	address, err := startDevDebugProxy(ctx, 0, dial, func(err error) { t.Errorf("proxy error: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("hello inspector")); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(conn)
	if err != nil || string(body) != "inspector:hello inspector" {
		t.Fatalf("proxied reply = %q, %v", body, err)
	}
}

// Discovery polls (chrome://inspect asks every second) are answered from a
// short cache instead of opening a tunnel each; the WebSocket that attaches
// the debugger always gets its own tunnel, its head forwarded intact.
func TestDevDebugProxyCachesDiscoveryAndTunnelsAttach(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var dials atomic.Int32
	attachHead := make(chan string, 1)
	dial := func(context.Context) (net.Conn, error) {
		dials.Add(1)
		local, remote := net.Pipe()
		go func() {
			defer func() { _ = remote.Close() }()
			reader := bufio.NewReader(remote)
			request, err := http.ReadRequest(reader)
			if err != nil {
				return
			}
			if request.Header.Get("Upgrade") != "" {
				attachHead <- request.Method + " " + request.URL.Path + " " + request.Header.Get("Sec-WebSocket-Key")
				_, _ = remote.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n\r\n"))
				return
			}
			body := `[{"id":"target-1","webSocketDebuggerUrl":"ws://` + request.Host + `/target-1"}]`
			_, _ = remote.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body))
		}()
		return local, nil
	}
	address, err := startDevDebugProxy(ctx, 0, dial, func(err error) { t.Errorf("proxy error: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for range 3 {
		response, err := client.Get("http://" + address + "/json/list")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "ws://"+address+"/target-1") {
			t.Fatalf("discovery = %d %q", response.StatusCode, body)
		}
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("three discovery polls opened %d tunnels, want 1", got)
	}

	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	upgrade := "GET /target-1 HTTP/1.1\r\nHost: " + address + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: abc\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(upgrade)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-attachHead:
		if got != "GET /target-1 abc" {
			t.Fatalf("attach reached the inspector as %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach request never reached a tunnel")
	}
	if got := dials.Load(); got != 2 {
		t.Fatalf("attach used %d tunnels in total, want its own", got)
	}
}

func TestDevDebugUnsupportedForBunAndDeno(t *testing.T) {
	for _, marker := range []string{"bun.lockb", "bun.lock", "deno.json"} {
		dir := t.TempDir()
		for _, name := range []string{"package.json", marker} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if devDebugSupported(dir, devSourceConfig{}) {
			t.Errorf("a project with %s reported Node.js debugger support", marker)
		}
	}
}

func TestCmdDevDebugFlagValidation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, args := range [][]string{
		{"--name", "debug-demo", "--debug", "--once"},
		{"--name", "debug-demo", "--debug", "--stop"},
		{"--name", "debug-demo", "--debug", "--debug-port", "70000"},
	} {
		if code := cmdDev(args); code == 0 {
			t.Fatalf("cmdDev(%v) = 0, want a flag error", args)
		}
	}
}
