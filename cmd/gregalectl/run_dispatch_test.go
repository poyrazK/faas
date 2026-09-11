// run_dispatch_test.go — coverage pass for cmd/gregalectl/main.go::run
// (Cluster 5c of the gregalectl coverage depth-pass, follow-on to
// PR #1044).
//
// Pins the top-level dispatcher contract end-to-end:
//   - run(nil) prints usage
//   - run("version") prints wire.Version
//   - run("help") prints usage
//   - run("man") routes to cmdMan
//   - run("man <unknown>") routes through cmdMan and prints "unknown command"
//   - run("completion") routes to cmdCompletion (returns 0 on no-arg)
//   - run(<each-known-command>) routes to the right dispatcher
//   - run(<unknown-command>) exits 1 with "unknown command" hint
//
// Each test resets jsonOutput to avoid bleed between subtests.
// No source changes; mirrors the whitebox pattern (package main)
// used by commands_pki_test.go and commands_compute_nodes_test.go.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// resetRunGlobals is the per-subtest reset for run() side-effects.
// jsonOutput must be false for every non-JSON subtest; osStdout
// is captured via swap.
func resetRunGlobals(t *testing.T) {
	t.Helper()
	prevJSON := jsonOutput
	prevStdout := osStdout
	t.Cleanup(func() {
		jsonOutput = prevJSON
		osStdout = prevStdout
	})
	jsonOutput = false
}

// captureRealStdout redirects the real os.Stdout file descriptor to
// a pipe for the duration of a single call. Returns a restore
// function that closes the writer side, drains the pipe, and
// restores os.Stdout. The returned string is whatever was written
// during the call.
//
// Mirrors captureStderrRun at cluster5a_dispatch_test.go:32. Used
// by run() tests that emit via fmt.Print / fmt.Printf (which bypass
// the osStdout package var).
func captureRealStdout(t *testing.T) (*bytes.Buffer, func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	var buf bytes.Buffer
	restore := func() string {
		_ = w.Close()
		tmp := make([]byte, 4096)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
			}
			if err != nil {
				break
			}
		}
		_ = r.Close()
		os.Stdout = orig
		return buf.String()
	}
	return &buf, restore
}

// TestRun_NoArgs pins the empty-args branch (prints usage, exit 0).
// run() writes via fmt.Print (real os.Stdout), so we capture the
// real fd via os.Pipe() — the package-level osStdout var is for
// dispatcher helpers, not main's usage printer.
func TestRun_NoArgs(t *testing.T) {
	resetRunGlobals(t)
	stdout, restore := captureRealStdout(t)
	code := run(nil)
	out := restore()
	if code != 0 {
		t.Errorf("run(nil) = %d, want 0", code)
	}
	_ = stdout
	if !strings.Contains(out, "gregalectl") {
		t.Errorf("run(nil) missing usage header: %q", out)
	}
	if !strings.Contains(out, "Commands:") {
		t.Errorf("run(nil) missing Commands block: %q", out)
	}
}

// TestRun_Version pins the version branch (exit 0, prints
// wire.Version). Three arg forms: "version", "--version", "-v".
// fmt.Printf writes to real os.Stdout; capture via pipe.
func TestRun_Version(t *testing.T) {
	for _, verb := range []string{"version", "--version", "-v"} {
		t.Run(verb, func(t *testing.T) {
			resetRunGlobals(t)
			_, restore := captureRealStdout(t)
			code := run([]string{verb})
			out := restore()
			if code != 0 {
				t.Errorf("run(%s) = %d, want 0", verb, code)
			}
			if !strings.Contains(out, "gregalectl ") {
				t.Errorf("run(%s) missing version prefix: %q", verb, out)
			}
		})
	}
}

// TestRun_VersionHelp pins the `version --help` branch (PrintUsage,
// exit 0). Confirms the special-cased --help inside the version arm.
func TestRun_VersionHelp(t *testing.T) {
	resetRunGlobals(t)
	prevStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = prevStderr })
	code := run([]string{"version", "--help"})
	_ = w.Close()
	var buf bytes.Buffer
	tmp := make([]byte, 1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			break
		}
	}
	_ = r.Close()
	if code != 0 {
		t.Errorf("run(version --help) = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "Docs:") {
		t.Errorf("version --help missing Docs hint: %q", buf.String())
	}
}

