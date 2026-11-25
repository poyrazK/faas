package main_test

import (
	"net/http"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r56 (rc.381): with interactive sessions off, `app cp` answered
// with the generic refusal and its database-schema advice. It now names a
// plain `app exec -- cat`, which still runs.
func TestAppCopyNamesExecFallbackWhenSessionsAreOff(t *testing.T) {
	linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost {
			api.WriteProblem(w, api.ErrInteractiveSessionsNotEnabled())
			return true
		}
		return false
	})
	code, _, stderr := runCLI(t, "app", "web", "cp", "/app/handler.py", "out.py")
	if code == 0 {
		t.Fatal("cp succeeded against a refusing server")
	}
	if want := "gregale app exec web -- cat /app/handler.py > out.py"; !strings.Contains(stderr, want) {
		t.Fatalf("stderr lacks %q:\n%s", want, stderr)
	}
}

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
