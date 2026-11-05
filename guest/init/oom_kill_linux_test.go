//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadVMStatOOMKills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmstat")
	if err := os.WriteFile(path, []byte("pgfault 12\noom_kill 3\nnr_free_pages 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, ok := readVMStatOOMKills(path); !ok || n != 3 {
		t.Fatalf("oom_kill = %d, %v", n, ok)
	}
	if err := os.WriteFile(path, []byte("pgfault 12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readVMStatOOMKills(path); ok {
		t.Fatal("missing counter reported")
	}
	if _, ok := readVMStatOOMKills(filepath.Join(t.TempDir(), "missing")); ok {
		t.Fatal("missing file reported")
	}
}

func TestOOMKilled(t *testing.T) {
	killed := exec.Command("sleep", "30")
	if err := killed.Start(); err != nil {
		t.Skipf("sleep unavailable: %v", err)
	}
	_ = killed.Process.Signal(syscall.SIGKILL)
	killErr := killed.Wait()
	exited := exec.Command("sh", "-c", "exit 3")
	exitErr := exited.Run()
	counter := func(n uint64) func() (uint64, bool) { return func() (uint64, bool) { return n, true } }
	for _, tc := range []struct {
		name     string
		err      error
		known    bool
		after    func() (uint64, bool)
		oomKills bool
	}{
		{"SIGKILL with a new OOM kill", killErr, true, counter(5), true},
		{"SIGKILL without an OOM kill", killErr, true, counter(4), false},
		{"non-zero exit", exitErr, true, counter(5), false},
		{"counter unknown at start", killErr, false, counter(5), false},
		{"counter unreadable after", killErr, true, func() (uint64, bool) { return 0, false }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := oomKilled(tc.err, 4, tc.known, tc.after); got != tc.oomKills {
				t.Fatalf("oomKilled = %v, want %v", got, tc.oomKills)
			}
		})
	}
}
