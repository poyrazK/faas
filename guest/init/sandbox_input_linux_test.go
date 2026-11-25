//go:build linux

package main

// adr: 20261027090013 step 12 — a background terminal command reads the
// input exec_input sends, is resized by it, and ends its input with ^D.

import (
	"os"
	"strings"
	"sync"
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
	// The partial line and the end of input come in separate calls.
	input(sandboxproto.Request{Input: []byte("partial")})
	input(sandboxproto.Request{CloseInput: true})
	input(sandboxproto.Request{Input: []byte("more\n")})
	out := pollUntilExit(t, f, handle)
	text := strings.ReplaceAll(string(out.Stdout), "\r", "")
	if !strings.Contains(text, "after-eof\n") || !strings.Contains(text, "then more\n") || out.ExitCode != 0 {
		t.Fatalf("terminal output %q, exit %d", text, out.ExitCode)
	}
}

// adr: 20261027090013 step 12 — a terminal command that stops reading
// cannot hold exec_input: the write times out as input_stalled, and the
// command still ends and is reaped. It sleeps far past the test so no call
// can see it exit first.
func TestSandboxTerminalInputStalls(t *testing.T) {
	old := sandboxInputWriteTimeout
	sandboxInputWriteTimeout = 300 * time.Millisecond
	t.Cleanup(func() { sandboxInputWriteTimeout = old })
	f := newTerminalFixture(t)
	req := execReq("sh", "-c", "stty raw -echo; sleep 600")
	req.TTY, req.Rows, req.Cols = true, 24, 80
	handle := startBackground(t, f, req)
	started := time.Now()
	r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: handle,
		Input: make([]byte, sandboxproto.MaxInputBytes)}, nil, nil)
	if r.code() != sandboxproto.CodeInputStalled {
		t.Fatalf("a full terminal: %v, want %s", r.err, sandboxproto.CodeInputStalled)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("the stalled write took %s", took)
	}
	// A call queued behind the stalled one gives up as quickly, so a
	// stalled command cannot hold the session's operations.
	var wg sync.WaitGroup
	codes := make([]string, 4)
	started = time.Now()
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecInput, Handle: handle,
				Input: make([]byte, sandboxproto.MaxInputBytes)}, nil, nil).code()
		}()
	}
	wg.Wait()
	for _, code := range codes {
		if code != sandboxproto.CodeInputStalled {
			t.Fatalf("concurrent stalled inputs answered %v", codes)
		}
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("queued stalled writes took %s", took)
	}
	// The command still ends and is reaped once killed.
	if r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecKill, Handle: handle, Signal: "KILL"}, nil, nil); r.err != nil {
		t.Fatalf("kill: %v", r.err)
	}
	if out := pollUntilExit(t, f, handle); out.Running {
		t.Fatalf("the command did not end: %+v", out)
	}
}
