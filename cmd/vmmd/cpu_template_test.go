package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func hostCPU(t *testing.T, family, model, stepping int) fcvm.HostCPU {
	t.Helper()
	h, err := fcvm.ParseHostCPU([]byte("vendor_id\t: GenuineIntel\ncpu family\t: " + strconv.Itoa(family) +
		"\nmodel\t\t: " + strconv.Itoa(model) + "\nstepping\t: " + strconv.Itoa(stepping) + "\nflags\t\t: fpu sse avx512f\n"))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// adr: 20261024225612 — cpu_template defaults to none, TOML sets it, the
// environment overrides it in either direction, and an unknown name fails
// the config load.
func TestLoadConfig_CPUTemplate(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CPUTemplate != "" {
		t.Fatalf("cpu_template must default to none, got %q", cfg.CPUTemplate)
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("cpu_template = \"T2CL\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		set  bool
		env  string
		want string
	}{{"toml", false, "", "T2CL"}, {"env overrides", true, "T2S", "T2S"}, {"env clears", true, "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("FAAS_CPU_TEMPLATE", tc.env)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.CPUTemplate != tc.want {
				t.Fatalf("CPUTemplate = %q, want %q", cfg.CPUTemplate, tc.want)
			}
		})
	}
	t.Setenv("FAAS_CPU_TEMPLATE", "t2cl")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "cpu_template") {
		t.Fatalf("unknown template error = %v, want a cpu_template validation error", err)
	}
}

// adr: 20261024225612 — vmmd refuses a template its host CPU is not
// documented as safe on, and registers the host signature either way.
func TestResolveCPUIdentity(t *testing.T) {
	cascade := hostCPU(t, 6, 85, 7)
	rocket := hostCPU(t, 6, 167, 1)
	read := func(h fcvm.HostCPU) func() (fcvm.HostCPU, error) {
		return func() (fcvm.HostCPU, error) { return h, nil }
	}
	failed := func() (fcvm.HostCPU, error) { return fcvm.HostCPU{}, errors.New("no /proc/cpuinfo") }

	cfg := &Config{CPUTemplate: "T2CL"}
	if err := resolveCPUIdentity(cfg, read(cascade), testLogger()); err != nil {
		t.Fatalf("T2CL on Cascade Lake: %v", err)
	}
	if cfg.ComputeNode.CPUTemplate != "T2CL" || cfg.ComputeNode.CPUModel != cascade.Signature() {
		t.Fatalf("registered identity = %q / %q", cfg.ComputeNode.CPUTemplate, cfg.ComputeNode.CPUModel)
	}

	cfg = &Config{CPUTemplate: "T2CL"}
	if err := resolveCPUIdentity(cfg, read(rocket), testLogger()); err == nil || !strings.Contains(err.Error(), "not safe") {
		t.Fatalf("T2CL on Rocket Lake error = %v, want a refusal", err)
	}

	cfg = &Config{CPUTemplate: "T2CL"}
	if err := resolveCPUIdentity(cfg, failed, testLogger()); err == nil {
		t.Fatal("a template with an unreadable host cpu must stop vmmd")
	}

	cfg = &Config{}
	cfg.ComputeNode.CPUModel = "stale"
	if err := resolveCPUIdentity(cfg, failed, testLogger()); err != nil {
		t.Fatalf("no template, unreadable cpu: %v", err)
	}
	if cfg.ComputeNode.CPUModel != "" || cfg.ComputeNode.CPUTemplate != "" {
		t.Fatalf("unreadable cpu must register no identity, got %q / %q", cfg.ComputeNode.CPUTemplate, cfg.ComputeNode.CPUModel)
	}

	cfg = &Config{}
	if err := resolveCPUIdentity(cfg, read(rocket), testLogger()); err != nil {
		t.Fatalf("no template on Rocket Lake: %v", err)
	}
	if cfg.ComputeNode.CPUModel != rocket.Signature() {
		t.Fatalf("CPUModel = %q, want the host signature", cfg.ComputeNode.CPUModel)
	}
}

// adr: 20261024225612 — registration writes the node's CPU identity, and a
// re-registration replaces it (vmmd owns it).
func TestRegisterComputeNode_CPUIdentity(t *testing.T) {
	st := state.NewMemStore()
	cfg := ComputeNodeConfig{NodeName: "box-cpu", VPCPUs: 16, MemMB: 64000, MaxConcurrency: 200, AdmissionCeilingMB: 47600,
		CPUTemplate: "T2CL", CPUModel: "cascadelake/GenuineIntel-6-85-7/aaaaaaaaaaaa"}
	got, err := registerComputeNode(context.Background(), st, cfg, "unix:///run/faas/vmmd.sock",
		func(context.Context) (string, error) { return "", nil }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUTemplate != "T2CL" || got.CPUModel != cfg.CPUModel {
		t.Fatalf("registered %q / %q", got.CPUTemplate, got.CPUModel)
	}
	cfg.CPUTemplate, cfg.CPUModel = "", "icelake/GenuineIntel-6-106-6/bbbbbbbbbbbb"
	got, err = registerComputeNode(context.Background(), st, cfg, "unix:///run/faas/vmmd.sock",
		func(context.Context) (string, error) { return "", nil }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUTemplate != "" || got.CPUModel != cfg.CPUModel {
		t.Fatalf("re-registration kept %q / %q", got.CPUTemplate, got.CPUModel)
	}
}
