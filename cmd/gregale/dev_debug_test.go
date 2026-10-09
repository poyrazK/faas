package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
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
		"https://api.gregale.dev":      "wss://api.gregale.dev/v1/apps/dev-api-1/debug",
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
	if _, err := conn.Write([]byte("GET /json/list")); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(conn)
	if err != nil || string(body) != "inspector:GET /json/list" {
		t.Fatalf("proxied reply = %q, %v", body, err)
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
