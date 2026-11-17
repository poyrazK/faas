package main_test

import (
	"bytes"
	"context"
	"errors"
	"os"
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
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want a deadline error", err)
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
