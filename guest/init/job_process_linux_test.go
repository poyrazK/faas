//go:build linux

package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// adr: 1790
func TestSuperviseJobCommandRunsInProcessDirWithProcessEnv(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer
	m := JobManifest{TaskTimeoutSec: 5, LeaseToken: "lease-process"}
	proc := jobProcess{Argv: []string{"/bin/sh", "-c", `printf '%s|%s' "$(pwd)" "$IMAGE_VAR"`},
		Env: []string{"PATH=/usr/bin:/bin", "IMAGE_VAR=from-image"}, Dir: dir}
	payload := superviseJobCommandWithStdio(m, proc, 50*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)),
		consoleJobStdio{stdout: &stdout, stderr: io.Discard}, make(chan os.Signal, 1))
	if payload.ExitCode != 0 || payload.ErrorClass != "succeeded" {
		t.Fatalf("payload = %+v", payload)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got := strings.SplitN(stdout.String(), "|", 2)
	if len(got) != 2 || (got[0] != dir && got[0] != want) || got[1] != "from-image" {
		t.Fatalf("output = %q, want %s|from-image", stdout.String(), dir)
	}
}

// adr: 1790
func TestEnsureJobWorkdir(t *testing.T) {
	root := t.TempDir()
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	missing := filepath.Join(root, "a", "b")
	if err := ensureWorkingDir(missing, uid, gid); err != nil {
		t.Fatalf("create missing workdir: %v", err)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("workdir = %v, %v", info, err)
	}
	if err := ensureWorkingDir(root, uid, gid); err != nil {
		t.Fatalf("existing workdir: %v", err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureWorkingDir(file, uid, gid); err == nil {
		t.Fatal("a regular file was accepted as the working directory")
	}
}

// adr: 1790
func TestPrepareJobProcessKeepsOrdinaryJobs(t *testing.T) {
	m := JobManifest{Command: []string{"/bin/true"}, Env: map[string]string{"A": "1"}}
	proc, failure := prepareJobProcess(m, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if failure != nil || proc.Dir != "" || proc.Credential != nil || proc.Argv[0] != "/bin/true" {
		t.Fatalf("ordinary process = %+v, failure %+v", proc, failure)
	}
	if env := envMap(proc.Env); env["A"] != "1" || env["FAAS_JOB"] != "1" {
		t.Fatalf("ordinary env = %v", env)
	}
}