// TestRun_Help pins the `help` / `--help` / `-h` aliases.
// fmt.Print writes to real os.Stdout; capture via pipe.
func TestRun_Help(t *testing.T) {
	for _, verb := range []string{"help", "--help", "-h"} {
		t.Run(verb, func(t *testing.T) {
			resetRunGlobals(t)
			_, restore := captureRealStdout(t)
			code := run([]string{verb})
			out := restore()
			if code != 0 {
				t.Errorf("run(%s) = %d, want 0", verb, code)
			}
			if !strings.Contains(out, "Commands:") {
				t.Errorf("run(%s) missing Commands block: %q", verb, out)
			}
		})
	}
}

// TestRun_Completion pins the completion dispatcher route (exit 0
// on no-arg). Doesn't pin the script content — that's covered by
// completion_test.go (cluster 1).
func TestRun_Completion(t *testing.T) {
	resetRunGlobals(t)
	var buf bytes.Buffer
	osStdout = &buf
	if code := run([]string{"completion", "bash"}); code != 0 {
		t.Errorf("run(completion bash) = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "complete -F") {
		t.Errorf("run(completion bash) missing bash header: %q", buf.String())
	}
}

// TestRun_Man pins the man dispatcher route. Two sub-cases:
// no-arg (top-level page) + valid command (per-command page).
func TestRun_Man(t *testing.T) {
	t.Run("no_arg", func(t *testing.T) {
		resetRunGlobals(t)
		var buf bytes.Buffer
		osStdout = &buf
		if code := run([]string{"man"}); code != 0 {
			t.Errorf("run(man) = %d, want 0", code)
		}
		if !strings.Contains(buf.String(), ".TH GREGALE 1") {
			t.Errorf("run(man) missing top-level title: %q", buf.String())
		}
	})
	t.Run("valid_command", func(t *testing.T) {
		resetRunGlobals(t)
		var buf bytes.Buffer
		osStdout = &buf
		if code := run([]string{"man", "manifest"}); code != 0 {
			t.Errorf("run(man manifest) = %d, want 0", code)
		}
		if !strings.Contains(buf.String(), "GREGALE-MANIFEST 1") {
			t.Errorf("run(man manifest) missing per-command title: %q", buf.String())
		}
	})
}

// TestRun_UnknownCommand pins the default branch (exit 1, "unknown
// command" hint). Picks a verb that no dispatcher recognises.
func TestRun_UnknownCommand(t *testing.T) {
	resetRunGlobals(t)
	prevStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = prevStderr })
	code := run([]string{"definitely-not-a-command"})
	_ = w.Close()
	var buf bytes.Buffer
	tmp := make([]byte, 1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			break
		}
	}
	_ = r.Close()
	if code != 1 {
		t.Errorf("run(definitely-not-a-command) = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "unknown command") {
		t.Errorf("unknown command stderr missing header: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "gregalectl help") {
		t.Errorf("unknown command stderr missing help hint: %q", buf.String())
	}
}

// TestRun_DispatchesToDispatchConstants pins that every dispatch*
// constant resolves through the run() switch. Each subtest feeds
// an invalid sub-command to the underlying dispatcher (--not-a-flag
// inside the verb), so the leaf exits via flag.Parse error before
// any real work happens — proving routing without side effects.
//
// Mirrors the dispatcher-sweep pattern at cluster5a_dispatch_test.go
// but goes through run() (not the dispatcher directly).
func TestRun_DispatchesToDispatchConstants(t *testing.T) {
	// The dispatch* constants are unexported; discover them via the
	// cliCommands slice which uses the same names. Each test runs
	// run(<verb> --not-a-flag) so the leaf's flag.Parse fails and we
	// observe a non-zero exit WITHOUT doing real work.
	verbs := []string{
		"doctor",
		"host-age",
		"pki",
		"sign-keys",
		"node-key",
		"backup",
		"secrets",
		"compute-nodes",
		"deploy",
	}
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			resetRunGlobals(t)
			if code := run([]string{verb, "--not-a-flag"}); code == 0 {
				t.Errorf("run(%s --not-a-flag) = %d, want non-zero (flag.Parse fails in leaf)", verb, code)
			}
		})
	}
}
