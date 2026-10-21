// spec: §6.3 — the console file is created off the wake's critical path.

package fcvm

import (
	"os"
	"testing"
)

func newConsoleTestVMM(t *testing.T) *JailerVMM {
	t.Helper()
	return &JailerVMM{consoleLogDir: t.TempDir()}
}

func writeConsole(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func readConsole(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConsoleFileTruncatesWhenTaken(t *testing.T) {
	cases := []struct {
		name    string
		preopen bool
	}{
		{name: "preopened", preopen: true},
		{name: "opened at startJailer", preopen: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newConsoleTestVMM(t)
			path := v.consoleLogPath("i1")
			writeConsole(t, path, "previous attempt\n")
			discard := func() {}
			if tc.preopen {
				discard = v.preopenConsoleFile("i1")
			}
			f := v.takeConsoleFile("i1")
			discard() // startJailer already took it: a no-op.
			if f == nil {
				t.Fatal("takeConsoleFile returned no file")
			}
			if _, err := f.WriteString("boot\n"); err != nil {
				t.Fatalf("console file is not writable: %v", err)
			}
			_ = f.Close()
			if got := readConsole(t, path); got != "boot\n" {
				t.Fatalf("console = %q, want the earlier content truncated", got)
			}
		})
	}
}

// A restore that fails before startJailer must not leak the descriptor or
// hand a stale open to the instance's next attempt (the ADR-005 cold boot).
func TestConsoleFileDiscardBeforeStartJailer(t *testing.T) {
	v := newConsoleTestVMM(t)
	path := v.consoleLogPath("i1")
	discard := v.preopenConsoleFile("i1")
	discard()
	v.mu.Lock()
	pending := len(v.consoleOpens)
	v.mu.Unlock()
	if pending != 0 {
		t.Fatalf("discard left %d pending console opens", pending)
	}
	writeConsole(t, path, "restore attempt\n")
	f := v.takeConsoleFile("i1")
	if f == nil {
		t.Fatal("takeConsoleFile returned no file")
	}
	_ = f.Close()
	if got := readConsole(t, path); got != "" {
		t.Fatalf("console = %q, want a fresh truncating open", got)
	}
}

// Without the log directory the VM still starts, without the file copy.
func TestConsoleFileMissingDirectory(t *testing.T) {
	v := &JailerVMM{consoleLogDir: t.TempDir() + "/absent"}
	defer v.preopenConsoleFile("i1")()
	if f := v.takeConsoleFile("i1"); f != nil {
		_ = f.Close()
		t.Fatal("takeConsoleFile opened a file in a missing directory")
	}
}
