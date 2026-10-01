//go:build linux

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkloadOutputReopen(t *testing.T) {
	for _, drop := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-user", true: "nonroot"}[drop], func(t *testing.T) {
			if drop && os.Geteuid() != 0 {
				t.Skip("credential-drop regression requires root")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "printf direct; printf reopened >/proc/self/fd/1; printf error >/proc/self/fd/2")
			if drop {
				cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
			}
			var stdout, stderr bytes.Buffer
			output, err := prepareWorkloadOutputWithWriters(cmd, &stdout, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			err = cmd.Start()
			output.started()
			if err != nil {
				output.close()
				t.Fatal(err)
			}
			err = cmd.Wait()
			output.close()
			if err != nil {
				t.Fatal(err)
			}
			if stdout.String() != "directreopened" || stderr.String() != "error" {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestWorkloadOutputFailedStartClosesPipes(t *testing.T) {
	cmd := exec.Command("/no-such-audit-command")
	var stdout, stderr bytes.Buffer
	output, err := prepareWorkloadOutputWithWriters(cmd, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err == nil {
		t.Fatal("unexpected successful start")
	}
	output.started()
	output.close()
	for _, stream := range output.streams {
		if _, err := stream.reader.Read(make([]byte, 1)); err == nil {
			t.Fatal("read descriptor remained open")
		}
	}
}

func TestWorkloadOutputDetachedWriterCleanupBounded(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 10 >&2 & printf tail")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	output, err := prepareWorkloadOutputWithWriters(cmd, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		output.close()
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	output.started()
	if err := cmd.Wait(); err != nil {
		output.close()
		t.Fatal(err)
	}
	start := time.Now()
	output.close()
	if time.Since(start) > 2*time.Second {
		t.Fatal("log cleanup waited for detached descendant")
	}
	if !strings.Contains(stdout.String(), "tail") {
		t.Fatalf("lost tail: %q", stdout.String())
	}
}
