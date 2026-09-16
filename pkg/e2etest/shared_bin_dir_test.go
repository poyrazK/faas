package e2etest

// Tests for FAAS_E2E_BIN_DIR — sharing compiled daemons across phases.
//
// The native gate now runs the metal suite as several phases, each its own
// `go test` process. EnsureSharedBinaries builds once per PROCESS, and the Go
// build cache covers compiled packages but never the final link, so without a
// shared directory every phase would re-link all eight binaries. The gate
// builds them once and points each phase at the result.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBinaryPresent_RejectsUnusableFiles(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good")
	if err := os.WriteFile(good, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	notExec := filepath.Join(dir, "notexec")
	if err := os.WriteFile(notExec, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want bool
		why  string
	}{
		{name: "executable", path: good, want: true},
		{
			name: "zero length", path: empty, want: false,
			why: "a half-written binary from an interrupted build must be rebuilt, not exec'd",
		},
		{
			name: "not executable", path: notExec, want: false,
			why: "exec would fail with a far less obvious error than a rebuild",
		},
		{name: "absent", path: filepath.Join(dir, "nope"), want: false},
		{name: "directory", path: dir, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := binaryPresent(tc.path); got != tc.want {
				t.Errorf("binaryPresent(%s) = %v, want %v; %s", tc.name, got, tc.want, tc.why)
			}
		})
	}
}

// A caller-owned directory is shared with later phases, so the process that
// happens to finish first must not delete it.
func TestRemoveSharedBinaries_KeepsACallerOwnedDir(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "apid")
	if err := os.WriteFile(marker, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_E2E_BIN_DIR", dir)

	saved := sharedBinDir
	sharedBinDir = dir
	t.Cleanup(func() { sharedBinDir = saved })

	RemoveSharedBinaries()

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("RemoveSharedBinaries deleted a caller-owned FAAS_E2E_BIN_DIR: %v; "+
			"later phases would rebuild every daemon", err)
	}
}

// Without the override the directory is this process's own temp dir and must
// still be cleaned up, or every e2e run leaks ~400 MB of binaries.
func TestRemoveSharedBinaries_RemovesAnOwnedTempDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "faas-e2e-bin-test-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apid"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_E2E_BIN_DIR", "")

	saved := sharedBinDir
	sharedBinDir = dir
	t.Cleanup(func() {
		sharedBinDir = saved
		_ = os.RemoveAll(dir)
	})

	RemoveSharedBinaries()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("RemoveSharedBinaries left an owned temp dir behind: %v", err)
	}
}
