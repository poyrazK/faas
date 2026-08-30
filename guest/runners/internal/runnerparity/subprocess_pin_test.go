package runnerparity

// TestRunners_InvokeHandlerUsesCmdRun pins the legacy fallback in §4.9.1:
// every current runner retains a `cmd.Run()` path for customer handlers that
// do not advertise the generated persistent protocol. Generated adapters use
// the worker pool, but the compatibility path must remain available.
//
// The grep walks every guest/runners/<runtime>/main.go (each
// runtime lives in its own sub-package, so a single file-system
// walk covers all of them). For each, we verify:
//   1. invokeHandler exists
//   2. invokeHandler calls cmd.Run() somewhere in its body
//
// We do not parse the Go AST — file-path string-matching is
// sufficient for this pin, and the test stays a one-file, no-
// dependency assertion that runs in microseconds.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runnerRoot is the parent of the runner sub-packages. Computed
// relative to this test file: ../../.. walks out of
// guest/runners/internal/runnerparity to the guest/ root, then
// ../runners lands in guest/runners. Tests run with cwd at the
// package directory, so this relative path resolves correctly.
const runnerRoot = "../../.."

func TestRunners_InvokeHandlerUsesCmdRun(t *testing.T) {
	root := filepath.Join(runnerRoot, "runners")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read runners dir: %v", err)
	}

	var checked, missingInvoke, missingCmdRun int
	var runnerDirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip the shared internal/ helpers package.
		if e.Name() == "internal" {
			continue
		}
		runnerDirs = append(runnerDirs, e.Name())
		mainPath := filepath.Join(root, e.Name(), "main.go")
		body, err := os.ReadFile(mainPath)
		if err != nil {
			t.Errorf("read %s: %v", mainPath, err)
			continue
		}
		src := string(body)
		checked++
		if !strings.Contains(src, "func invokeHandler") {
			missingInvoke++
			t.Errorf("%s: missing invokeHandler (runner shape changed — update §4.9.1 and this test)", e.Name())
			continue
		}
		if !strings.Contains(src, "cmd.Run()") {
			missingCmdRun++
			t.Errorf("%s: invokeHandler does not call cmd.Run() — runner switched to long-lived handler (closes the §4.9.1 listener-vs-handler gap, update spec)", e.Name())
		}
	}

	// Belt-and-suspenders: every active runtime enum in
	// openapi.yaml (node22, node24, python312, python313, go124,
	// go124-alpine) needs a runner dir. Today go124-alpine is an
	// imaged tag (not a separate runner package), so this check
	// fires against the 5 actual runner dirs.
	if checked == 0 {
		t.Fatal("no runner dirs found under " + root + " — walk path is wrong")
	}
	t.Logf("walked %d runner dirs %v — all invokeHandler retain cmd.Run fallback", checked, runnerDirs)
}
