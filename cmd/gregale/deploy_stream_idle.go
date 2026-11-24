package main

import (
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// deployStreamIdleTimeout bounds how long a deploy's log stream may send
// nothing at all. The server writes a heartbeat every 15 s while it tails, so
// three missed heartbeats mean the connection is gone. Without this a
// silently dropped connection was only noticed by TCP keepalive (15 s idle +
// 9 probes 15 s apart, about 150 s): an MCP redeploy on 2026-11-24 sat 154 s
// after its deployment went live. Package-level so tests can shrink it.
var deployStreamIdleTimeout = 45 * time.Second

var errDeployStreamIdle = errors.New("no data or heartbeat from the server")

// idleWatchdogReader closes rc when no Read has returned data for idle, which
// unblocks a Read stuck on a dead connection with errDeployStreamIdle.
type idleWatchdogReader struct {
	rc    io.ReadCloser
	idle  time.Duration
	timer *time.Timer
	fired atomic.Bool
}

func newIdleWatchdogReader(rc io.ReadCloser, idle time.Duration) *idleWatchdogReader {
	w := &idleWatchdogReader{rc: rc, idle: idle}
	w.timer = time.AfterFunc(idle, func() {
		w.fired.Store(true)
		_ = rc.Close()
	})
	return w
}

func (w *idleWatchdogReader) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	if n > 0 && !w.fired.Load() {
		w.timer.Reset(w.idle)
	}
	if err != nil && w.fired.Load() {
		err = errDeployStreamIdle
	}
	return n, err
}

func (w *idleWatchdogReader) Close() error {
	w.timer.Stop()
	return w.rc.Close()
}
