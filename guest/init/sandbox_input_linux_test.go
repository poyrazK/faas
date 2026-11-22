//go:build linux

package main

// adr: 20261027090013 step 12 — a background terminal command reads the
// input exec_input sends, is resized by it, and ends its input with ^D.

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

func TestSandboxBackgroundTerminalInput(t *testing.T) {
	uid := os.Getuid()
	runner := execRunner{uid: -1, setSize: setAppTaskPTYSize,
		openPTY: func(int) (*os.File, *os.File, error) { return openSandboxPTY(uid) }}
	workspace, dir := t.TempDir(), t.TempDir()
	f := &sandboxFixture{srv: newSandboxServer(workspace, dir, -1, runner, nil), workspace: workspace}

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
