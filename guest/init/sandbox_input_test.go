package main

// adr: 20261027090013 step 12 — a background command takes input between
// output polls: exec_input writes to its stdin and ends its input, and a
// command that has ended, or a resize of a command with no terminal, is
// refused.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// pollUntilExit reads a background command's output until it exits.
func pollUntilExit(t *testing.T, f *sandboxFixture, handle string) sandboxproto.ExecOutput {
	t.Helper()
	var out, step sandboxproto.ExecOutput
	out.Running = true
	deadline := time.Now().Add(5 * time.Second)
	for out.Running {
		if time.Now().After(deadline) {
			t.Fatalf("command %s still runs: %+v", handle, out)
		}
		f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecOutput, Handle: handle, StdoutOffset: out.StdoutOffset,
			StderrOffset: out.StderrOffset}, nil, nil).decode(t, &step)
		out.Stdout = append(out.Stdout, step.Stdout...)
		out.Command, out.StdoutOffset, out.StderrOffset = step.Command, step.StdoutOffset, step.StderrOffset
		if out.Running {
			time.Sleep(20 * time.Millisecond)
		}
	}
	return out
}

func startBackground(t *testing.T, f *sandboxFixture, req sandboxproto.Request) string {
	t.Helper()
	req.Background = true
	var started sandboxproto.ExecResult
	f.call(t, req, nil, nil).decode(t, &started)
	if started.Handle == "" {
		t.Fatalf("no handle: %+v", started)
	}
	return started.Handle
}

func TestSandboxBackgroundInput(t *testing.T) {
	f := newSandboxFixture(t)
	handle := startBackground(t, f, execReq("cat"))
	input := func(req sandboxproto.Request) *callResult {
		req.Op, req.Handle = sandboxproto.OpExecInput, handle
		return f.call(t, req, nil, nil)
	}
	var cmd sandboxproto.Command
	input(sandboxproto.Request{Input: []byte("hello ")}).decode(t, &cmd)
	if !cmd.Running || cmd.TTY || cmd.Handle != handle {
		t.Fatalf("after input: %+v", cmd)
	}
	if r := input(sandboxproto.Request{Rows: 24, Cols: 80}); r.code() != sandboxproto.CodeUnsupported {
		t.Fatalf("a resize without a terminal: %v", r.err)
	}
	input(sandboxproto.Request{Input: []byte("world\n"), CloseInput: true}).decode(t, &cmd)
	out := pollUntilExit(t, f, handle)
	if string(out.Stdout) != "hello world\n" || out.ExitCode != 0 {
		t.Fatalf("cat answered %q, exit %d", out.Stdout, out.ExitCode)
	}
	if r := input(sandboxproto.Request{Input: []byte("late")}); r.code() != sandboxproto.CodeCommandEnded {
		t.Fatalf("input after the end: %v", r.err)
	}
	if r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: "c0000000000000000", Input: []byte("x")}, nil, nil); r.code() != sandboxproto.CodeNotFound {
		t.Fatalf("an unknown handle: %v", r.err)
	}
}

func TestExecInputValidation(t *testing.T) {
	const handle = "c0123456789abcdef"
	for name, req := range map[string]sandboxproto.Request{
		"nothing to do":   {Handle: handle},
		"rows alone":      {Handle: handle, Rows: 24},
		"too much input":  {Handle: handle, Input: make([]byte, sandboxproto.MaxInputBytes+1)},
		"no valid handle": {Handle: "cmd-1", Input: []byte("x")},
	} {
		req.Op = sandboxproto.OpExecInput
		if err := req.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: handle, Rows: 24, Cols: 80}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a resize: %v", err)
	}
}
