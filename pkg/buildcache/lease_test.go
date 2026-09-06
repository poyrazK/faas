package buildcache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLeasePathRoundTripAndRelease(t *testing.T) {
	path, err := LeasePath(t.TempDir(), "deployment-123")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := ParseLeasePath(path); !ok || got != "deployment-123" {
		t.Fatalf("ParseLeasePath(%q) = %q, %v", path, got, ok)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("artifact"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Release(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lease still exists: %v", err)
	}
	if err := Release(path); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
}

func TestLeasePathRejectsUnsafeDeploymentID(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../escape", "a/b", `a\\b`} {
		if _, err := LeasePath(t.TempDir(), id); err == nil {
			t.Fatalf("LeasePath accepted %q", id)
		}
	}
	if err := Release(filepath.Join(t.TempDir(), "ordinary.oci.tar")); err != nil {
		t.Fatalf("ordinary path should be ignored: %v", err)
	}
}
