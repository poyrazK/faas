//go:build unix

package wire

// adr: 1580 — handoff fixtures control host observations independently.

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// HandoffOptionsWithMemoryCheckForTest injects an observation only into test
// binaries. Production's zero-value options keep the real cgroup check.
func HandoffOptionsWithMemoryCheckForTest(check func() error) HandoffOptions {
	return HandoffOptions{memoryAllowed: check}
}

func TestHandoffMemoryRefusalPrecedesSocketClaim(t *testing.T) {
	refused := errors.New("daemon cgroup has no second-generation headroom")
	checks := 0
	// A refusal that outlasts the spike wait (shortened here) still precedes
	// every socket and process effect.
	pid, err := Handoff(t.Context(), HandoffOptions{
		Path:          filepath.Join(t.TempDir(), "missing-successor"),
		underSystemd:  func() bool { return false },
		memoryAllowed: func() error { checks++; return refused },
		freeMemory:    func() {},
		memoryWait:    time.Nanosecond,
		memoryPoll:    time.Nanosecond,
	})
	if pid != 0 || !errors.Is(err, refused) || checks == 0 {
		t.Fatalf("memory refusal did not precede socket/process effects: pid=%d checks=%d err=%v", pid, checks, err)
	}
}
