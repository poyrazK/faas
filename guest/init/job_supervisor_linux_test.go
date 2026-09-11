//go:build linux

package main

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestSuperviseJobCommandCapturesExit(t *testing.T) {
	payload := superviseJobCommand(JobManifest{
		Command:        []string{"/bin/sh", "-c", "exit 7"},
		TaskTimeoutSec: 5,
		LeaseToken:     "lease-1",
	}, nil, 50*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if payload.ExitCode != 7 || payload.ErrorClass != "failed" || payload.Signal != 0 {
		t.Fatalf("payload = %+v, want failed exit 7", payload)
	}
	if payload.LeaseToken != "lease-1" || payload.FinishedAtUnixNano == 0 {
		t.Fatalf("payload metadata = %+v", payload)
	}
}

func TestSuperviseJobCommandEnforcesTimeout(t *testing.T) {
	payload := superviseJobCommand(JobManifest{
		Command:        []string{"/bin/sh", "-c", "trap '' TERM; sleep 30"},
		TaskTimeoutSec: 1,
		LeaseToken:     "lease-timeout",
	}, nil, 25*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if payload.ExitCode != 124 || payload.ErrorClass != "timeout" {
		t.Fatalf("payload = %+v, want timeout exit 124", payload)
	}
}
