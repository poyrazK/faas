package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

func TestConfiguredReadinessURLUsesMetricsAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imaged.toml")
	if err := os.WriteFile(path, []byte("metrics_addr = \"fsn-2.gregale.dev:9102\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := serviceConfigPaths
	t.Cleanup(func() { serviceConfigPaths = original })
	serviceConfigPaths = map[string]string{"imaged": path}

	got, configured, err := configuredReadinessURLForService("imaged")
	if err != nil {
		t.Fatal(err)
	}
	if !configured || got != "http://fsn-2.gregale.dev:9102/readyz" {
		t.Fatalf("configured readiness = %q, %v; want private metrics URL", got, configured)
	}
}

func TestReadinessURLForMetricsTargetMapsWildcardToLoopback(t *testing.T) {
	got, err := readinessURLForMetricsTarget("0.0.0.0:9104")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:9104/readyz" {
		t.Fatalf("readiness URL = %q", got)
	}
}

func TestUpgradeReadyEntriesContainOnlyComputeDaemons(t *testing.T) {
	entries, err := upgradeReadyEntries()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name)
	}
	want := daemonunitspec.DaemonsForRole(daemonunitspec.RoleComputeOnly)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upgrade readiness entries = %v, want %v", got, want)
	}
	for _, forbidden := range []string{"apid", "gatewayd-public", "meterd", "githubd", "outboundd"} {
		if slices.Contains(got, forbidden) {
			t.Errorf("compute image gate includes control-plane daemon %q", forbidden)
		}
	}
}

func TestTargetReadinessProbeUsesTargetMetricsConfig(t *testing.T) {
	entry, ok := daemonEntry("vmmd")
	if !ok {
		t.Fatal("vmmd registry entry missing")
	}
	command, err := targetReadinessProbeCommand(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"/etc/faas/vmmd.toml", "tomllib", "metrics_addr", `http://${addr}/readyz`} {
		if !strings.Contains(command, required) {
			t.Errorf("target readiness command missing %q: %s", required, command)
		}
	}
	if strings.Contains(command, entry.Lifecycle.ReadyzURL) {
		t.Errorf("target readiness command still uses static registry URL: %s", command)
	}
}
