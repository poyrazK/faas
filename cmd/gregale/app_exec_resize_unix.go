//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// watchTerminalResize calls onResize for every SIGWINCH until ctx ends or
// the returned stop function runs.
func watchTerminalResize(ctx context.Context, onResize func()) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-signals:
				onResize()
			}
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}
