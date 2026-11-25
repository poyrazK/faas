package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// sandboxCommands is a session's background commands. Each keeps the most
// recent max_output_bytes of each stream; output read past them is reported
// as skipped. Finished commands are forgotten, oldest first, when the session
// needs room for a new one.
type sandboxCommands struct {
	mu     sync.Mutex
	runner sandboxRunner
	max    int
	now    func() time.Time
	byID   map[string]*backgroundCommand
	order  []string
}

type backgroundCommand struct {
	handle  string
	argv    []string
	tty     bool
	started time.Time
	proc    sandboxProcess
	stdout  *outputRing
	stderr  *outputRing
	done    chan struct{}
	exit    sandboxExit

	// inTurn admits one exec_input at a time; a call waits for its turn no
	// longer than one write may take. linePending is guarded by it.
	inTurn chan struct{}
	// inputEnded is set when the command exits or its pipe is closed. The
	// exit never waits for an input write: closing the pipe ends it.
	inputEnded atomic.Bool
	// linePending: the terminal holds a partial line, so a ^D would only
	// hand it over rather than end the input.
	linePending bool
}

// sandboxInputWriteTimeout bounds one exec_input write, so a command that
// stops reading its input cannot hold an operation open. Tests shorten it.
var sandboxInputWriteTimeout = 5 * time.Second

func newSandboxCommands(runner sandboxRunner, max int, now func() time.Time) *sandboxCommands {
	return &sandboxCommands{runner: runner, max: max, now: now, byID: map[string]*backgroundCommand{}}
}

// start runs a command in the background and returns its handle at once.
// It is stopped like a foreground command when its timeout passes.
func (c *sandboxCommands) start(spec sandboxCommandSpec, req sandboxproto.Request) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.order) >= c.max && !c.forgetFinishedLocked() {
		return nil, sandboxError(sandboxproto.CodeLimit, "the session keeps at most %d background commands; kill one first", c.max)
	}
	proc, err := c.runner.start(spec)
	if err != nil {
		return nil, sandboxError(sandboxproto.CodeStartFailed, "%s", err.Error())
	}
	cmd := &backgroundCommand{handle: newCommandHandle(), argv: append([]string(nil), spec.argv...), tty: spec.tty, started: c.now(), proc: proc,
		stdout: newOutputRing(req.MaxOutputBytes), stderr: newOutputRing(req.MaxOutputBytes), done: make(chan struct{}),
		inTurn: make(chan struct{}, 1)}
	c.byID[cmd.handle] = cmd
	c.order = append(c.order, cmd.handle)
	go cmd.run(time.Duration(req.TimeoutMillis) * time.Millisecond)
	return sandboxproto.ExecResult{Handle: cmd.handle}, nil
}

// forgetFinishedLocked drops the oldest finished command, if any.
func (c *sandboxCommands) forgetFinishedLocked() bool {
	for i, handle := range c.order {
		select {
		case <-c.byID[handle].done:
			delete(c.byID, handle)
			c.order = append(c.order[:i], c.order[i+1:]...)
			return true
		default:
		}
	}
	return false
}

