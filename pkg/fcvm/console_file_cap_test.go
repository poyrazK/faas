package fcvm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Every byte a tenant app writes lands in its console file on the node's
// root filesystem, which had no size cap and was never deleted (bug hunt
// 2026-10-28 B15).
func TestCappedConsoleFileStartsOverPastTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm-cap.console")
	f := openConsoleFile(path)
	if f == nil {
		t.Fatal("open console file")
	}
	defer func() { _ = f.Close() }()
	w := &cappedConsoleFile{f: f}
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for range consoleFileMaxBytes/len(chunk) + 3 {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > consoleFileMaxBytes {
		t.Fatalf("console file is %d bytes, want at most %d", info.Size(), consoleFileMaxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, consoleTruncatedMarker) {
		t.Fatalf("file does not start with the truncation marker: %q, %v", data[:min(len(data), 80)], err)
	}
}

func TestRemoveConsoleFileDeletesOnlyTheLiveConsole(t *testing.T) {
	v := &JailerVMM{consoleLogDir: t.TempDir()}
	live := v.consoleLogPath("inst-1")
	preserved := filepath.Join(v.consoleLogDir, "vm-inst-1.restore-failed.console")
	for _, p := range []string{live, preserved} {
		if err := os.WriteFile(p, []byte("boot"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	v.removeConsoleFile("inst-1")
	v.removeConsoleFile("inst-missing")
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("live console still present: %v", err)
	}
	if _, err := os.Stat(preserved); err != nil {
		t.Fatalf("preserved failed-restore console removed: %v", err)
	}
}
