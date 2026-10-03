package e2etest

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarnessSocketDirWithLongTMPDIR(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("long-", 30))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)
	first, second := newHarnessSocketDir(t), newHarnessSocketDir(t)
	if first == second {
		t.Fatal("harnesses share a socket directory")
	}
	for _, dir := range []string{first, second} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("socket directory must be private: %v, %v", info, err)
		}
		listener, err := net.Listen("unix", filepath.Join(dir, "request_telemetry.sock"))
		if err != nil {
			t.Fatalf("bind harness socket with long TMPDIR: %v", err)
		}
		t.Cleanup(func() { _ = listener.Close() })
	}
}
