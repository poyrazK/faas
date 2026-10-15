package main

// Build-tag-free half of interactive image runs (ADR-1330): how the session
// maps onto the job supervisor's signals and exit receipt. The listener and
// stdio live in job_interactive_linux.go.

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

const (
	// jobAttachTimeoutExitCode reports a run whose client never attached.
	jobAttachTimeoutExitCode = 125
	// jobSessionDeadlineSkew ignores a host "disconnect" reported at the
	// session time limit: the session's input reader shares that deadline,
	// and the supervisor's own task timeout classifies the outcome.
	jobSessionDeadlineSkew = time.Second
)

// forwardJobSessionSignals passes host cancellation to the supervisor and
// turns a client disconnect into SIGHUP for the command's process group, as
// closing a terminal does. The session time limit is not a signal here: the
// supervisor enforces the task timeout itself.
func forwardJobSessionSignals(ctx context.Context, from <-chan os.Signal, to chan<- os.Signal, done <-chan struct{}) {
	ctxDone := ctx.Done()
	for {
		select {
		case sig := <-from:
			select {
			case to <- sig:
			default:
			}
		case <-ctxDone:
			ctxDone = nil
			if jobSessionHostLeft(ctx) {
				select {
				case to <- syscall.SIGHUP:
				default:
				}
			}
		case <-done:
			return
		}
	}
}

func jobSessionHostLeft(ctx context.Context) bool {
	if !errors.Is(context.Cause(ctx), apptaskproto.ErrHostDisconnected) {
		return false
	}
	deadline, ok := ctx.Deadline()
	return !ok || time.Now().Before(deadline.Add(-jobSessionDeadlineSkew))
}

// jobSessionResult is the terminal result the client sees; it mirrors the
// job exit receipt.
func jobSessionResult(p JobExitPayload) apptaskproto.Result {
	code := int(p.ExitCode)
	failed := func(failureCode, message string) apptaskproto.Result {
		return apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &code, FailureCode: failureCode, FailureMessage: message}
	}
	switch p.ErrorClass {
	case "succeeded":
		return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &code}
	case "timeout":
		result := failed("timeout", "the task reached its timeout")
		result.Status = apptaskproto.StatusTimedOut
		return result
	case "cancelled":
		return failed("cancelled", "the run was cancelled")
	case "oom":
		return failed("oom", "the command ran out of memory")
	case "infra":
		return failed("command_start_failed", "the command could not be run")
	default:
		return failed("command_failed", "command exited unsuccessfully")
	}
}
