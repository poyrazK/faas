package e2etest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHarnessGuestInitRefusesPlaceholderWithKernel(t *testing.T) {
	dir := t.TempDir()
	if path, err := harnessGuestInit(dir, "", "/real/kernel"); err == nil || path != "" {
		t.Fatal("native harness accepted a placeholder PID 1")
	}
	if _, err := os.Stat(filepath.Join(dir, "init")); !os.IsNotExist(err) {
		t.Fatal("native prerequisite failure wrote a placeholder")
	}
	if path, err := harnessGuestInit(dir, "/real/guest-init", "/real/kernel"); err != nil || path != "/real/guest-init" {
		t.Fatal("explicit guest init was not retained")
	}
	if _, err := harnessGuestInit(dir, "", ""); err != nil {
		t.Fatal(err)
	}
	if ValidateNativeGuestInit(filepath.Join(dir, "init")) == nil {
		t.Fatal("native validation accepted the non-guest shell placeholder")
	}
}

func TestNativeGuestInitRejectsUnrelatedStaticExecutable(t *testing.T) {
	binary, err := PostgresProbeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(path, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	if ValidateNativeGuestInit(path) == nil {
		t.Fatal("unrelated static Go binary accepted as guest PID 1")
	}
}
