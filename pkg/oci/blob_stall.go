package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

var (
	errBlobStalled         = errors.New("oci: registry blob transfer stalled")
	errBlobTransferTimeout = errors.New("oci: registry blob transfer exceeded its time limit")
)

// stallGuardBody cancels a blob transfer when one body read waits longer than
// timeout for data. Only time spent blocked in Read counts: a consumer that
// extracts slowly between reads is not a stalled registry.
type stallGuardBody struct {
	body    io.ReadCloser
	ctx     context.Context
	timeout time.Duration
	timer   *time.Timer
	release func()
}

func newStallGuard(body io.ReadCloser, ctx context.Context, timeout time.Duration, stall context.CancelCauseFunc, release func()) io.ReadCloser {
	g := &stallGuardBody{body: body, ctx: ctx, timeout: timeout, release: release}
	g.timer = time.AfterFunc(timeout, func() { stall(errBlobStalled) })
	g.timer.Stop()
	return g
}

func (g *stallGuardBody) Read(p []byte) (int, error) {
	g.timer.Reset(g.timeout)
	n, err := g.body.Read(p)
	g.timer.Stop()
	if err != nil && !errors.Is(err, io.EOF) {
		if cause := context.Cause(g.ctx); errors.Is(cause, errBlobStalled) || errors.Is(cause, errBlobTransferTimeout) {
			return n, fmt.Errorf("%w after %s: %w", cause, g.timeout, err)
		}
	}
	return n, err
}

func (g *stallGuardBody) Close() error {
	g.timer.Stop()
	err := g.body.Close()
	g.release()
	return err
}
