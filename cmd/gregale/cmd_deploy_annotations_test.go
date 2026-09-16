// cmd/gregale/cmd_deploy_annotations_test.go — unit tests for the
// annotation helpers added by issue #977 / ADR-116. Pure-function
// tests; no daemon / network / DB seam.

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestIsValidDeploymentAnnotationTag pins the closed-set vocabulary
// mirrored from migrations/00346_deployments_annotation.sql. Any
// value NOT in this set is rejected at the CLI layer (and again at
// the apid handler + DB CHECK). Adding a new tag requires
// migrations to widen the DB CHECK + ADR update; this list is the
// CLI's mirror, drift is caught by the test below.
func TestIsValidDeploymentAnnotationTag(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// Valid closed-set values.
		{"incident_recovery", true},
		{"hotfix", true},
		{"scheduled_maintenance", true},
		{"compliance_hold", true},
		{"partner_request", true},
		// Empty string is allowed (== no tag).
		{"", true},
		// Negative cases — anything outside the set.
		{"feature_release", false},
		{"HOTFIX", false},           // case-sensitive
		{"hot fix", false},          // whitespace
		{"hotfix\n", false},         // trailing newline
		{"'hotfix'", false},         // quote-wrapped
		{"incident_recover", false}, // typo
	}
	for _, tc := range tests {
		if got := isValidDeploymentAnnotationTag(tc.in); got != tc.want {
			t.Errorf("isValidDeploymentAnnotationTag(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestDeploymentAnnotationTags_MirrorsDB pins the in-memory list
// length + ordering against the canonical DB CHECK. A test that
// fails on `len(DeploymentAnnotationTags) != 5` is the canonical
// tripwire when an operator wants to add a 6th tag.
func TestDeploymentAnnotationTags_MirrorsDB(t *testing.T) {
	if got, want := len(DeploymentAnnotationTags), 5; got != want {
		t.Fatalf("DeploymentAnnotationTags length = %d, want %d (DB CHECK is the source of truth — widen the migration first)", got, want)
	}
	// Ordering matters for help text; do not re-sort without a docs PR.
	want := []string{
		"incident_recovery",
		"hotfix",
		"scheduled_maintenance",
		"compliance_hold",
		"partner_request",
	}
	for i, w := range want {
		if DeploymentAnnotationTags[i] != w {
			t.Errorf("DeploymentAnnotationTags[%d] = %q, want %q", i, DeploymentAnnotationTags[i], w)
		}
	}
}

func TestDeployTagHelpNamesEveryAcceptedValue(t *testing.T) {
	var help string
	for _, command := range cliCommands {
		if command.Name != "deploy" {
			continue
		}
		for _, flag := range command.Flags {
			if flag.Name == "tag" {
				help = flag.Short
			}
		}
	}
	if help == "" {
		t.Fatal("deploy --tag metadata is missing")
	}
	for _, tag := range DeploymentAnnotationTags {
		if !strings.Contains(help, tag) {
			t.Errorf("deploy --tag help %q does not name accepted value %q", help, tag)
		}
	}
}

// TestResolveDeployedBy_ExplicitWins pins the precedence rule:
// the explicit --deployed-by flag value is taken verbatim, NO git
// config lookup happens. Operators rely on this to override the
// auto-capture (e.g. when running as a service account whose git
// config carries someone else's name).
func TestResolveDeployedBy_ExplicitWins(t *testing.T) {
	got := resolveDeployedBy("ci-bot")
	if got != "ci-bot" {
		t.Errorf("explicit --deployed-by must win; got %q, want ci-bot", got)
	}
}

// TestResolveDeployedBy_NonGitRepoFallsBackToEmpty covers the
// "operator ran from /tmp or a non-git directory" path. The
// helper must NOT error out — it returns "" so the deploy
// proceeds with a NULL deployed_by column (the column is
// nullable, no plan gate, no schema violation).
func TestResolveDeployedBy_NonGitRepoFallsBackToEmpty(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp: %v", err)
	}
	got := resolveDeployedBy("")
	if got != "" {
		t.Errorf("non-git dir should yield \"\"; got %q", got)
	}
}

// TestResolveDeployedBy_AutoCapturesGitUserName stages a fake git
// repo with `git config user.name` set to a known value and asserts
// the helper picks it up. Mirrors the integration-test contract
// for cmd_deploy_zero_config.go:69-72 + the cmdDeployTarball
// fallback path. Note: this test invokes the `git` CLI; on a
// machine without git installed it skips rather than fails.
func TestResolveDeployedBy_AutoCapturesGitUserName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not installed on this host: %v", err)
	}
	dir := t.TempDir()
	// `git init` requires the user.email to be set too on modern
	// git, but `git config user.name <value>` does not need it; we
	// only need a repo-shaped dir with .git/.
	runGitCmdOrSkip(t, dir, "init", "-q")
	runGitCmdOrSkip(t, dir, "config", "user.name", "Test User 977")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp git repo: %v", err)
	}
	got := resolveDeployedBy("")
	if got != "Test User 977" {
		t.Errorf("git config user.name auto-capture: got %q, want %q", got, "Test User 977")
	}
}

// runGitCmdOrSkip shells out to git via the package-level
// runGitCmd helper; if any error surfaces, the test is skipped
// (CI runners always have git; this branch is for the local-dev
// case where a developer happens to lack it).
func runGitCmdOrSkip(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := runGitCmd(dir, args...); err != nil {
		t.Skipf("git %v failed: %v", args, err)
	}
}
