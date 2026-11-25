package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r56 (rc.381): after `job update --command` fixed a failing task,
// `job replay` re-ran the source run's captured command and failed again
// without saying so. Replay preview and submission now name both commands.
func TestJobReplayCommandNote(t *testing.T) {
	oldCmd := []string{"/bin/sh", "-c", "exit 3"}
	newCmd := []string{"/bin/sh", "-c", "echo fixed"}
	if note := main.JobReplayCommandNote("nightly", newCmd, oldCmd); !strings.Contains(note, "exit 3") || !strings.Contains(note, "echo fixed") || !strings.Contains(note, "gregale job run nightly") {
		t.Fatalf("note = %q", note)
	}
	for _, tc := range []struct{ current, replayed []string }{{oldCmd, oldCmd}, {newCmd, nil}} {
		if note := main.JobReplayCommandNote("nightly", tc.current, tc.replayed); note != "" {
			t.Errorf("JobReplayCommandNote(%q, %q) = %q, want none", tc.current, tc.replayed, note)
		}
	}
}
