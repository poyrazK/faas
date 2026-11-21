package main_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// fakeGit puts a `git` shell script first on PATH for the test.
func fakeGit(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake git")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shortGitBudgets(t *testing.T) *bytes.Buffer {
	t.Helper()
	oldTimeout, oldNotice, oldDelay, oldErr := *main.GitMetadataTimeout, *main.GitSlowNoticeAfter, *main.GitPipeWaitDelay, *main.OsStderr
	var stderr bytes.Buffer
	*main.GitMetadataTimeout, *main.GitSlowNoticeAfter, *main.GitPipeWaitDelay, *main.OsStderr = 400*time.Millisecond, 100*time.Millisecond, 200*time.Millisecond, &stderr
	t.Cleanup(func() {
		*main.GitMetadataTimeout, *main.GitSlowNoticeAfter, *main.GitPipeWaitDelay, *main.OsStderr = oldTimeout, oldNotice, oldDelay, oldErr
	})
	return &stderr
}

// GREG-03: a `git status` that never returns used to hang a project dry
// run silently, before any network request.
func TestRunGitCmd_HungGitTimesOutWithNoticeAndCommand(t *testing.T) {
	fakeGit(t, "exec sleep 30")
	stderr := shortGitBudgets(t)

	start := time.Now()
	_, err := main.RunGitCmd(t.TempDir(), "status", "--porcelain")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("runGitCmd took %s; want it bounded by the metadata timeout", elapsed)
	}
	if !errors.Is(err, main.ErrLocalGitTimeout) {
		t.Fatalf("err = %v; want ErrLocalGitTimeout", err)
	}
	// A local timeout is not a network failure: printErr renders
	// context.DeadlineExceeded as "Could not reach Gregale" (rc.349).
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; a local git timeout must not look like a transport timeout", err)
	}
	for _, want := range []string{"git status --porcelain did not finish within", "core.fsmonitor", "outside the repository"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if !strings.Contains(stderr.String(), "Waiting for `git status --porcelain`") {
		t.Errorf("stderr = %q; want a waiting notice naming the command", stderr.String())
	}
}

// A daemon that git leaves behind holding the output pipe (fsmonitor) must
// not keep the CLI waiting after git itself exited.
func TestRunGitCmd_InheritedPipeDoesNotHang(t *testing.T) {
	fakeGit(t, "echo ok; (sleep 30) & exit 0")
	shortGitBudgets(t)
	*main.GitMetadataTimeout = 10 * time.Second

	start := time.Now()
	out, err := main.RunGitCmd(t.TempDir(), "rev-parse", "HEAD")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("runGitCmd took %s after git exited; want the pipe wait bounded", elapsed)
	}
	// Go reports exec.ErrWaitDelay when it had to abandon the pipe; the
	// output git wrote before exiting is not what matters here, the bound is.
	_ = out
	_ = err
}

// rc.349 follow-up: in a repository whose git hangs, a project dry run
// reported "Could not reach Gregale … the request timed out" before making
// any request, because the git timeout wrapped context.DeadlineExceeded.
func TestProjectDryRunHungGitReportsGitNotNetwork(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "compose.yaml"), []byte("services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qm", "init"}} {
		cmd := exec.Command(realGit, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	fakeGit(t, "exec sleep 30")
	shortGitBudgets(t)
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Chdir(repo)
	main.WithResetJSONOutput(t, false)
	stdout, stderr, restore := main.SwapIO(t)
	code := main.CmdDeployTarball([]string{"--project", "--name", "demo", "--dry-run"})
	restore()
	out := stdout.String() + stderr()
	if code == 0 || strings.Contains(out, "Could not reach Gregale") || strings.Contains(out, "request timed out") {
		t.Fatalf("exit=%d; a hung local git was reported as a network failure:\n%s", code, out)
	}
	if !strings.Contains(out, "did not finish within") {
		t.Fatalf("exit=%d; output does not name the timeout:\n%s", code, out)
	}
}

func TestRunGitCmd_FastGitHasNoNotice(t *testing.T) {
	fakeGit(t, "echo clean")
	stderr := shortGitBudgets(t)
	*main.GitMetadataTimeout, *main.GitSlowNoticeAfter = 20*time.Second, 10*time.Second

	out, err := main.RunGitCmd(t.TempDir(), "status", "--porcelain")
	if err != nil || strings.TrimSpace(out) != "clean" {
		t.Fatalf("runGitCmd = %q, %v", out, err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q; want no notice for a fast call", stderr.String())
	}
}
