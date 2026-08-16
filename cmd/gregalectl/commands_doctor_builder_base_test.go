// commands_doctor_builder_base_test.go — focused tests for the doctor's
// builder-base-ext4 check (issue #938 / PR-B / ADR-114).
//
// The check uses package-level hooks (locateBuilderBasePathHook,
// statHook, lookPathHook, runDebugfsHook) so tests can stub the
// filesystem + debugfs probe without root or special tools. Every
// case asserts the exact severity + message-shape contract so a
// regression that flips a warn to an error (or vice versa) is loud.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withBuilderBaseHooks installs test hooks for the duration of t and
// restores the originals at cleanup. Path/Stat/LookPath/RunDebugfs
// are all stubbed; missing fields fall back to the production hook
// so partial overrides still work.
type builderBaseHooks struct {
	Path      string
	Stat      func(string) (os.FileInfo, error)
	LookPath  func(string) (string, error)
	RunDebugfs func(ctx context.Context, debugfs, ext4, target string) ([]byte, error)
}

func withBuilderBaseHooks(t *testing.T, h builderBaseHooks) {
	t.Helper()
	origPath, origStat, origLook, origRun := locateBuilderBasePathHook, statHook, lookPathHook, runDebugfsHook
	t.Cleanup(func() {
		locateBuilderBasePathHook = origPath
		statHook = origStat
		lookPathHook = origLook
		runDebugfsHook = origRun
	})
	if h.Path != "" {
		locateBuilderBasePathHook = func() string { return h.Path }
	}
	if h.Stat != nil {
		statHook = h.Stat
	}
	if h.LookPath != nil {
		lookPathHook = h.LookPath
	}
	if h.RunDebugfs != nil {
		runDebugfsHook = h.RunDebugfs
	}
}

// TestCheckBuilderBaseExt4_FileMissing: ext4 is not staged yet →
// SeverityWarn. imaged stages on first cold boot; this finding self-
// resolves after imaged has run.
func TestCheckBuilderBaseExt4_FileMissing(t *testing.T) {
	dir := t.TempDir()
	notExt4 := filepath.Join(dir, "does-not-exist.ext4")
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: notExt4,
		Stat: func(p string) (os.FileInfo, error) {
			return nil, &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
		},
	})
	findings, err := checkBuilderBaseExt4(&doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityWarn {
		t.Errorf("severity = %q, want %q", findings[0].Severity, doctorSeverityWarn)
	}
	if findings[0].Check != doctorCheckBuilderBaseExt4 {
		t.Errorf("check = %q, want %q", findings[0].Check, doctorCheckBuilderBaseExt4)
	}
	if !strings.Contains(findings[0].Message, "not staged") {
		t.Errorf("message %q does not name 'not staged'", findings[0].Message)
	}
}

// TestCheckBuilderBaseExt4_DebugfsMissing: ext4 present but no debugfs
// on PATH → SeverityWarn. The check degrades rather than failing
// because macOS dev boxes + minimal containers lack e2fsprogs.
func TestCheckBuilderBaseExt4_DebugfsMissing(t *testing.T) {
	dir := t.TempDir()
	ext4 := filepath.Join(dir, "fake.ext4")
	if err := os.WriteFile(ext4, []byte("not a real ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: ext4,
		Stat: os.Stat,
		LookPath: func(string) (string, error) {
			return "", errors.New("exec: \"debugfs\": executable file not found in $PATH")
		},
	})
	findings, err := checkBuilderBaseExt4(&doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityWarn {
		t.Errorf("severity = %q, want %q (debugfs-missing should degrade to warn, not error)",
			findings[0].Severity, doctorSeverityWarn)
	}
	if !strings.Contains(findings[0].Message, "debugfs unavailable") {
		t.Errorf("message %q should name 'debugfs unavailable'", findings[0].Message)
	}
}

// TestCheckBuilderBaseExt4_FilePresent: debugfs runs successfully
// AND the output contains an Inode line → SeverityOK. This is the
// happy path on a Lima box or a deployed production control-plane
// node where imaged has staged the real builder-base image.
func TestCheckBuilderBaseExt4_FilePresent(t *testing.T) {
	dir := t.TempDir()
	ext4 := filepath.Join(dir, "fake.ext4")
	if err := os.WriteFile(ext4, []byte("not a real ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: ext4,
		Stat: os.Stat,
		LookPath: func(string) (string, error) {
			return "/usr/sbin/debugfs", nil
		},
		RunDebugfs: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return []byte("Inode: 12345   File mode: 0755"), nil
		},
	})
	findings, err := checkBuilderBaseExt4(&doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityOK {
		t.Errorf("severity = %q, want %q", findings[0].Severity, doctorSeverityOK)
	}
	if !strings.Contains(findings[0].Message, "present") {
		t.Errorf("message %q should name 'present'", findings[0].Message)
	}
}

// TestCheckBuilderBaseExt4_FileAbsent: debugfs runs AND returns an
// error (file does not exist in the ext4) → SeverityError. This is
// the load-bearing case: the alpine placeholder from
// sealed.env.example:25 produces exactly this finding, so the
// operator sees the broken state before running `gregale deploy`.
func TestCheckBuilderBaseExt4_FileAbsent(t *testing.T) {
	dir := t.TempDir()
	ext4 := filepath.Join(dir, "fake.ext4")
	if err := os.WriteFile(ext4, []byte("not a real ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: ext4,
		Stat: os.Stat,
		LookPath: func(string) (string, error) {
			return "/usr/sbin/debugfs", nil
		},
		RunDebugfs: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return []byte("debugfs: error: file not found: usr/local/bin/faas-guest-init"), errors.New("exit status 1")
		},
	})
	findings, err := checkBuilderBaseExt4(&doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityError {
		t.Errorf("severity = %q, want %q (file-absent MUST be error, not warn — this is the load-bearing case)",
			findings[0].Severity, doctorSeverityError)
	}
	if !strings.Contains(findings[0].Message, "missing") {
		t.Errorf("message %q should name 'missing'", findings[0].Message)
	}
}

// TestCheckBuilderBaseExt4_PathOverride verifies the FAAS_BUILDER_BASE_PATH
// env var drives locateBuilderBasePathHook — covered implicitly by
// the production wiring, but pinned here so a future refactor that
// drops the env lookup trips the test.
func TestCheckBuilderBaseExt4_PathOverride(t *testing.T) {
	custom := "/tmp/custom/builder-base.ext4"
	t.Setenv("FAAS_BUILDER_BASE_PATH", custom)
	got := locateBuilderBasePathHook()
	if got != custom {
		t.Errorf("locateBuilderBasePathHook = %q, want %q (FAAS_BUILDER_BASE_PATH override)", got, custom)
	}
}