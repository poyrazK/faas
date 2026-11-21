package fcvm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A fresh host had no /var/log/faas: every console open failed silently and
// agent-run captures waited 60 s for a marker that could never appear
// (metal, 2026-11-21). The directory is created on demand.
func TestOpenConsoleFileCreatesItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "faas", "vm-x.console")
	f := openConsoleFile(path)
	if f == nil {
		t.Fatal("console file not opened under a missing directory")
	}
	_ = f.Close()
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != consoleLogDirMode {
		t.Fatalf("directory %v %v", info, err)
	}
}

// Without a console file the marker can never appear, so the wait says so at
// once instead of running out its window.
func TestWaitConsoleMarkerFailsFastWithoutAConsoleFile(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	v.recs["vm-1"] = &instanceRecord{consolePath: filepath.Join(t.TempDir(), "missing.console"), done: make(chan struct{})}
	started := time.Now()
	err := v.WaitAgentRunReady(context.Background(), "vm-1", time.Minute)
	if err == nil || !strings.Contains(err.Error(), "no console file") || time.Since(started) > 5*time.Second {
		t.Fatalf("err %v after %s", err, time.Since(started))
	}
}
