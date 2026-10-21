package fcvm

import (
	"os"
	"path/filepath"
	"testing"
)

// A failed restore's console holds guest-init's resume-hook diagnosis; the
// fallback cold boot of the same instance would truncate it.
func TestPreserveFailedRestoreConsole(t *testing.T) {
	dir := t.TempDir()
	prev := consoleLogDir
	consoleLogDir = dir
	t.Cleanup(func() { consoleLogDir = prev })
	src := instanceConsolePath("vm-a")
	if err := os.WriteFile(src, []byte("restore reseed: 1 of 1 processes did not reseed\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	kept, err := (&JailerVMM{}).PreserveFailedRestoreConsole("vm-a")
	if err != nil {
		t.Fatalf("PreserveFailedRestoreConsole: %v", err)
	}
	if want := filepath.Join(dir, "vm-vm-a.restore-failed.console"); kept != want {
		t.Fatalf("kept = %q, want %q", kept, want)
	}
	if body, err := os.ReadFile(kept); err != nil || string(body) != "restore reseed: 1 of 1 processes did not reseed\n" {
		t.Fatalf("kept console = %q, %v", body, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("live console path still present: %v", err)
	}
	if _, err := (&JailerVMM{}).PreserveFailedRestoreConsole("missing"); err == nil {
		t.Fatal("kept a console that does not exist")
	}
}
