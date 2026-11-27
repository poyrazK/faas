package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// #2669: `gregale deploy` uploads `git archive HEAD` (committed files only)
// when the source is in a repository with an origin remote and --source is not
// worktree. doctor walked the working tree with only .gregaleignore applied,
// so a .gitignore'd fixture produced blocking loopback/architecture findings
// for files that never ship. doctor must ask git which paths packaging omits
// instead of re-parsing .gitignore (which uses different grammar and has
// per-directory, info/exclude and global variants).

const (
	doctorFixtureGoMod = "module example.com/demo\n\ngo 1.26\n"

	doctorFixtureMainGo = `package main

import "net/http"

func main() {
	_ = http.ListenAndServe("0.0.0.0:8080", nil)
}
`

	doctorFixtureLoopbackGo = `package nested

import "net/http"

func bad() {
	_ = http.ListenAndServe("127.0.0.1:9999", nil)
}
`

	doctorFixtureIgnoredEnvGo = `package nested

import "os"

var _ = os.Getenv("IGNORED_ONLY_SECRET")
`
)

type doctorFixtureCheck struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Sources []string `json:"sources"`
}

type doctorFixtureReport struct {
	Path    string `json:"path"`
	Profile *struct {
		Warnings []struct {
			Code    string   `json:"code"`
			Sources []string `json:"sources"`
		} `json:"warnings"`
	} `json:"profile"`
	Checks []doctorFixtureCheck `json:"checks"`
}

func (r doctorFixtureReport) check(t *testing.T, name string) doctorFixtureCheck {
	t.Helper()
	for _, check := range r.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("doctor report has no %s check", name)
	return doctorFixtureCheck{}
}

func runDoctorFixtureJSON(t *testing.T, args ...string) (doctorFixtureReport, int) {
	t.Helper()
	main.WithResetJSONOutput(t, true)
	stdout, _, restore := main.SwapIO(t)
	code := main.CmdDoctor(args)
	restore()
	if stdout.Len() == 0 {
		t.Fatalf("doctor wrote no JSON for %v", args)
	}
	var report doctorFixtureReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("doctor JSON: %v\n%s", err, stdout.String())
	}
	return report, code
}

func writeDoctorFixtureFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func doctorFixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newDoctorFixtureRepo creates a git repository and isolates it from the
// developer's global configuration (ignores, signing hooks).
func newDoctorFixtureRepo(t *testing.T, origin bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	dir := t.TempDir()
	doctorFixtureGit(t, dir, "init", "-q")
	doctorFixtureGit(t, dir, "config", "user.name", "doctor fixture")
	doctorFixtureGit(t, dir, "config", "user.email", "doctor@example.invalid")
	doctorFixtureGit(t, dir, "config", "commit.gpgsign", "false")
	if origin {
		doctorFixtureGit(t, dir, "remote", "add", "origin", "https://github.com/example/doctor-fixture.git")
	}
	return dir
}

func commitDoctorFixture(t *testing.T, dir string) {
	t.Helper()
	doctorFixtureGit(t, dir, "add", "-A")
	doctorFixtureGit(t, dir, "commit", "-q", "-m", "fixture")
}

// writeIgnoredDoctorFixture writes a valid Go app plus an ignored tree that
// holds exactly the three findings #2669 reported: a loopback listener, a
// foreign-architecture executable and an env reference.
func writeIgnoredDoctorFixture(t *testing.T, dir string) {
	t.Helper()
	writeDoctorFixtureFile(t, dir, "go.mod", doctorFixtureGoMod)
	writeDoctorFixtureFile(t, dir, "main.go", doctorFixtureMainGo)
	writeDoctorFixtureFile(t, dir, ".gitignore", "ignored/\n")
	writeDoctorFixtureFile(t, dir, "ignored/nested/bad.go", doctorFixtureLoopbackGo)
	writeDoctorFixtureFile(t, dir, "ignored/nested/env.go", doctorFixtureIgnoredEnvGo)
	// Mach-O 64-bit magic; doctor's arch check rejects anything that is not
	// Linux/amd64.
	writeDoctorFixtureFile(t, dir, "ignored/nested/gregale-darwin-arm64", "\xcf\xfa\xed\xfe"+strings.Repeat("\x00", 60))
}

// TestDoctorGitArchiveSkipsIgnoredPaths is the original bug: with an origin
// remote and no --source=worktree, deploy uploads git archive HEAD, which
// never contains ignored/nested. doctor must agree.
func TestDoctorGitArchiveSkipsIgnoredPaths(t *testing.T) {
	dir := newDoctorFixtureRepo(t, true)
	writeIgnoredDoctorFixture(t, dir)
	commitDoctorFixture(t, dir)

	report, code := runDoctorFixtureJSON(t, "--path", dir)
	if code != 0 {
		t.Fatalf("doctor exit = %d, want 0; checks = %+v", code, report.Checks)
	}
	for _, name := range []string{"loopback-bind", "arch", "env-required"} {
		check := report.check(t, name)
		if check.Status != "ok" {
			t.Errorf("%s status = %q, want ok; sources = %v", name, check.Status, check.Sources)
		}
		for _, source := range check.Sources {
			if strings.Contains(source, "ignored/") {
				t.Errorf("%s cited a git-ignored source: %s", name, source)
			}
		}
	}
	if report.Profile == nil {
		t.Fatalf("doctor report has no profile to check for ignored warnings")
	}
	for _, warning := range report.Profile.Warnings {
		for _, source := range warning.Sources {
			if strings.Contains(source, "ignored/") {
				t.Errorf("profile warning %s cited a git-ignored source: %s", warning.Code, source)
			}
		}
	}
}

