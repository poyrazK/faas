// Tests for cmd/gregale/commands_release.go. The dispatch surface
// (usage, unknown subcommand, --help) is unit-testable; the bundle
// and install paths require a real Postgres (FAAS_PG_DSN) and are
// covered by cmd/e2e/release_install_test.go.
//
// The drift test (commands_completion_test.go:137) enforces that
// main.go's `case "release":` and cli_meta.go's cliCommand{Name: "release"}
// are both present or both absent — those are not covered here.

package main

import (
	"os"
	"strings"
	"testing"
)

func TestCmdReleaseDispatch_NoArgs(t *testing.T) {
	if code := cmdReleaseDispatch(nil); code != 1 {
		t.Errorf("cmdReleaseDispatch(nil) = %d, want 1", code)
	}
}

func TestCmdReleaseDispatch_Unknown(t *testing.T) {
	if code := cmdReleaseDispatch([]string{"wat"}); code != 1 {
		t.Errorf("cmdReleaseDispatch(wat) = %d, want 1", code)
	}
}

func TestCmdReleaseDispatch_Help(t *testing.T) {
	for _, h := range []string{"-h", "--help"} {
		if code := cmdReleaseDispatch([]string{h}); code != 0 {
			t.Errorf("cmdReleaseDispatch(%s) = %d, want 0", h, code)
		}
	}
}

func TestCmdReleaseHistory_Validation(t *testing.T) {
	if code := cmdReleaseHistory([]string{"--limit=0"}); code != 1 {
		t.Errorf("history invalid limit = %d, want 1", code)
	}
	if code := cmdReleaseHistory([]string{"unexpected"}); code != 1 {
		t.Errorf("history positional argument = %d, want 1", code)
	}
}

func TestCmdReleaseInspect_Validation(t *testing.T) {
	if code := cmdReleaseInspect(nil); code != 1 {
		t.Errorf("inspect missing SHA = %d, want 1", code)
	}
	if code := cmdReleaseInspect([]string{"not-a-sha"}); code != 1 {
		t.Errorf("inspect malformed SHA = %d, want 1", code)
	}
}

func TestCmdReleaseBundle_Help(t *testing.T) {
	for _, h := range []string{"-h", "--help"} {
		if code := cmdReleaseBundle([]string{h}); code != 0 {
			t.Errorf("cmdReleaseBundle(%s) = %d, want 0", h, code)
		}
	}
}

func TestCmdReleaseBundle_MissingFlags(t *testing.T) {
	if code := cmdReleaseBundle(nil); code != 1 {
		t.Errorf("cmdReleaseBundle(nil) = %d, want 1", code)
	}
	if code := cmdReleaseBundle([]string{"--bin-dir=/tmp/x"}); code != 1 {
		t.Errorf("cmdReleaseBundle(missing git-sha) = %d, want 1", code)
	}
	if code := cmdReleaseBundle([]string{"--bin-dir=/tmp/x", "--git-sha=abc"}); code != 1 {
		t.Errorf("cmdReleaseBundle(missing manifest-hash) = %d, want 1", code)
	}
}

func TestCmdReleaseBundle_BadGitSHA(t *testing.T) {
	// 40 hex chars, but uppercase — releaseinstall rejects non-lowercase.
	if code := cmdReleaseBundle([]string{
		"--bin-dir=/tmp/x",
		"--git-sha=0123456789ABCDEF0123456789ABCDEF01234567",
		"--manifest-hash=sha256:" + strings.Repeat("a", 64),
	}); code != 1 {
		t.Errorf("cmdReleaseBundle(uppercase sha) = %d, want 1 (releaseinstall rejects)", code)
	}
}

func TestCmdReleaseInstall_Help(t *testing.T) {
	for _, h := range []string{"-h", "--help"} {
		if code := cmdReleaseInstall([]string{h}); code != 0 {
			t.Errorf("cmdReleaseInstall(%s) = %d, want 0", h, code)
		}
	}
}

func TestCmdReleaseInstall_MissingFlags(t *testing.T) {
	if code := cmdReleaseInstall(nil); code != 1 {
		t.Errorf("cmdReleaseInstall(nil) = %d, want 1", code)
	}
}

func TestReadDatabaseEnvFile(t *testing.T) {
	path := t.TempDir() + "/compute-db.env"
	if err := os.WriteFile(path, []byte("# comment\nOTHER=value\nexport DATABASE_URL='postgres:///faas?host=/run/postgresql'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := readDatabaseEnvFile(path)
	if !ok {
		t.Fatal("readDatabaseEnvFile returned ok=false")
	}
	want := "postgres:///faas?host=/run/postgresql"
	if got != want {
		t.Fatalf("readDatabaseEnvFile = %q, want %q", got, want)
	}
}

func TestReadDatabaseEnvFileMissing(t *testing.T) {
	if got, ok := readDatabaseEnvFile(t.TempDir() + "/missing.env"); ok || got != "" {
		t.Fatalf("readDatabaseEnvFile missing = (%q, %v), want (empty, false)", got, ok)
	}
}

// TestCmdReleaseInstall_BadGitSHA asserts the new gitSHA-validity
// gate added by ADR-113 review-fix #4: any non-40-char-lowercase-
// hex --git-sha is rejected at flag-validation time (exit 2,
// before any side effects), so the path-traversal gosec on the
// SBoM-on-disk writefile can never receive an attacker-controlled
// gitSHA.
func TestCmdReleaseInstall_BadGitSHA(t *testing.T) {
	for _, bad := range []string{
		"../etc/passwd",
		"not-a-sha",
		"0123456789ABCDEF0123456789ABCDEF01234567", // uppercase
	} {
		code := cmdReleaseInstall([]string{"--git-sha=" + bad})
		if code != 2 {
			t.Errorf("cmdReleaseInstall(--git-sha=%q) = %d, want 2 (rejected at flag-validation)", bad, code)
		}
	}
}

// TestCmdReleaseInstall_LegacyAndTarballMutuallyExclusive asserts
// review-fix #4: passing both --legacy-bundle-dir and
// --tarball-path is a usage error (exit 2), not a runtime error.
func TestCmdReleaseInstall_LegacyAndTarballMutuallyExclusive(t *testing.T) {
	code := cmdReleaseInstall([]string{
		"--git-sha=0123456789abcdef0123456789abcdef01234567",
		"--legacy-bundle-dir=/tmp/x",
		"--tarball-path=/tmp/y",
	})
	if code != 2 {
		t.Errorf("cmdReleaseInstall(legacy+tarball) = %d, want 2 (mutually exclusive)", code)
	}
}
