// Tests for the vmmd daemon entrypoint. The actual VM work needs KVM+root
// (//go:build metal); here we only exercise the run() orchestration through
// the dependency-injection seam so the listener/metrics/shutdown paths are
// covered on a vanilla dev box.

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortDir mirrors the helper in pkg/wire's test (kept private here so this
// test package doesn't pull in wire's internals). macOS sun_path is ~104.
func shortDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	short := "/tmp/fwst-vmmd-" + t.Name()
	if err := os.Symlink(root, short); err != nil {
		return root
	}
	t.Cleanup(func() { _ = os.Remove(short) })
	return short
}

func TestRun_BadConfigPath(t *testing.T) {
	// LoadConfig treats ENOENT as defaults — to exercise the error branch
	// we feed it a directory, which fails the read with non-ENOENT.
	dir := t.TempDir()
	deps := runDeps{configPath: dir}
	err := runWithDeps(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	if err == nil {
		t.Fatal("expected error from directory-as-config-path")
	}
}

func TestRun_DrainsOnCancel(t *testing.T) {
	// Happy-path early lifecycle: load config (defaults), skip FC detect via
	// injection, listen on a temp unix socket, then cancel.
	dir := shortDir(t)
	cfgPath := filepath.Join(dir, "vmmd.toml")
	if err := os.WriteFile(cfgPath, []byte("socket_path = \""+filepath.Join(dir, "vmmd.sock")+"\"\nowner_user = \"root\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	deps := runDeps{
		configPath: cfgPath,
		detectFC:   func(context.Context) (string, error) { return "1.7.0-test", nil },
		listen:     func(path, _ string) (net.Listener, error) { return net.Listen("unix", path) },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), deps) }()

	// Give the goroutine a beat to bind the listener.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil on clean drain", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return within 3s of cancel")
	}
}

func TestRun_ListenFailurePropagates(t *testing.T) {
	// If the listener cannot be created, run must return that error.
	dir := shortDir(t)
	cfgPath := filepath.Join(dir, "vmmd.toml")
	if err := os.WriteFile(cfgPath, []byte("socket_path = \""+filepath.Join(dir, "x.sock")+"\"\nowner_user = \"root\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("listen broken")
	deps := runDeps{
		configPath: cfgPath,
		detectFC:   func(context.Context) (string, error) { return "1.7.0", nil },
		listen:     func(string, string) (net.Listener, error) { return nil, wantErr },
	}
	err := runWithDeps(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	if err == nil {
		t.Fatal("listen failure should propagate")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wraps %v", err, wantErr)
	}
}

func TestRun_FCDetectFailureIsWarning(t *testing.T) {
	// fcvm.DetectFirecrackerVersion returning an error is logged as a warning
	// but does NOT abort run; we verify that by listening on a fake socket
	// then cancelling.
	dir := shortDir(t)
	cfgPath := filepath.Join(dir, "vmmd.toml")
	if err := os.WriteFile(cfgPath, []byte("socket_path = \""+filepath.Join(dir, "y.sock")+"\"\nowner_user = \"root\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	deps := runDeps{
		configPath: cfgPath,
		detectFC:   func(context.Context) (string, error) { return "", errors.New("no fc on host") },
		listen:     func(path, _ string) (net.Listener, error) { return net.Listen("unix", path) },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), deps) }()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil despite fc-detect failure (it must warn, not fail)", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return within 3s of cancel")
	}
}

func TestDefaultDeps(t *testing.T) {
	d := defaultDeps()
	if d.configPath != "/etc/faas/vmmd.toml" {
		t.Errorf("configPath = %q, want default", d.configPath)
	}
	if d.detectFC == nil || d.listen == nil {
		t.Error("deps must include non-nil detectFC and listen")
	}
}
