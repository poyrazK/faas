package e2etest

// Tests for forwardedStorageEnv — the seam that hands harness-booted daemons
// the host's artifact store instead of pkg/storage's local default.
//
// Regression pinned here: on an OCI-backed node, harness daemons that kept
// the local default read an empty /srv/fc/scans while imaged had staged every
// runtime base and its Grype scan sidecar into the registry. vmmd's issue
// #299 admission gate then refused every cold boot with "scan sidecar
// missing", which reads like absent security evidence but is a
// wrong-store lookup. Measured on compute node 2, 2026-09-14: the builder
// base's sidecar was present and CRITICAL-clean in the OCI store throughout.

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

func parseEnvPairs(t *testing.T, pairs []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("forwardedStorageEnv returned %q, which is not NAME=VALUE", kv)
		}
		out[name] = value
	}
	return out
}

// A CI runner exports none of these, and that path must keep behaving exactly
// as it did before: local backend, no forwarding.
func TestForwardedStorageEnv_EmptyWhenHostSetsNothing(t *testing.T) {
	for _, name := range daemonunitspec.ArtifactStorageEnvNames() {
		t.Setenv(name, "")
	}
	if got := forwardedStorageEnv(); len(got) != 0 {
		t.Errorf("forwardedStorageEnv() = %v, want empty when no storage variable is set", got)
	}
}

func TestForwardedStorageEnv_ForwardsSetValues(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")
	t.Setenv("FAAS_OCI_REGISTRY", "https://ghcr.io")
	t.Setenv("FAAS_OCI_REPO_PREFIX", "example/faas-e2e")

	got := parseEnvPairs(t, forwardedStorageEnv())

	for name, want := range map[string]string{
		"FAAS_STORAGE_BACKEND": "oci",
		"FAAS_OCI_REGISTRY":    "https://ghcr.io",
		"FAAS_OCI_REPO_PREFIX": "example/faas-e2e",
	} {
		if got[name] != want {
			t.Errorf("forwardedStorageEnv()[%s] = %q, want %q", name, got[name], want)
		}
	}
}

// An empty value must not be forwarded as an explicit empty assignment: that
// would override a daemon's own default with "", which is a different and
// worse failure than leaving the variable absent.
func TestForwardedStorageEnv_SkipsEmptyValues(t *testing.T) {
	for _, name := range daemonunitspec.ArtifactStorageEnvNames() {
		t.Setenv(name, "")
	}
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")

	got := parseEnvPairs(t, forwardedStorageEnv())

	if got["FAAS_STORAGE_BACKEND"] != "oci" {
		t.Errorf("forwardedStorageEnv() dropped the set backend: %v", got)
	}
	if _, present := got["FAAS_OCI_REGISTRY"]; present {
		t.Error("forwardedStorageEnv() forwarded FAAS_OCI_REGISTRY with an empty value; " +
			"an explicit empty assignment masks the daemon's own default")
	}
}

// testEnvCommon is what every harness-booted daemon actually receives, so the
// forwarding must be reachable from there — not merely correct in isolation.
func TestTestEnvCommon_CarriesHostStorageConfiguration(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")
	t.Setenv("FAAS_OCI_REPO_PREFIX", "example/faas-e2e")

	got := parseEnvPairs(t, testEnvCommon("postgres:///faas_e2e"))

	if got["FAAS_STORAGE_BACKEND"] != "oci" {
		t.Errorf("testEnvCommon did not forward the storage backend; daemons would "+
			"fall back to the local default at /srv/fc (got %q)", got["FAAS_STORAGE_BACKEND"])
	}
	if got["FAAS_OCI_REPO_PREFIX"] != "example/faas-e2e" {
		t.Errorf("testEnvCommon did not forward the OCI repo prefix (got %q)",
			got["FAAS_OCI_REPO_PREFIX"])
	}
}
