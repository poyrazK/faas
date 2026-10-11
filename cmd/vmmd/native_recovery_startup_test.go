// adr: 568 — recover ownership before vmmd exposes admission.
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

func TestNativeSnapshotPublicationRootRequiresNativeRecoveryAndCleanAbsolutePath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recovery bool
		root     string
		wantErr  bool
	}{
		{name: "unset", root: ""},
		{name: "configured", recovery: true, root: filepath.Join(string(filepath.Separator), "var", "lib", "faas", "publications")},
		{name: "requires recovery", root: filepath.Join(string(filepath.Separator), "var", "lib", "faas", "publications"), wantErr: true},
		{name: "relative", recovery: true, root: "var/lib/faas/publications", wantErr: true},
		{name: "unclean", recovery: true, root: string(filepath.Separator) + "var/../tmp/publications", wantErr: true},
		{name: "filesystem root", recovery: true, root: string(filepath.Separator), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNativeSnapshotPublicationConfig(tc.recovery, tc.root)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateNativeSnapshotPublicationConfig(%v, %q) err = %v", tc.recovery, tc.root, err)
			}
		})
	}
}

func TestNativeSnapshotPublicationRootLoadsAsExperimentalOptIn(t *testing.T) {
	root := filepath.Join(t.TempDir(), "publications")
	config := filepath.Join(t.TempDir(), "vmmd.toml")
	contents := "native_process_recovery = true\nnative_snapshot_publication_root = \"" + root + "\"\n"
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(config)
	if err != nil || !cfg.NativeProcessRecovery || cfg.NativeSnapshotPublicationRoot != root {
		t.Fatalf("native publication config = %+v, err = %v", cfg, err)
	}
	if err := os.WriteFile(config, []byte("native_snapshot_publication_root = \""+root+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(config); err == nil {
		t.Fatal("publication root was accepted without native process recovery")
	}
}
