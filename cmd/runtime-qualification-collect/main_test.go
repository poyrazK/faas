package main

// adr: 601

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequalification"
	"github.com/onebox-faas/faas/pkg/state"
)

func collectorArgs(t *testing.T) ([]string, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	release := state.RuntimeRelease{Runtime: "node22", Architecture: "amd64", SourceRef: "ghcr.io/test/node@sha256:" + strings.Repeat("a", 64), GuestInitSHA256: strings.Repeat("b", 64), BaseSHA256: strings.Repeat("c", 64), LayoutVersion: "test"}
	release.ID = release.Identity()
	fixture := runtimequalification.Fixture{Target: release, RunID: uuid.NewString(), HostID: uuid.NewString(), SourceCommit: strings.Repeat("d", 40), DeploymentID: uuid.NewString(), LayerKey: "apps/fixture.ext4", LayerSHA256: strings.Repeat("e", 64), KernelSHA256: strings.Repeat("f", 64), FirecrackerSHA256: strings.Repeat("1", 64)}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fixture.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "signer.seed"), key.Seed(), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"-fixture", path, "-signing-seed-file", filepath.Join(dir, "signer.seed"), "-public-key", hex.EncodeToString(pub), "-release", release.ID, "-host", fixture.HostID, "-source-commit", fixture.SourceCommit, "-run", fixture.RunID, "-source-dir", filepath.Join(dir, "source"), "-go", filepath.Join(dir, "go"), "-go-sha256", strings.Repeat("2", 64), "-kernel", filepath.Join(dir, "kernel"), "-firecracker-version", "1.7.0", "-output-dir", filepath.Join(dir, "evidence")}, key
}

func TestCollectorCLINativeGuardBeforeKeyAndInfrastructure(t *testing.T) {
	args, _ := collectorArgs(t)
	rejected := errors.New("not designated native host")
	keyLoaded, opened := false, false
	deps := dependencies{guard: func(context.Context) error { return rejected }, loadKey: func(string, ed25519.PublicKey) (ed25519.PrivateKey, error) { keyLoaded = true; return nil, nil }, open: func(context.Context) (resources, error) { opened = true; return resources{}, nil }}
	if err := run(t.Context(), args, deps, &bytes.Buffer{}); !errors.Is(err, rejected) || keyLoaded || opened {
		t.Fatal("native guard bypassed", err, keyLoaded, opened)
	}
}

func TestCollectorCLIRejectsEvidenceAndSecretPlacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]string) []string
	}{{"wrong target", func(a []string) []string { a[7] = strings.Repeat("3", 64); return a }}, {"key inside source", func(a []string) []string { a[3] = filepath.Join(a[15], "signer.seed"); return a }}, {"key inside output", func(a []string) []string { a[3] = filepath.Join(a[25], "signer.seed"); return a }}, {"missing flags", func([]string) []string { return nil }}} {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := collectorArgs(t)
			args = tc.edit(args)
			loaded, opened := false, false
			deps := dependencies{guard: func(context.Context) error { return nil }, loadKey: func(string, ed25519.PublicKey) (ed25519.PrivateKey, error) { loaded = true; return nil, nil }, open: func(context.Context) (resources, error) { opened = true; return resources{}, nil }}
			if err := run(t.Context(), args, deps, &bytes.Buffer{}); err == nil || loaded || opened {
				t.Fatal("invalid request opened sensitive resources", err, loaded, opened)
			}
		})
	}
}

func TestCollectorCLIKeyClearingResourceCleanupAndHelp(t *testing.T) {
	args, key := collectorArgs(t)
	closed := false
	deps := dependencies{guard: func(context.Context) error { return nil }, loadKey: func(string, ed25519.PublicKey) (ed25519.PrivateKey, error) { return key, nil }, open: func(context.Context) (resources, error) { return resources{close: func() { closed = true }}, nil }, native: func(context.Context, runtimequalification.NativeConfig, runtimequalification.Fixture) (runtimequalification.NativeSession, error) {
		t.Fatal("invalid resources reached native session")
		return nil, nil
	}}
	if err := run(t.Context(), args, deps, &bytes.Buffer{}); err == nil || !closed || !bytes.Equal(key, make([]byte, ed25519.PrivateKeySize)) {
		t.Fatal("failed collector retained resources/key", err, closed)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-help"}, dependencies{}, &out); !errors.Is(err, flag.ErrHelp) || !strings.Contains(out.String(), "signing-seed-file") {
		t.Fatal("collector help failed", err)
	}
}

func TestCollectorCLIRejectsAliasedSigningLocation(t *testing.T) {
	args, _ := collectorArgs(t)
	// Lexically outside source, physically inside it through a directory alias.
	seed := filepath.Join(args[15], "signer.seed")
	if err := os.WriteFile(seed, make([]byte, ed25519.SeedSize), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(args[15]), "source-alias")
	if err := os.Symlink(args[15], alias); err != nil {
		t.Fatal(err)
	}
	args[3] = filepath.Join(alias, "signer.seed")
	loaded := false
	deps := dependencies{guard: func(context.Context) error { return nil }, loadKey: func(string, ed25519.PublicKey) (ed25519.PrivateKey, error) { loaded = true; return nil, nil }}
	if err := run(t.Context(), args, deps, &bytes.Buffer{}); err == nil || loaded {
		t.Fatal("aliased source exposed signing seed", err)
	}
}
