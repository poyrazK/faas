// adr: 20261029155852
package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/oci"
)

func TestNewBaseProvenanceVerifierModes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := func(values map[string]string) func(string) string {
		return func(k string) string { return values[k] }
	}
	puller := oci.NewRegistryClient()

	v, err := newBaseProvenanceVerifier(env(nil), puller, "/srv/fc", prometheus.NewRegistry(), log)
	if err != nil || v == nil || v.Mode != imaged.BaseProvenanceObserve || v.CosignBin != imaged.DefaultCosignBin || v.Bundles == nil ||
		v.CacheDir != filepath.Join("/srv/fc", "sigs", "base-provenance") || !strings.HasSuffix(v.TrustedRoot, imaged.BaseProvenanceTrustedRootName) {
		t.Fatalf("default verifier = %+v, %v", v, err)
	}
	v, err = newBaseProvenanceVerifier(env(map[string]string{imaged.BaseProvenanceModeEnv: "enforce", imaged.CosignBinEnv: "/opt/cosign"}), puller, "/srv/fc", prometheus.NewRegistry(), log)
	if err != nil || v.Mode != imaged.BaseProvenanceEnforce || v.CosignBin != "/opt/cosign" {
		t.Fatalf("enforce verifier = %+v, %v", v, err)
	}
	if v, err := newBaseProvenanceVerifier(env(map[string]string{imaged.BaseProvenanceModeEnv: "off"}), puller, "/srv/fc", prometheus.NewRegistry(), log); v != nil || err != nil {
		t.Fatalf("off = %+v, %v; want no verifier", v, err)
	}
	if _, err := newBaseProvenanceVerifier(env(map[string]string{imaged.BaseProvenanceModeEnv: "warn"}), puller, "/srv/fc", prometheus.NewRegistry(), log); err == nil {
		t.Fatal("an unknown mode did not stop imaged")
	}
}

func TestBaseProvenanceCommandArguments(t *testing.T) {
	if _, ok := runBaseProvenanceCommand([]string{"--config", "/etc/faas/imaged.toml"}, io.Discard, io.Discard); ok {
		t.Fatal("daemon flags were taken for the subcommand")
	}
	if _, ok := runBaseProvenanceCommand(nil, io.Discard, io.Discard); ok {
		t.Fatal("no arguments were taken for the subcommand")
	}
	var stderr bytes.Buffer
	if code, ok := runBaseProvenanceCommand([]string{"verify-base-provenance"}, io.Discard, &stderr); !ok || code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("no --ref = %d %v %q", code, ok, stderr.String())
	}
	bad := filepath.Join(t.TempDir(), "legacy.txt")
	if err := os.WriteFile(bad, []byte("not a digest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, ok := runBaseProvenanceCommand([]string{"verify-base-provenance", "--legacy", bad, "--ref", "ghcr.io/poyrazk/runner-node22@sha256:" + strings.Repeat("a", 64)}, io.Discard, io.Discard); !ok || code != 2 {
		t.Fatalf("malformed legacy list = %d %v", code, ok)
	}
}
