package main

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// defaultDeployStreamIdleTimeout bounds how long a deploy's log stream may
// send nothing at all. The server writes a heartbeat every 15 s while it
// tails, so three missed heartbeats mean the connection is gone. Without this
// a silently dropped connection was only noticed by TCP keepalive (15 s idle
// + 9 probes 15 s apart, about 150 s): an MCP redeploy on 2026-11-24 sat 154 s
// after its deployment went live.
const defaultDeployStreamIdleTimeout = 45 * time.Second

var errDeployStreamIdle = errors.New("no data or heartbeat from the server")

// deployStreamIdleError is the read error after the watchdog closed the
// stream. It reads as the idle bound and wraps both errDeployStreamIdle and
// the transport error the close produced.
type deployStreamIdleError struct {
	idle time.Duration
	err  error
}

func (e *deployStreamIdleError) Error() string {
	return fmt.Sprintf("%v for %s", errDeployStreamIdle, e.idle)
}

func (e *deployStreamIdleError) Unwrap() []error { return []error{errDeployStreamIdle, e.err} }

// idleWatchdogReader closes rc when a single Read waits longer than idle for
// the server. The clock runs only while Read is blocked on the connection, so
// a consumer that is slow to ask for more data is not mistaken for a silent
// server.
type idleWatchdogReader struct {
	rc   io.ReadCloser
	idle time.Duration

	mu     sync.Mutex
	timer  *time.Timer
	fired  bool
	closed bool
}

func newIdleWatchdogReader(rc io.ReadCloser, idle time.Duration) *idleWatchdogReader {
	return &idleWatchdogReader{rc: rc, idle: idle}
}

func (w *idleWatchdogReader) arm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.fired {
		return
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(w.idle, w.fire)
		return
	}
	w.timer.Reset(w.idle)
}

func (w *idleWatchdogReader) disarm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
}

func (w *idleWatchdogReader) fire() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.fired = true
	w.mu.Unlock()
	_ = w.rc.Close()
}

func (w *idleWatchdogReader) Read(p []byte) (int, error) {
	w.arm()
	n, err := w.rc.Read(p)
	w.disarm()
	if err != nil {
		w.mu.Lock()
		fired := w.fired
		w.mu.Unlock()
		if fired {
			err = &deployStreamIdleError{idle: w.idle, err: err}
		}
	}
	return n, err
}

func (w *idleWatchdogReader) Close() error {
	w.mu.Lock()
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	return w.rc.Close()
}
