package main

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/sealsig"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261028161502 — vmmd checks runtime secret signatures only with a
// verify key, and requires them only on the contract step.
func TestLoadConfig_RuntimeSecretSignatureSwitches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RuntimeSecretSignatureKeysFile != "" || cfg.RequireRuntimeSecretSignatures {
		t.Fatalf("defaults = %q, %v; want off", cfg.RuntimeSecretSignatureKeysFile, cfg.RequireRuntimeSecretSignatures)
	}
	t.Setenv("FAAS_RUNTIME_SECRET_SIGNATURE_REQUIRED", "true")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "runtime_secret_signature_keys_file") {
		t.Fatalf("required without keys = %v, want a named error", err)
	}
	t.Setenv("FAAS_RUNTIME_SECRET_SIGNATURE_KEYS_FILE", "/etc/faas/secrets/domains/runtime-signing.pub")
	if cfg, err = LoadConfig(path); err != nil || !cfg.RequireRuntimeSecretSignatures || cfg.RuntimeSecretSignatureKeysFile == "" {
		t.Fatalf("LoadConfig = %+v, %v", cfg, err)
	}
	t.Setenv("FAAS_RUNTIME_SECRET_SIGNATURE_REQUIRED", "yes please")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "FAAS_RUNTIME_SECRET_SIGNATURE_REQUIRED") {
		t.Fatalf("invalid switch = %v, want a named error", err)
	}
}

func TestRuntimeSecretSignatureChecker(t *testing.T) {
	dir := t.TempDir()
	pub := filepath.Join(dir, "runtime-signing.pub")
	if err := sealsig.WriteSigningKeyPair(filepath.Join(dir, "runtime-signing.key"), pub); err != nil {
		t.Fatal(err)
	}
	enforcement := func(reg *prometheus.Registry) float64 {
		t.Helper()
		families, err := reg.Gather()
		if err != nil || len(families) != 1 {
			t.Fatalf("gather = %v, %v", families, err)
		}
		return families[0].GetMetric()[0].GetGauge().GetValue()
	}

	reg := prometheus.NewRegistry()
	checker, err := runtimeSecretSignatureChecker(&Config{}, nil, reg, slog.Default())
	if err != nil || checker != nil || enforcement(reg) != 0 {
		t.Fatalf("no verify key = %v, %v", checker, err)
	}
	if _, err := runtimeSecretSignatureChecker(&Config{RuntimeSecretSignatureKeysFile: pub}, nil, prometheus.NewRegistry(), slog.Default()); err == nil {
		t.Fatal("a verify key with no database must refuse to start")
	}
	reg = prometheus.NewRegistry()
	checker, err = runtimeSecretSignatureChecker(&Config{RuntimeSecretSignatureKeysFile: pub, RequireRuntimeSecretSignatures: true}, state.NewMemStore(), reg, slog.Default())
	if err != nil || checker == nil || enforcement(reg) != 2 {
		t.Fatalf("required = %v, %v, enforcement %v", checker, err, testutil.CollectAndCount(reg))
	}
}
