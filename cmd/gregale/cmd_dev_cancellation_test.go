package main

import (
	"context"
	"crypto/sha256"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunDevWatchLoopCancellationDuringPendingSetup(t *testing.T) {
	for _, stage := range []string{"resolve", "refresh"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			failed := make(chan struct{})
			var waitCalls atomic.Int64
			var refreshCalls, changeCalls int
			exit := runDevWatchLoop(ctx, "project", [sha256.Size]byte{}, devSourceConfig{shape: shapeApp}, false, devLoopOps{
				deploy: func(context.Context, devSourceConfig, func(string)) int { return 7 },
				waitForChange: func(watchCtx context.Context, _ string, prior [sha256.Size]byte) ([sha256.Size]byte, error) {
					if waitCalls.Add(1) == 1 {
						select {
						case <-failed:
							prior[0] = 1
							return prior, nil
						case <-watchCtx.Done():
							return prior, watchCtx.Err()
						}
					}
					<-watchCtx.Done()
					return prior, watchCtx.Err()
				},
				resolve: func(string) (devSourceConfig, error) {
					if stage == "resolve" {
						cancel()
					}
					return devSourceConfig{shape: shapeApp}, nil
				},
				refresh: func(devSourceConfig) error {
					refreshCalls++
					if stage == "refresh" {
						cancel()
					}
					return nil
				},
				onDeployFailed: func(int) { close(failed) },
				onChange:       func() { changeCalls++ },
			})
			if exit != 0 {
				t.Fatalf("exit = %d, want cancellation success", exit)
			}
			wantRefresh := 0
			if stage == "refresh" {
				wantRefresh = 1
			}
			if refreshCalls != wantRefresh {
				t.Fatalf("refresh after cancellation: got %d calls, want %d", refreshCalls, wantRefresh)
			}
			if changeCalls != 0 {
				t.Fatalf("source change announced after cancellation: %d", changeCalls)
			}
		})
	}
}
