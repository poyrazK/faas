package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBackendFromEnv_LocalDefaults exercises the local backend fork
// with production default roots — the FAAS_APPS_ROOT default
// (/var/lib/faas/apps) differs from FAAS_STORAGE_ROOT (/srv/fc), so
// the helper produces a PrefixRouter.
func TestBackendFromEnv_LocalDefaults(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("FAAS_STORAGE_BACKEND", "")
	t.Setenv("FAAS_STORAGE_ROOT", filepath.Join(tmp, "fc"))
	t.Setenv("FAAS_APPS_ROOT", filepath.Join(tmp, "apps"))
	be, err := BackendFromEnv()
	if err != nil {
		t.Fatalf("BackendFromEnv: %v", err)
	}
	if _, ok := be.(*PrefixRouter); !ok {
		t.Errorf("backend type = %T, want *PrefixRouter (default split)", be)
	}
}

// TestBackendFromEnv_LocalSplit exercises the local fork with
// FAAS_APPS_ROOT pointing at a separate dir (production deploys the
// two as siblings).
func TestBackendFromEnv_LocalSplit(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_STORAGE_ROOT", filepath.Join(tmp, "fc"))
	t.Setenv("FAAS_APPS_ROOT", filepath.Join(tmp, "apps"))
	be, err := BackendFromEnv()
	if err != nil {
		t.Fatalf("BackendFromEnv: %v", err)
	}
	if _, ok := be.(*PrefixRouter); !ok {
		t.Errorf("backend type = %T, want *PrefixRouter (split layout)", be)
	}
}

// TestBackendFromEnv_LocalCoalesced verifies the router collapses to a
// single LocalStorageBackend when the two roots coincide.
func TestBackendFromEnv_LocalCoalesced(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_STORAGE_ROOT", tmp)
	t.Setenv("FAAS_APPS_ROOT", tmp)
	be, err := BackendFromEnv()
	if err != nil {
		t.Fatalf("BackendFromEnv: %v", err)
	}
	if _, ok := be.(*LocalStorageBackend); !ok {
		t.Errorf("backend type = %T, want *LocalStorageBackend (coalesced)", be)
	}
}

// TestBackendFromEnv_OCIRequiresRegistry verifies the OCI fork
// refuses to default without FAAS_OCI_REGISTRY.
func TestBackendFromEnv_OCIRequiresRegistry(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")
	os.Unsetenv("FAAS_OCI_REGISTRY") // ensure unset
	_, err := BackendFromEnv()
	if err == nil {
		t.Fatal("expected error for oci backend without registry")
	}
}

// TestBackendFromEnv_OCIRejectsUnknown verifies unknown backend kinds
// are rejected at startup.
func TestBackendFromEnv_OCIRejectsUnknown(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "s3")
	_, err := BackendFromEnv()
	if err == nil {
		t.Fatal("expected error for unknown backend kind")
	}
	if got := err.Error(); !strings.Contains(got, "unknown") {
		t.Errorf("error %q lacks 'unknown'", got)
	}
}
