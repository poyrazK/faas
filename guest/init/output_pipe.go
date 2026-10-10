package main

import (
	"io"
	"os"
	"sync"
	"time"
)

// workloadOutputPipe carries a workload's stdout and stderr to guest-init's
// writers through a pipe the workload's own user owns.
//
// production-us hunt #8: official images commonly log by opening /dev/stdout
// or /dev/stderr (nginx symlinks /var/log/nginx/error.log to /dev/stderr).
// Reopening /proc/self/fd/2 checks the pipe inode's owner, and the pipe
// os/exec creates belongs to guest-init (root), so nginx running as uid 101
// failed with EACCES and crash-looped. runc chowns container stdio for the
// same reason; guest-init now does too.
type workloadOutputPipe struct {
	w         *os.File
	done      chan struct{}
	closeOnce sync.Once
}

// workloadOutputDrainTimeout bounds how long a finished workload's output is
// drained: a backgrounded grandchild may keep the pipe open indefinitely.
const workloadOutputDrainTimeout = 2 * time.Second

func newWorkloadOutputPipe(dst io.Writer, uid, gid int) (*workloadOutputPipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	if uid != 0 || gid != 0 {
		if err := w.Chown(uid, gid); err != nil {
			_ = r.Close()
			_ = w.Close()
			return nil, err
		}
	}
	p := &workloadOutputPipe{w: w, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		_, _ = io.Copy(dst, r)
		_ = r.Close()
	}()
	return p, nil
}

// closeWriter releases guest-init's copy of the write end; call it once the
// workload has started (or failed to) so the reader sees EOF when it exits.
func (p *workloadOutputPipe) closeWriter() {
	p.closeOnce.Do(func() { _ = p.w.Close() })
}

// finish closes the write end and waits, bounded, for the output to drain.
func (p *workloadOutputPipe) finish() {
	p.closeWriter()
	select {
	case <-p.done:
	case <-time.After(workloadOutputDrainTimeout):
	}
}
