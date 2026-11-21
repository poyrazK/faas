// spec: §6.3 — a failed cold boot's serial console survives cleanup (bounded).

package fcvm

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 2026-11-21: concurrent Bank of Anthos cold boots never became ready and
// cleanup deleted their consoles, leaving nothing to say where 30 s went. A
// cold boot that failed readiness keeps its console; the node keeps a
// bounded number of them; an ordinary VM's console is still removed.
func TestRemoveConsoleFileKeepsBootFailedConsole(t *testing.T) {
	v := newConsoleTestVMM(t)
	ordinary, failed := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	writeConsole(t, v.consoleLogPath(ordinary), "ok\n")
	writeConsole(t, v.consoleLogPath(failed), "gunicorn booting\n")
	v.bootFailedConsoles.Store(failed, struct{}{})

	v.removeConsoleFile(ordinary)
	v.removeConsoleFile(failed)

	if _, err := os.Stat(v.consoleLogPath(ordinary)); !os.IsNotExist(err) {
		t.Fatalf("ordinary console kept: %v", err)
	}
	kept := filepath.Join(v.consoleLogDir, "vm-"+failed+bootFailedConsoleSuffix)
	if body, err := os.ReadFile(kept); err != nil || string(body) != "gunicorn booting\n" {
		t.Fatalf("failed boot console = %q, %v", body, err)
	}
	if _, ok := v.bootFailedConsoles.Load(failed); ok {
		t.Fatal("flag survived the kill")
	}
}

func TestKeepBootFailedConsolePrunesOldest(t *testing.T) {
	v := newConsoleTestVMM(t)
	old := time.Now().Add(-time.Hour)
	for i := 0; i < bootFailedConsolesKept; i++ {
		path := filepath.Join(v.consoleLogDir, fmt.Sprintf("vm-old-%02d%s", i, bootFailedConsoleSuffix))
		writeConsole(t, path, "old\n")
		stamp := old.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	instance := "33333333-3333-3333-3333-333333333333"
	writeConsole(t, v.consoleLogPath(instance), "new\n")
	if _, err := v.keepBootFailedConsole(instance); err != nil {
		t.Fatal(err)
	}
	kept, err := filepath.Glob(filepath.Join(v.consoleLogDir, "vm-*"+bootFailedConsoleSuffix))
	if err != nil || len(kept) != bootFailedConsolesKept {
		t.Fatalf("kept = %d (%v), want %d", len(kept), err, bootFailedConsolesKept)
	}
	if _, err := os.Stat(filepath.Join(v.consoleLogDir, "vm-old-00"+bootFailedConsoleSuffix)); !os.IsNotExist(err) {
		t.Fatalf("oldest console not pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.consoleLogDir, "vm-"+instance+bootFailedConsoleSuffix)); err != nil {
		t.Fatalf("newest console pruned: %v", err)
	}
}