func (b *backgroundCommand) run(timeout time.Duration) {
	var pumps sync.WaitGroup
	for _, s := range []struct {
		r    interface{ Read([]byte) (int, error) }
		ring *outputRing
	}{{b.proc.stdout(), b.stdout}, {b.proc.stderr(), b.stderr}} {
		if s.r == nil || s.ring == nil {
			continue
		}
		pumps.Add(1)
		go func() {
			defer pumps.Done()
			buf := make([]byte, 32<<10)
			for {
				n, err := s.r.Read(buf)
				if n > 0 {
					_, _ = s.ring.Write(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
	}
	exited := make(chan sandboxExit, 1)
	go func() { exited <- b.proc.wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case b.exit = <-exited:
	case <-timer.C:
		_ = b.proc.signal(syscall.SIGTERM)
		select {
		case b.exit = <-exited:
		case <-time.After(sandboxproto.StopGrace):
			_ = b.proc.signal(syscall.SIGKILL)
			b.exit = <-exited
		}
	}
	// A terminal's input is its output too: closeOutput closes it once the
	// output is drained.
	b.inputEnded.Store(true)
	if !b.tty {
		_ = b.proc.stdin().Close()
	}
	drained := make(chan struct{})
	go func() { pumps.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(sandboxDrainAfterExit):
	}
	b.proc.closeOutput()
	close(b.done)
}

func (b *backgroundCommand) info() sandboxproto.Command {
	out := sandboxproto.Command{Handle: b.handle, Argv: b.argv, Running: true, TTY: b.tty, StartedAtUnix: b.started.UnixMilli()}
	select {
	case <-b.done:
		out.Running, out.ExitCode, out.Signal = false, b.exit.code, b.exit.signal
	default:
	}
	return out
}

func (c *sandboxCommands) find(handle string) (*backgroundCommand, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd, ok := c.byID[handle]
	if !ok {
		return nil, sandboxError(sandboxproto.CodeNotFound, "no background command %s", handle)
	}
	return cmd, nil
}

func (c *sandboxCommands) list() sandboxproto.ExecList {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := sandboxproto.ExecList{Commands: make([]sandboxproto.Command, 0, len(c.order))}
	for _, handle := range c.order {
		out.Commands = append(out.Commands, c.byID[handle].info())
	}
	return out
}

// output returns a command's output since the request's offsets, at most
// max_bytes of each stream when set.
func (c *sandboxCommands) output(req sandboxproto.Request) (any, error) {
	cmd, err := c.find(req.Handle)
	if err != nil {
		return nil, err
	}
	limit := req.MaxBytes
	if limit == 0 {
		limit = sandboxproto.MaxOutputBytes
	}
	out := sandboxproto.ExecOutput{Command: cmd.info()}
	var skippedOut, skippedErr bool
	out.Stdout, out.StdoutOffset, skippedOut = cmd.stdout.read(req.StdoutOffset, limit)
	out.Stderr, out.StderrOffset, skippedErr = cmd.stderr.read(req.StderrOffset, limit)
	out.SkippedOutput = skippedOut || skippedErr
	return out, nil
}

// kill signals a command's process group: KILL unless the request names
// TERM or INT.
func (c *sandboxCommands) kill(req sandboxproto.Request) (any, error) {
	cmd, err := c.find(req.Handle)
	if err != nil {
		return nil, err
	}
	sig := syscall.SIGKILL
	switch req.Signal {
	case "TERM":
		sig = syscall.SIGTERM
	case "INT":
		sig = syscall.SIGINT
	}
	if err := cmd.proc.signal(sig); err != nil {
		return nil, sandboxError(sandboxproto.CodeInternal, "signal %s: %s", cmd.handle, err.Error())
	}
	return cmd.info(), nil
}

// input resizes a background command's terminal, writes to its stdin and
// ends its input (exec_input). A terminal's input ends with ^D, since
// closing it would also close its output; the terminal itself stays open
// for input until its command exits.
func (c *sandboxCommands) input(req sandboxproto.Request) (any, error) {
	cmd, err := c.find(req.Handle)
	if err != nil {
		return nil, err
	}
	select {
	case cmd.inTurn <- struct{}{}:
		defer func() { <-cmd.inTurn }()
	case <-time.After(sandboxInputWriteTimeout):
		return nil, sandboxError(sandboxproto.CodeInputStalled, "command %s is not reading its input; an earlier input is still waiting", cmd.handle)
	}
	if cmd.inputEnded.Load() {
		return nil, sandboxError(sandboxproto.CodeCommandEnded, "command %s has ended or its input was closed", cmd.handle)
	}
	if req.Rows > 0 {
		if !cmd.tty {
			return nil, sandboxError(sandboxproto.CodeUnsupported, "command %s has no terminal to resize", cmd.handle)
		}
		if err := cmd.proc.resize(req.Rows, req.Cols); err != nil {
			return nil, sandboxError(sandboxproto.CodeInternal, "resize %s: %s", cmd.handle, err.Error())
		}
	}
	data := req.Input
	if cmd.tty {
		if req.CloseInput {
			data = terminalEOF(cmd.linePending, data)
		}
		// The line state follows what was written, even in part: a stalled
		// write leaves it unknown, and assuming a pending line at worst
		// hands over an empty one.
		cmd.linePending, _ = terminalLine(cmd.linePending, data)
	}
	if err := writeInput(cmd.proc.stdin(), data); err != nil {
		return nil, inputError(cmd.handle, err)
	}
	// A terminal outlives the program that read its ^D (a shell returns to
	// its prompt), so only a pipe's input ends here.
	if req.CloseInput && !cmd.tty {
		cmd.inputEnded.Store(true)
		_ = cmd.proc.stdin().Close()
	}
	return cmd.info(), nil
}

// terminalLine follows a canonical terminal's line through data: whether a
// partial line is pending afterwards, and whether data ended in a ^D that
// ended input (one at the start of a line).
func terminalLine(pending bool, data []byte) (bool, bool) {
	endedInput := false
	for _, b := range data {
		endedInput = false
		switch b {
		case '\n', '\r':
			pending = false
		case 4:
			endedInput, pending = !pending, false
		default:
			pending = true
		}
	}
	return pending, endedInput
}

// terminalEOF makes data end the terminal program's input. A canonical
// terminal reads ^D as end of input only at the start of a line; after a
// partial line, written now or by an earlier call, ^D just hands the line
// over, so a second one follows. Data that already ends input gets none:
// another ^D would reach the shell behind the program.
func terminalEOF(pending bool, data []byte) []byte {
	pending, ended := terminalLine(pending, data)
	if ended && len(data) > 0 {
		return data
	}
	out := append([]byte(nil), data...)
	if pending {
		out = append(out, 4)
	}
	return append(out, 4)
}

// writeInput writes data within sandboxInputWriteTimeout when the stream
// supports deadlines (pipes and terminals do).
func writeInput(w interface{ Write([]byte) (int, error) }, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if d, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if d.SetWriteDeadline(time.Now().Add(sandboxInputWriteTimeout)) == nil {
			defer func() { _ = d.SetWriteDeadline(time.Time{}) }()
		}
	}
	_, err := w.Write(data)
	return err
}

func inputError(handle string, err error) error {
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		return sandboxError(sandboxproto.CodeInputStalled, "command %s is not reading its input; some of it may have been written", handle)
	case errors.Is(err, os.ErrClosed), errors.Is(err, syscall.EPIPE), errors.Is(err, syscall.EIO):
		return sandboxError(sandboxproto.CodeCommandEnded, "command %s no longer reads its input", handle)
	}
	return sandboxError(sandboxproto.CodeInternal, "write to %s: %s", handle, err.Error())
}

func newCommandHandle() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "c" + hex.EncodeToString(b[:])
}

// outputRing keeps the last limit bytes written to it. Offsets count every
// byte ever written, so a reader resumes where it left off.
type outputRing struct {
	mu    sync.Mutex
	buf   []byte
	start int64
	limit int
}

func newOutputRing(limit int64) *outputRing { return &outputRing{limit: int(limit)} }

func (r *outputRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if drop := len(r.buf) - r.limit; drop > 0 {
		r.buf = append([]byte(nil), r.buf[drop:]...)
		r.start += int64(drop)
	}
	return len(p), nil
}

// read returns at most limit bytes from offset, the offset after them, and
// whether output before the oldest kept byte was asked for.
func (r *outputRing) read(offset, limit int64) ([]byte, int64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	end := r.start + int64(len(r.buf))
	skipped := offset < r.start
	offset = min(max(offset, r.start), end)
	stop := min(end, offset+limit)
	data := append([]byte{}, r.buf[offset-r.start:stop-r.start]...)
	return data, stop, skipped
}