// TestDoctorGitArchiveUsesRepositoryRootGitignore covers the case
// `--path apps/api` hit: the rule lives at the repository root, not in the
// scanned directory.
func TestDoctorGitArchiveUsesRepositoryRootGitignore(t *testing.T) {
	repo := newDoctorFixtureRepo(t, true)
	writeDoctorFixtureFile(t, repo, ".gitignore", "apps/api/ignored/\n")
	writeDoctorFixtureFile(t, repo, "README.md", "# fixture\n")
	writeDoctorFixtureFile(t, repo, "apps/api/go.mod", doctorFixtureGoMod)
	writeDoctorFixtureFile(t, repo, "apps/api/main.go", doctorFixtureMainGo)
	writeDoctorFixtureFile(t, repo, "apps/api/ignored/bad.go", doctorFixtureLoopbackGo)
	commitDoctorFixture(t, repo)

	report, code := runDoctorFixtureJSON(t, "--path", filepath.Join(repo, "apps", "api"))
	if code != 0 {
		t.Fatalf("doctor exit = %d, want 0; checks = %+v", code, report.Checks)
	}
	check := report.check(t, "loopback-bind")
	if check.Status != "ok" {
		t.Errorf("loopback-bind status = %q, want ok; sources = %v", check.Status, check.Sources)
	}
}

// TestDoctorWorktreePackagingScansIgnoredPaths guards #2669's first
// acceptance criterion: loadPackRules never reads .gitignore, so in worktree
// mode every file on disk ships and doctor must keep reporting it.
func TestDoctorWorktreePackagingScansIgnoredPaths(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		origin bool
		args   []string
	}{
		{name: "explicit source worktree", origin: true, args: []string{"--source=worktree"}},
		{name: "auto without origin remote", origin: false, args: nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := newDoctorFixtureRepo(t, testCase.origin)
			writeIgnoredDoctorFixture(t, dir)
			commitDoctorFixture(t, dir)

			args := append([]string{"--path", dir}, testCase.args...)
			report, code := runDoctorFixtureJSON(t, args...)
			if code != 1 {
				t.Fatalf("doctor exit = %d, want 1; checks = %+v", code, report.Checks)
			}
			check := report.check(t, "loopback-bind")
			if check.Status != "error" {
				t.Errorf("loopback-bind status = %q, want error; sources = %v", check.Status, check.Sources)
			}
			if !sourcesContain(check.Sources, "ignored/nested/bad.go") {
				t.Errorf("loopback-bind sources = %v, want ignored/nested/bad.go (that file ships in worktree mode)", check.Sources)
			}
		})
	}
}

// TestDoctorNonGitDirectoryScansIgnoredPaths: a directory outside Git has no
// packaging-side ignore rule; deploy packs every file, so .gitignore is just
// text and doctor must not treat it as an exclusion list.
func TestDoctorNonGitDirectoryScansIgnoredPaths(t *testing.T) {
	dir := t.TempDir()
	writeIgnoredDoctorFixture(t, dir)

	report, code := runDoctorFixtureJSON(t, "--path", dir)
	if code != 1 {
		t.Fatalf("doctor exit = %d, want 1; checks = %+v", code, report.Checks)
	}
	check := report.check(t, "loopback-bind")
	if check.Status != "error" {
		t.Errorf("loopback-bind status = %q, want error; sources = %v", check.Status, check.Sources)
	}
	if !sourcesContain(check.Sources, "ignored/nested/bad.go") {
		t.Errorf("loopback-bind sources = %v, want ignored/nested/bad.go (non-git source ships every file)", check.Sources)
	}
}

// TestDoctorGitArchiveKeepsTrackedFilesMatchingGitignore: git archive ships
// every tracked file, even one a .gitignore pattern matches, so doctor must
// keep scanning it.
func TestDoctorGitArchiveKeepsTrackedFilesMatchingGitignore(t *testing.T) {
	dir := newDoctorFixtureRepo(t, true)
	writeDoctorFixtureFile(t, dir, "go.mod", doctorFixtureGoMod)
	writeDoctorFixtureFile(t, dir, "main.go", doctorFixtureMainGo)
	writeDoctorFixtureFile(t, dir, ".gitignore", "ignored/\n")
	writeDoctorFixtureFile(t, dir, "ignored/bad.go", doctorFixtureLoopbackGo)
	doctorFixtureGit(t, dir, "add", "-A")
	doctorFixtureGit(t, dir, "add", "-f", "ignored/bad.go")
	doctorFixtureGit(t, dir, "commit", "-q", "-m", "fixture")

	report, code := runDoctorFixtureJSON(t, "--path", dir)
	if code != 1 {
		t.Fatalf("doctor exit = %d, want 1; checks = %+v", code, report.Checks)
	}
	check := report.check(t, "loopback-bind")
	if check.Status != "error" {
		t.Errorf("loopback-bind status = %q, want error; sources = %v", check.Status, check.Sources)
	}
	if !sourcesContain(check.Sources, "ignored/bad.go") {
		t.Errorf("loopback-bind sources = %v, want ignored/bad.go (git archive ships tracked files)", check.Sources)
	}
}

func sourcesContain(sources []string, want string) bool {
	for _, source := range sources {
		if strings.Contains(source, want) {
			return true
		}
	}
	return false
}
