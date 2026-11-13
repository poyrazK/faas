package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestLoadRuntimeSealDomain(t *testing.T) {
	dir := t.TempDir()
	if ids, err := loadRuntimeSealDomain(dir); err != nil || len(ids) != 0 {
		t.Fatalf("no domain staged = %d identities, %v; want none and no error", len(ids), err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "domains"), 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := secretbox.WriteHostKeyAtPath(sealdomain.IdentityPath(filepath.Join(dir, "domains"), sealdomain.Runtime), id); err != nil {
		t.Fatal(err)
	}
	ids, err := loadRuntimeSealDomain(dir)
	if err != nil || len(ids) != 1 || ids[0].Recipient().String() != id.Recipient().String() {
		t.Fatalf("staged domain = %v, %v", ids, err)
	}
	loose := sealdomain.IdentityPath(filepath.Join(dir, "domains"), sealdomain.Runtime) + ".previous"
	if err := os.WriteFile(loose, []byte(id.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRuntimeSealDomain(dir); err == nil {
		t.Fatal("loaded a world-readable previous identity")
	}
}

// TestUnsealIdentities: vmmd opens with each domain first and the legacy
// identities after it; a contracted vmmd (ADR 20261022111812) has no legacy
// identities and refuses to start without both domains.
func TestUnsealIdentities(t *testing.T) {
	newID := func() *age.X25519Identity {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	runtime, build, fleet, host := newID(), newID(), newID(), newID()
	ids := func(xs ...*age.X25519Identity) []*age.X25519Identity { return xs }
	same := func(got, want []*age.X25519Identity) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	gotHost, gotRegistry, err := unsealIdentities(ids(runtime), ids(build), ids(fleet, host), false, false)
	if err != nil || !same(gotHost, ids(runtime, fleet, host)) || !same(gotRegistry, ids(build, fleet, host)) {
		t.Fatalf("uncontracted = %v, %v, %v; want each domain then fleet and host", gotHost, gotRegistry, err)
	}
	if gotHost, gotRegistry, err := unsealIdentities(nil, nil, ids(fleet, host), false, false); err != nil || !same(gotHost, ids(fleet, host)) || !same(gotRegistry, ids(fleet, host)) {
		t.Fatalf("no domain staged = %v, %v, %v; want the legacy identities", gotHost, gotRegistry, err)
	}
	gotHost, gotRegistry, err = unsealIdentities(ids(runtime), ids(build), nil, true, false)
	if err != nil || !same(gotHost, ids(runtime)) || !same(gotRegistry, ids(build)) {
		t.Fatalf("contracted = %v, %v, %v; want each domain alone", gotHost, gotRegistry, err)
	}
	for name, staged := range map[string][2][]*age.X25519Identity{
		"no runtime domain": {nil, ids(build)},
		"no build domain":   {ids(runtime), nil},
	} {
		if _, _, err := unsealIdentities(staged[0], staged[1], nil, true, false); err == nil {
			t.Errorf("contracted with %s started", name)
		}
	}
	// ADR-20261110210000 slice 3e: a retired node needs only the build domain.
	gotHost, gotRegistry, err = unsealIdentities(nil, ids(build), nil, true, true)
	if err != nil || len(gotHost) != 0 || !same(gotRegistry, ids(build)) {
		t.Fatalf("retired = %v, %v, %v; want no runtime identities and the build domain", gotHost, gotRegistry, err)
	}
	if _, _, err := unsealIdentities(nil, nil, nil, true, true); err == nil {
		t.Error("retired without the build domain started")
	}
}

// ADR-20261110210000 slice 3e: a retired node holds no private key that
// opens runtime secrets, opens them through secretd, and seals capture keys
// to the runtime recipient.
func TestRuntimeRetiredRecipient(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	newDir := func(t *testing.T, files ...string) string {
		t.Helper()
		dir := t.TempDir()
		domains := filepath.Join(dir, "domains")
		if err := os.MkdirAll(domains, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			body := id.String()
			if strings.HasSuffix(f, ".pub") {
				body = id.Recipient().String() + "\n"
			}
			if err := os.WriteFile(filepath.Join(domains, f), []byte(body), 0o400); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	for _, tc := range []struct {
		name       string
		files      []string
		contracted bool
		opener     string
		want       string
	}{
		{name: "retired", files: []string{"runtime.age.pub"}, contracted: true, opener: "secretd"},
		{name: "not contracted", files: []string{"runtime.age.pub"}, opener: "secretd", want: "CONTRACTED"},
		{name: "opens locally", files: []string{"runtime.age.pub"}, contracted: true, opener: "local", want: "OPENER=secretd"},
		{name: "identity left", files: []string{"runtime.age.pub", "runtime.age"}, contracted: true, opener: "secretd", want: "still on this host"},
		{name: "previous left", files: []string{"runtime.age.pub", "runtime.age.previous"}, contracted: true, opener: "secretd", want: "still on this host"},
		{name: "no recipient", contracted: true, opener: "secretd", want: "needs the runtime recipient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := runtimeRetiredRecipient(newDir(t, tc.files...), tc.contracted, tc.opener)
			if tc.want == "" {
				if err != nil || r.String() != id.Recipient().String() {
					t.Fatalf("recipient = %v, %v", r, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestRun_ContractedVMMDSkipsLegacyIdentities: with
// FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED vmmd never loads the fleet and host
// identities, and without the runtime and build domains it refuses to start.
func TestRun_ContractedVMMDSkipsLegacyIdentities(t *testing.T) {
	dir := shortDir(t)
	cfgPath := filepath.Join(dir, "vmmd.toml")
	if err := os.WriteFile(cfgPath, []byte("resource_journal_dir = \""+filepath.Join(dir, "resources")+"\"\n"+"socket_path = \""+filepath.Join(dir, "x.sock")+"\"\nowner_user = \"root\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_HOST_KEY_PATH", filepath.Join(dir, "secrets", "host.age"))
	t.Setenv("FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED", "true")
	load, write := nopHostKeyDeps(t)
	deps := runDeps{
		configPath:  cfgPath,
		detectFC:    func(context.Context) (string, error) { return "1.7.0", nil },
		loadHostKey: load,
		loadHostKeys: func(string) ([]*age.X25519Identity, error) {
			t.Fatal("a contracted vmmd loaded the fleet and host identities")
			return nil, nil
		},
		writeRecipient: write,
		capCheck:       nopCapCheck(),
	}
	err := runWithDeps(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	if err == nil || !strings.Contains(err.Error(), "FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED needs domains/runtime.age and domains/build.age") {
		t.Fatalf("contracted start without the domains = %v; want it refused", err)
	}
}

func TestLoadConfig_RuntimeSealDomainContractedSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	for env, want := range map[string]bool{"": false, "false": false, "true": true} {
		t.Run("env="+env, func(t *testing.T) {
			if env != "" {
				t.Setenv("FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED", env)
			}
			cfg, err := LoadConfig(path)
			if err != nil || cfg.RuntimeSealDomainContracted != want {
				t.Fatalf("RuntimeSealDomainContracted = %v, %v; want %v", cfg != nil && cfg.RuntimeSealDomainContracted, err, want)
			}
		})
	}
	t.Setenv("FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED", "domain")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED") {
		t.Fatalf("invalid switch = %v; want a named validation error", err)
	}
}
