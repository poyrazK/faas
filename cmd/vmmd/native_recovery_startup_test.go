// adr: 532 — recover ownership before vmmd exposes admission.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestRunNativeRecoveryFailureStopsBeforeListening(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "vmmd.toml")
	if err := os.WriteFile(config, []byte("native_process_recovery = true\nprepared_networks = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	load, write := nopHostKeyDeps(t)
	cause := errors.New("native ownership cannot be established")
	called := false
	deps := runDeps{
		configPath: config, detectFC: func(context.Context) (string, error) { return "1.7.0", nil },
		loadHostKey: load, loadHostKeys: nopHostKeysDep(t), writeRecipient: write, capCheck: nopCapCheck(),
		recoverNativeProcesses: func(context.Context, *fcvm.Manager) error { called = true; return cause },
		listen: func(context.Context, string, *tls.Config, string) (net.Listener, error) {
			t.Fatal("recovery failure exposed RPC listener")
			return nil, nil
		},
	}
	err := runWithDeps(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	if !called || !errors.Is(err, cause) {
		t.Fatalf("recovery called=%v err=%v", called, err)
	}
}

func TestNativeRecoveryConfigurationIsExplicitOptIn(t *testing.T) {
	config := filepath.Join(t.TempDir(), "vmmd.toml")
	cfg, err := LoadConfig(config)
	if err != nil || cfg.NativeProcessRecovery {
		t.Fatalf("default opt-in=%v err=%v", cfg.NativeProcessRecovery, err)
	}
	if err := os.WriteFile(config, []byte("native_process_recovery = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(config)
	if err != nil || !cfg.NativeProcessRecovery {
		t.Fatalf("configured opt-in=%v err=%v", cfg.NativeProcessRecovery, err)
	}
}
