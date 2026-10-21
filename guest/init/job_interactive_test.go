package main

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestJobSessionResultMirrorsExitReceipt(t *testing.T) {
	for _, tc := range []struct {
		class       string
		exit        int32
		status      apptaskproto.Status
		failureCode string
	}{
		{class: "succeeded", exit: 0, status: apptaskproto.StatusSucceeded},
		{class: "failed", exit: 3, status: apptaskproto.StatusFailed, failureCode: "command_failed"},
		{class: "timeout", exit: 124, status: apptaskproto.StatusTimedOut, failureCode: "timeout"},
		{class: "cancelled", exit: 129, status: apptaskproto.StatusFailed, failureCode: "cancelled"},
		{class: "oom", exit: 137, status: apptaskproto.StatusFailed, failureCode: "oom"},
		{class: "infra", exit: 127, status: apptaskproto.StatusFailed, failureCode: "command_start_failed"},
	} {
		t.Run(tc.class, func(t *testing.T) {
			got := jobSessionResult(JobExitPayload{ExitCode: tc.exit, ErrorClass: tc.class})
			if got.Status != tc.status || got.FailureCode != tc.failureCode || got.ExitCode == nil || *got.ExitCode != int(tc.exit) {
				t.Fatalf("result = %+v (exit %v), want status %q failure %q exit %d", got, got.ExitCode, tc.status, tc.failureCode, tc.exit)
			}
			if err := got.Validate(0); err != nil {
				t.Fatalf("result does not validate: %v", err)
			}
		})
	}
}

func TestJobSessionHostLeft(t *testing.T) {
	disconnected := func(deadline time.Duration) context.Context {
		ctx, cancel := context.WithCancelCause(context.Background())
		var timed context.Context = ctx
		if deadline != 0 {
			var stop context.CancelFunc
			timed, stop = context.WithTimeout(ctx, deadline)
			t.Cleanup(stop)
		}
		cancel(apptaskproto.ErrHostDisconnected)
		return timed
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{name: "disconnect without deadline", ctx: disconnected(0), want: true},
		{name: "disconnect well before deadline", ctx: disconnected(time.Hour), want: true},
		{name: "disconnect at the deadline", ctx: disconnected(jobSessionDeadlineSkew / 2), want: false},
		{name: "ordinary cancellation", ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}(), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := jobSessionHostLeft(tc.ctx); got != tc.want {
				t.Fatalf("jobSessionHostLeft = %v, want %v (cause %v)", got, tc.want, context.Cause(tc.ctx))
			}
		})
	}
}

func TestForwardJobSessionSignals(t *testing.T) {
	t.Run("host cancellation is forwarded", func(t *testing.T) {
		from, to, done := make(chan os.Signal, 1), make(chan os.Signal, 1), make(chan struct{})
		defer close(done)
		go forwardJobSessionSignals(context.Background(), from, to, done)
		from <- syscall.SIGTERM
		if got := receiveSignal(t, to); got != syscall.SIGTERM {
			t.Fatalf("forwarded %v, want SIGTERM", got)
		}
	})
	t.Run("client disconnect hangs up", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		to, done := make(chan os.Signal, 1), make(chan struct{})
		defer close(done)
		go forwardJobSessionSignals(ctx, nil, to, done)
		cancel(apptaskproto.ErrHostDisconnected)
		if got := receiveSignal(t, to); got != syscall.SIGHUP {
			t.Fatalf("forwarded %v, want SIGHUP", got)
		}
	})
	t.Run("session end without disconnect sends nothing", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		to, done := make(chan os.Signal, 1), make(chan struct{})
		defer close(done)
		go forwardJobSessionSignals(ctx, nil, to, done)
		cancel(errors.New("session finished"))
		select {
		case sig := <-to:
			t.Fatalf("forwarded %v after an ordinary session end", sig)
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func receiveSignal(t *testing.T, ch <-chan os.Signal) os.Signal {
	t.Helper()
	select {
	case sig := <-ch:
		return sig
	case <-time.After(5 * time.Second):
		t.Fatal("no signal forwarded")
		return nil
	}
}
