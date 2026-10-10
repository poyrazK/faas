//go:build linux

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// startTraceBridge serves the guest-local OTLP endpoint for apps that opted
// into tracing (ADR-934). A bind failure (the app owns 4318) only disables
// tracing for this instance.
func startTraceBridge(cfg *api.TracingConfig, log *slog.Logger) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	ln, err := net.Listen("tcp4", api.TraceLocalListen)
	if err != nil {
		return fmt.Errorf("trace bridge listen: %w", err)
	}
	bridge := newTraceBridge(sendTraceFrame)
	bridge.observe = newTraceOutcomeLogger(log)
	srv := &http.Server{
		Handler:           bridge.handler(),
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       api.TraceTransportTimeout,
		WriteTimeout:      2 * api.TraceTransportTimeout,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Warn("trace bridge stopped", "err", err)
		}
	}()
	log.Info("trace bridge started", "endpoint", api.TraceLocalEndpoint+api.TraceLocalTracesPath)
	return nil
}

// sendTraceFrame forwards one encoded frame over the dedicated vsock port and
// returns the host's ack byte.
func sendTraceFrame(ctx context.Context, frame []byte) (byte, error) {
	deadline := time.Now().Add(api.TraceTransportTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn, err := dialRuntimeConfigVsock(api.TraceVsockPort, time.Until(deadline))
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err := conn.Write(frame); err != nil {
		return 0, err
	}
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return 0, err
	}
	return ack[0], nil
}

// newTraceOutcomeLogger logs the first delivered export once and any
// delivery problem at most every 30 seconds, so a broken host path is visible
// on the console without flooding it.
func newTraceOutcomeLogger(log *slog.Logger) func(string, error) {
	var mu sync.Mutex
	var delivered bool
	var lastProblem time.Time
	return func(outcome string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if outcome == "accepted" {
			if !delivered {
				delivered = true
				log.Info("trace bridge delivered first export")
			}
			return
		}
		if time.Since(lastProblem) < 30*time.Second {
			return
		}
		lastProblem = time.Now()
		log.Warn("trace bridge export not delivered", "outcome", outcome, "err", err)
	}
}
