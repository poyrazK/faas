//go:build linux

package main

// adr: 20261027090013 step 12 — a background terminal command reads the
// input exec_input sends, is resized by it, and ends its input with ^D.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

func newTerminalFixture(t *testing.T) *sandboxFixture {
	t.Helper()
	uid := os.Getuid()
	runner := execRunner{uid: -1, setSize: setAppTaskPTYSize,
		openPTY: func(int) (*os.File, *os.File, error) { return openSandboxPTY(uid) }}
	workspace, dir := t.TempDir(), t.TempDir()
	return &sandboxFixture{srv: newSandboxServer(workspace, dir, -1, runner, nil), workspace: workspace}
}

func TestSandboxBackgroundTerminalInput(t *testing.T) {
	f := newTerminalFixture(t)

	req := execReq("sh", "-c", `read line; stty size; echo "got $line"; cat`)
	req.TTY, req.Rows, req.Cols = true, 10, 40
	handle := startBackground(t, f, req)
	input := func(r sandboxproto.Request) {
		r.Op, r.Handle = sandboxproto.OpExecInput, handle
		var cmd sandboxproto.Command
		f.call(t, r, nil, nil).decode(t, &cmd)
		if !cmd.TTY {
			t.Fatalf("the command has no terminal: %+v", cmd)
		}
	}
	input(sandboxproto.Request{Rows: 33, Cols: 101})
	input(sandboxproto.Request{Input: []byte("hi\n")})
	input(sandboxproto.Request{CloseInput: true})
	out := pollUntilExit(t, f, handle)
	text := strings.ReplaceAll(string(out.Stdout), "\r", "")
	if !strings.Contains(text, "33 101\n") || !strings.Contains(text, "got hi\n") || out.ExitCode != 0 {
		t.Fatalf("terminal output %q, exit %d", text, out.ExitCode)
	}
}

// adr: 20261027090013 step 12 — ^D after a partial line still ends a
// terminal program's input, and the terminal keeps taking input afterwards
// for the shell that outlives that program.
func TestSandboxTerminalEOFAfterAPartialLine(t *testing.T) {
	f := newTerminalFixture(t)
	req := execReq("sh", "-c", `cat; echo after-eof; read line; echo "then $line"`)
	req.TTY, req.Rows, req.Cols = true, 24, 80
	handle := startBackground(t, f, req)
	input := func(r sandboxproto.Request) {
		t.Helper()
		r.Op, r.Handle = sandboxproto.OpExecInput, handle
		var cmd sandboxproto.Command
		f.call(t, r, nil, nil).decode(t, &cmd)
	}
	input(sandboxproto.Request{Input: []byte("partial"), CloseInput: true})
	input(sandboxproto.Request{Input: []byte("more\n")})
	out := pollUntilExit(t, f, handle)
	text := strings.ReplaceAll(string(out.Stdout), "\r", "")
	if !strings.Contains(text, "after-eof\n") || !strings.Contains(text, "then more\n") || out.ExitCode != 0 {
		t.Fatalf("terminal output %q, exit %d", text, out.ExitCode)
	}
}

// adr: 20261027090013 step 12 — a terminal command that stops reading
// cannot hold exec_input: the write times out as input_stalled, and the
// command still ends and is reaped.
func TestSandboxTerminalInputStalls(t *testing.T) {
	old := sandboxInputWriteTimeout
	sandboxInputWriteTimeout = 300 * time.Millisecond
	t.Cleanup(func() { sandboxInputWriteTimeout = old })
	f := newTerminalFixture(t)
	req := execReq("sh", "-c", "stty raw -echo; sleep 2")
	req.TTY, req.Rows, req.Cols = true, 24, 80
	handle := startBackground(t, f, req)
	started := time.Now()
	r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: handle,
		Input: make([]byte, sandboxproto.MaxInputBytes)}, nil, nil)
	if r.code() != sandboxproto.CodeInputStalled {
		t.Fatalf("a full terminal: %v, want %s", r.err, sandboxproto.CodeInputStalled)
	}
	if took := time.Since(started); took > 1500*time.Millisecond {
		t.Fatalf("the stalled write took %s", took)
	}
	if out := pollUntilExit(t, f, handle); out.Running {
		t.Fatalf("the command did not end: %+v", out)
	}
}
