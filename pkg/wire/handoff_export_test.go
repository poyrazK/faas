//go:build unix

package wire

// adr: 2026101801 — handoff fixtures control host observations independently.

import (
	"errors"
	"path/filepath"
	"testing"
)

// HandoffOptionsWithMemoryCheckForTest injects an observation only into test
// binaries. Production's zero-value options keep the real cgroup check.
func HandoffOptionsWithMemoryCheckForTest(check func() error) HandoffOptions {
	return HandoffOptions{memoryAllowed: check}
}

func TestHandoffMemoryRefusalPrecedesSocketClaim(t *testing.T) {
	refused := errors.New("daemon cgroup has no second-generation headroom")
	checks := 0
	pid, err := Handoff(t.Context(), HandoffOptions{
		Path:          filepath.Join(t.TempDir(), "missing-successor"),
		underSystemd:  func() bool { return false },
		memoryAllowed: func() error { checks++; return refused },
	})
	if pid != 0 || !errors.Is(err, refused) || checks != 1 {
		t.Fatalf("memory refusal did not precede socket/process effects: pid=%d checks=%d err=%v", pid, checks, err)
	}
}
