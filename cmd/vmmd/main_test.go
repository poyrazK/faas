// Tests for the vmmd daemon entrypoint. The actual VM work needs KVM+root
// (//go:build metal); here we only exercise the run() orchestration through
// the dependency-injection seam so the listener/metrics/shutdown paths are
// covered on a vanilla dev box.

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
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/wire"
)

// nopHostKeyDeps returns host-key dep fields that simulate "host key
// already on disk" — LoadHostKey returns a freshly generated identity,
// genAndSaveKey is unreachable (the not-found path is not exercised),
// and WriteRecipientFile is a no-op so the test doesn't need to write
// into /etc/faas/secrets/.
//
// Production uses /etc/faas/secrets/host.age which tests can't write
// to. Every existing runDeps{} user calls this helper. The ADR-021
// host-key lifecycle tests below exercise the real secretbox path
// against a temp dir.
func nopHostKeyDeps(t *testing.T) (loadHostKey func(string) (*age.X25519Identity, error), writeRecipient func(string, *age.X25519Identity) error) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("nopHostKeyDeps: GenerateX25519Identity: %v", err)
	}
	return func(string) (*age.X25519Identity, error) { return id, nil },
		func(string, *age.X25519Identity) error { return nil }
}

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

	load, write := nopHostKeyDeps(t)
	deps := runDeps{
		configPath: cfgPath,
		detectFC:   func(context.Context) (string, error) { return "1.7.0-test", nil },
		listen: func(_ context.Context, target string, _ *tls.Config, _ string) (net.Listener, error) {
			t, err := wire.ParseTarget(target)
			if err != nil {
				return nil, err
			}
			return net.Listen("unix", t.Address)
		},
		loadHostKey:    load,
		writeRecipient: write,
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
	load, write := nopHostKeyDeps(t)
	deps := runDeps{
		configPath:     cfgPath,
		detectFC:       func(context.Context) (string, error) { return "1.7.0", nil },
		listen:         func(context.Context, string, *tls.Config, string) (net.Listener, error) { return nil, wantErr },
		loadHostKey:    load,
		writeRecipient: write,
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

	load, write := nopHostKeyDeps(t)
	deps := runDeps{
		configPath: cfgPath,
		detectFC:   func(context.Context) (string, error) { return "", errors.New("no fc on host") },
		listen: func(_ context.Context, target string, _ *tls.Config, _ string) (net.Listener, error) {
			t, err := wire.ParseTarget(target)
			if err != nil {
				return nil, err
			}
			return net.Listen("unix", t.Address)
		},
		loadHostKey:    load,
		writeRecipient: write,
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
	if d.loadHostKey == nil {
		t.Error("loadHostKey must default to secretbox.LoadHostKey")
	}
	if d.genAndSaveKey == nil {
		t.Error("genAndSaveKey must default to secretbox.GenerateAndSaveHostKey")
	}
	if d.writeRecipient == nil {
		t.Error("writeRecipient must default to secretbox.WriteRecipientFile")
	}
}

// TestLoadOrGenerateHostIdentity_FirstBoot covers the G2 ship-blocker
// scenario: LoadHostKey returns ErrHostKeyNotFound, the helper must
// call GenerateAndSaveHostKey to create a fresh X25519 identity and
// then WriteRecipientFile to publish the public recipient. Without
// this, Manager.Wake refuses any app that PUT a secret.
//
// The helper is exercised directly (not via runWithDeps) so the test
// doesn't block on the gRPC Serve loop.
func TestLoadOrGenerateHostIdentity_FirstBoot(t *testing.T) {
	generated, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("age.GenerateX25519Identity: %v", err)
	}
	var wroteID *age.X25519Identity
	genCalled := false
	deps := runDeps{
		loadHostKey: func(string) (*age.X25519Identity, error) {
			return nil, secretbox.ErrHostKeyNotFound
		},
		genAndSaveKey: func(string) (*age.X25519Identity, error) {
			if genCalled {
				t.Error("GenerateAndSaveHostKey called more than once")
			}
			genCalled = true
			return generated, nil
		},
		writeRecipient: func(_ string, id *age.X25519Identity) error {
			wroteID = id
			return nil
		},
	}
	id, _, _, err := loadOrGenerateHostIdentity(deps, "/x/host.age", "/x/host.age.pub")
	if err != nil {
		t.Fatalf("helper returned err = %v", err)
	}
	if !genCalled {
		t.Error("GenerateAndSaveHostKey was not called on first-boot")
	}
	if wroteID == nil {
		t.Fatal("WriteRecipientFile was not called")
	}
	if id.Recipient().String() != generated.Recipient().String() {
		t.Errorf("returned identity = %s, want %s", id.Recipient(), generated.Recipient())
	}
}

// TestLoadOrGenerateHostIdentity_Restart covers the restart path:
// LoadHostKey returns a valid identity, GenerateAndSave must NOT be
// called (a restarted vmmd that regenerated would invalidate every
// sealed secret on the box). The identity that flows into
// writeRecipient must equal the loaded one.
func TestLoadOrGenerateHostIdentity_Restart(t *testing.T) {
	loaded, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("age.GenerateX25519Identity: %v", err)
	}
	genCalled := false
	var wroteID *age.X25519Identity
	deps := runDeps{
		loadHostKey: func(string) (*age.X25519Identity, error) { return loaded, nil },
		genAndSaveKey: func(string) (*age.X25519Identity, error) {
			genCalled = true
			return nil, errors.New("must not be called on restart")
		},
		writeRecipient: func(_ string, id *age.X25519Identity) error {
			wroteID = id
			return nil
		},
	}
	id, _, _, err := loadOrGenerateHostIdentity(deps, "/x/host.age", "/x/host.age.pub")
	if err != nil {
		t.Fatalf("helper returned err = %v", err)
	}
	if genCalled {
		t.Error("GenerateAndSaveHostKey was called on restart — would invalidate every sealed secret on the box")
	}
	if wroteID == nil {
		t.Fatal("WriteRecipientFile was not called")
	}
	if id.Recipient().String() != loaded.Recipient().String() {
		t.Errorf("restart identity = %s, want %s (loaded from disk)", id.Recipient(), loaded.Recipient())
	}
}

// TestLoadOrGenerateHostIdentity_WriteRecipientFailure asserts a
// failed WriteRecipientFile aborts the lifecycle (the recipient file
// is the only way apid / builderd can seal secrets, so a missing
// recipient would silently break PUTs at runtime — fail loud).
func TestLoadOrGenerateHostIdentity_WriteRecipientFailure(t *testing.T) {
	loaded, _ := age.GenerateX25519Identity()
	wantErr := errors.New("disk full")
	deps := runDeps{
		loadHostKey:    func(string) (*age.X25519Identity, error) { return loaded, nil },
		writeRecipient: func(string, *age.X25519Identity) error { return wantErr },
	}
	_, _, _, err := loadOrGenerateHostIdentity(deps, "/x/host.age", "/x/host.age.pub")
	if err == nil {
		t.Fatal("expected writeRecipient failure to propagate")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wraps %v", err, wantErr)
	}
}
