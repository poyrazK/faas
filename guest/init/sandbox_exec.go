package main

import (
	"context"
	"io"
	"path"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// sandboxDrainAfterExit is how long a command's output may keep arriving
// after it exits, from a grandchild that still holds its streams.
const sandboxDrainAfterExit = 500 * time.Millisecond

// commandSpec is the command req asks for: its user, environment and
// directory (relative directories are under the workspace).
func (s *sandboxServer) commandSpec(req sandboxproto.Request) sandboxCommandSpec {
	root := req.RunAs == sandboxproto.RunAsRoot
	home, user := s.home, "app"
	if root {
		home, user = "/root", "root"
	}
	dir := req.Workdir
	switch {
	case dir == "":
		dir = s.workspace
	case !path.IsAbs(dir):
		dir = path.Join(s.workspace, dir)
	}
	return sandboxCommandSpec{argv: req.Argv, env: sandboxEnv(home, user, req.TTY, req.Env), dir: dir, root: root,
		tty: req.TTY, rows: req.Rows, cols: req.Cols}
}

// exec runs a command in the foreground, streaming its output until it
// ends, or starts it in the background and returns its handle.
func (s *sandboxServer) exec(ctx context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	spec := s.commandSpec(req)
	if req.Background {
		return s.commands.start(spec, req)
	}
	started := s.now()
	proc, err := s.runner.start(spec)
	if err != nil {
		return nil, sandboxError(sandboxproto.CodeStartFailed, "%s", err.Error())
	}
	budget := &sandboxBudget{remaining: req.MaxOutputBytes}
	var pumps sync.WaitGroup
	pump := func(r io.Reader, w io.Writer) {
		pumps.Add(1)
		go func() {
			defer pumps.Done()
			copyBudgeted(w, r, budget)
		}()
	}
	pump(proc.stdout(), call.Stdout())
	if stderr := proc.stderr(); stderr != nil {
		pump(stderr, call.Stderr())
	}
	exit, timedOut, killed := s.supervise(ctx, req, proc, call.Inbound())
	drained := make(chan struct{})
	go func() { pumps.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(sandboxDrainAfterExit):
		proc.closeOutput()
		<-drained
	}
	proc.closeOutput()
	return sandboxproto.ExecResult{ExitCode: exit.code, Signal: exit.signal, TimedOut: timedOut, Killed: killed,
		OutputTruncated: budget.truncated(), WallTimeMillis: s.now().Sub(started).Milliseconds()}, nil
}

// supervise forwards the host's stdin, resize and kill frames until the
// command ends. A timeout stops it with SIGTERM, then SIGKILL after
// StopGrace; a kill frame, a closed connection or an ended session kills it.
func (s *sandboxServer) supervise(ctx context.Context, req sandboxproto.Request, proc sandboxProcess,
	inbound <-chan sandboxproto.Frame) (exit sandboxExit, timedOut, killed bool) {
	exited := make(chan sandboxExit, 1)
	go func() { exited <- proc.wait() }()
	timeout := time.NewTimer(time.Duration(req.TimeoutMillis) * time.Millisecond)
	defer timeout.Stop()
	var escalate <-chan time.Time
	kill := func() {
		killed = true
		_ = proc.signal(syscall.SIGKILL)
	}
	for {
		select {
		case exit = <-exited:
			// A terminal's input is its output too; closeOutput closes it
			// once the output is drained.
			if !req.TTY {
				_ = proc.stdin().Close()
			}
			return exit, timedOut, killed
		case f, ok := <-inbound:
			if !ok {
				inbound = nil
				kill()
				continue
			}
			if f.Type == sandboxproto.FrameKill {
				kill()
				continue
			}
			forwardInput(req.TTY, proc, f)
		case <-timeout.C:
			timedOut = true
			_ = proc.signal(syscall.SIGTERM)
			escalate = time.After(sandboxproto.StopGrace)
		case <-escalate:
			_ = proc.signal(syscall.SIGKILL)
		case <-ctx.Done():
			kill()
			ctx = context.Background()
		}
	}
}

// forwardInput delivers one stdin, end-of-input or resize frame. A terminal
// ends its input with ^D, since closing it would also close the output.
func forwardInput(tty bool, proc sandboxProcess, f sandboxproto.Frame) {
	switch f.Type {
	case sandboxproto.FrameStdin:
		_, _ = proc.stdin().Write(f.Body)
	case sandboxproto.FrameStdinEOF:
		if tty {
			_, _ = proc.stdin().Write([]byte{4})
		} else {
			_ = proc.stdin().Close()
		}
	case sandboxproto.FrameResize:
		var size sandboxproto.ResizeFrame
		if sandboxproto.DecodeStrict(f.Body, &size) == nil && size.Rows > 0 && size.Cols > 0 {
			_ = proc.resize(size.Rows, size.Cols)
		}
	}
}

// sandboxBudget is a command's output allowance, shared by its streams.
type sandboxBudget struct {
	mu        sync.Mutex
	remaining int64
	dropped   bool
}

// take returns how many of n bytes may still be forwarded.
func (b *sandboxBudget) take(n int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if int64(n) > b.remaining {
		n = int(b.remaining)
		b.dropped = true
	}
	b.remaining -= int64(n)
	return n
}

func (b *sandboxBudget) truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// copyBudgeted forwards r to w within the budget and drains the rest, so a
// command never blocks on output the host will not see.
func copyBudgeted(w io.Writer, r io.Reader, budget *sandboxBudget) {
	buf := make([]byte, 32<<10)
	forward := true
	for {
		n, err := r.Read(buf)
		if n > 0 && forward {
			if keep := budget.take(n); keep > 0 {
				if _, werr := w.Write(buf[:keep]); werr != nil {
					forward = false
				}
			}
		}
		if err != nil {
			return
		}
	}
}
