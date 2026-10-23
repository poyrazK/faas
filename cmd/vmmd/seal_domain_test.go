package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

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

func TestRegisterSealDomainLoaded(t *testing.T) {
	reg := prometheus.NewRegistry()
	registerSealDomainLoaded(reg, sealdomain.Runtime, true)
	want := `
# HELP faas_seal_domain_loaded 1 when this daemon loaded the seal domain's identity at start (ADR 20261022111812).
# TYPE faas_seal_domain_loaded gauge
faas_seal_domain_loaded{domain="runtime"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "faas_seal_domain_loaded"); err != nil {
		t.Fatal(err)
	}
}
