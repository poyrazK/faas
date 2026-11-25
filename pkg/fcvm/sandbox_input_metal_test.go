//go:build metal

// adr: 20261027090013 steps 12 and 13 — in a session VM, exec_input writes a
// background command's stdin and ends it, a background terminal is resized,
// reads its input and ends on ^D, and session_env gives every later command
// the session's environment under its own, answering only the names.
package fcvm

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/wire"
)

// sandboxDo sends one request to the session and decodes its result into
// out; frames are appended to the returned output.
func sandboxDo(t *testing.T, m *Manager, instance, sandboxID string, req sandboxproto.Request, out any) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := m.DialSandboxSession(ctx, instance, sandboxID)
	if err != nil {
		t.Fatalf("dial sandbox: %v", err)
	}
	var frames strings.Builder
	err = sandboxproto.Do(ctx, conn, req, func(_ uint32, body []byte) error {
		frames.Write(body)
		return nil
	}, out)
	return frames.String(), err
}

// backgroundOutput polls a background command until it exits or the
// deadline passes, and returns all of its stdout.
func backgroundOutput(t *testing.T, m *Manager, instance, sandboxID, handle string) (string, sandboxproto.ExecOutput) {
	t.Helper()
	var stdout strings.Builder
	var offset int64
	deadline := time.Now().Add(20 * time.Second)
	for {
		var out sandboxproto.ExecOutput
		if _, err := sandboxDo(t, m, instance, sandboxID, sandboxproto.Request{Op: sandboxproto.OpExecOutput, Handle: handle, StdoutOffset: offset}, &out); err != nil {
			t.Fatalf("exec_output %s: %v", handle, err)
		}
		stdout.Write(out.Stdout)
		offset = out.StdoutOffset
		if !out.Running {
			return stdout.String(), out
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still running after 20 s; output so far %q", handle, stdout.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestMetalSandboxInputAndEnv(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	if kernel == "" || base == "" {
		t.Skip("set FAAS_TEST_KERNEL and FAAS_TEST_AGENT_RUN_BASE (an agent-base ext4 with this checkout's guest-init)")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	const instance, sandboxID = "sbx-input", "sbx-input-1"
	if _, err := m.WakeSandbox(context.Background(), SandboxWakeRequest{Instance: instance, SandboxID: sandboxID, AccountID: "acct-sandbox",
		Plan: api.PlanPro, BaseKey: base, LayerKey: sandboxRootLayer(t), VcpuCount: 1, MemSizeMiB: 512, CPUMillicores: 1000,
		Network: api.SandboxNetworkNone}); err != nil {
		t.Fatalf("wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	run := func(req sandboxproto.Request, out any) (string, error) {
		return sandboxDo(t, m, instance, sandboxID, req, out)
	}
	start := func(req sandboxproto.Request) string {
		t.Helper()
		req.Op, req.Background, req.TimeoutMillis, req.MaxOutputBytes = sandboxproto.OpExec, true, 30_000, 1<<20
		var started sandboxproto.ExecResult
		if _, err := run(req, &started); err != nil || started.Handle == "" {
			t.Fatalf("start %v: %+v %v", req.Argv, started, err)
		}
		return started.Handle
	}

	t.Run("stdin of a background command", func(t *testing.T) {
		handle := start(sandboxproto.Request{Argv: []string{"/bin/cat"}})
		var cmd sandboxproto.Command
		if _, err := run(sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: handle, Input: []byte("hello from stdin\n"), CloseInput: true}, &cmd); err != nil {
			t.Fatalf("exec_input: %v", err)
		}
		out, done := backgroundOutput(t, m, instance, sandboxID, handle)
		if out != "hello from stdin\n" || done.ExitCode != 0 {
			t.Fatalf("cat wrote %q, exit %d", out, done.ExitCode)
		}
		// Its input has ended: more is refused.
		_, err := run(sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: handle, Input: []byte("late\n")}, &cmd)
		if err == nil || !strings.Contains(err.Error(), sandboxproto.CodeCommandEnded) {
			t.Fatalf("input after the end = %v, want %s", err, sandboxproto.CodeCommandEnded)
		}
	})

	t.Run("a background terminal", func(t *testing.T) {
		handle := start(sandboxproto.Request{Argv: []string{"/bin/sh", "-c", "read line; stty size; echo got:$line"}, TTY: true, Rows: 24, Cols: 80})
		var cmd sandboxproto.Command
		if _, err := run(sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: handle, Rows: 30, Cols: 100, Input: []byte("typed\n")}, &cmd); err != nil {
			t.Fatalf("exec_input: %v", err)
		}
		if !cmd.TTY {
			t.Fatalf("the command is not on a terminal: %+v", cmd)
		}
		out, done := backgroundOutput(t, m, instance, sandboxID, handle)
		if !strings.Contains(out, "30 100") || !strings.Contains(out, "got:typed") || done.ExitCode != 0 {
			t.Fatalf("terminal output %q, exit %d; want the new size and the typed line", out, done.ExitCode)
		}
		// ^D ends a terminal's input without closing its output.
		handle = start(sandboxproto.Request{Argv: []string{"/bin/sh", "-c", "cat; echo after-eof"}, TTY: true, Rows: 24, Cols: 80})
		if _, err := run(sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: handle, CloseInput: true}, &cmd); err != nil {
			t.Fatalf("exec_input eof: %v", err)
		}
		if out, _ := backgroundOutput(t, m, instance, sandboxID, handle); !strings.Contains(out, "after-eof") {
			t.Fatalf("terminal after ^D wrote %q", out)
		}
	})

	t.Run("the session environment", func(t *testing.T) {
		var set sandboxproto.SessionEnvResult
		frames, err := run(sandboxproto.Request{Op: sandboxproto.OpSessionEnv, Env: map[string]string{"SESSION_TOKEN": "s3cret", "SHARED": "session"}}, &set)
		if err != nil || !slices.Equal(set.Names, []string{"SESSION_TOKEN", "SHARED"}) {
			t.Fatalf("session_env = %+v %v", set, err)
		}
		if strings.Contains(frames, "s3cret") {
			t.Fatal("session_env echoed a value")
		}
		var result sandboxproto.ExecResult
		out, err := run(sandboxproto.Request{Op: sandboxproto.OpExec, Argv: []string{"/bin/sh", "-c", "echo $SESSION_TOKEN $SHARED"},
			Env: map[string]string{"SHARED": "own"}, TimeoutMillis: 10_000, MaxOutputBytes: 1 << 16}, &result)
		if err != nil || strings.TrimSpace(out) != "s3cret own" {
			t.Fatalf("command saw %q (%v); want the session's value under its own", out, err)
		}
		if _, err := run(sandboxproto.Request{Op: sandboxproto.OpSessionEnv, Env: map[string]string{}}, &set); err != nil || len(set.Names) != 0 {
			t.Fatalf("clearing = %+v %v", set, err)
		}
		out, err = run(sandboxproto.Request{Op: sandboxproto.OpExec, Argv: []string{"/bin/sh", "-c", "echo [$SESSION_TOKEN]"},
			TimeoutMillis: 10_000, MaxOutputBytes: 1 << 16}, &result)
		if err != nil || strings.TrimSpace(out) != "[]" {
			t.Fatalf("after clearing a command saw %q (%v)", out, err)
		}
	})
}
