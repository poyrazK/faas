//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// StopSignalHandler (M-2 / ADR-138 §Decision 1, closes issue #474)
// is the PID 1 signal-handling layer guest-init installs between
// the liveness listener and the supervisor spawn. It owns three
// concerns:
//
//  1. The graceful-stop sequence (ADR-138 §Decision 1): a
//     SIGTERM (default fallback) or the customer-declared
//     AppManifest.StopSignal triggers sup.Stop(ctx), which sends
//     the same signal to the tracked workload child, waits up
//     to AppManifest.StopGracePeriod (or the per-plan cap, per
//     commit 10), then escalates to SIGKILL via the supervisor.
//  2. The SIGCHLD reaper loop: PID 1's PID-1 obligation. Without
//     a reaper, every forked child becomes a zombie that holds
//     its PID forever — which on a fresh guest with a single PID
//     namespace means the second cold boot of any short-lived
//     child would fail with EAGAIN from fork(). The loop is
//     syscall.Wait4(-1, …) wrapped in a no-wait loop so it
//     reaps every ready zombie and only blocks when the queue
//     is empty.
//  3. The signal-forwarding contract: every signal the customer
//     declared via AppManifest.StopSignal that guest-init
//     receives is forwarded to the tracked workload via the
//     supervisor's kill-by-PID helper. This is what makes
//     STOPSIGNAL=SIGUSR1 in the image-config actually deliver
//     SIGUSR1 to the customer's PID 1 — guest-init is the bridge
//     between host-vsock (which can't reach the workload's PID
//     directly) and the workload's signal handler.
//
// All three concerns live in the SAME goroutine that does the
// blocking syscall.Wait4 — we multiplex on a channel rather
// than spinning two goroutines, because the reaper is the
// steady-state idle activity and signal delivery is rare. The
// design matches systemd's PID 1 implementation: one
// sigchldnoexit loop, multiple signal channels.
//
// The handler returns when the supervisor finishes (clean exit,
// exhausted restarts, or forced SIGKILL escalation). main_linux.go
// treats that return as "the workload is done" and powers off the
// VM with the captured exit code, mirroring the existing runWorkloads
// pattern.

const (
	// defaultStopSignal is the canonical POSIX stop signal used
	// when AppManifest.StopSignal is empty. Mirrors Docker /
	// Kubernetes default behaviour and the OCI image-config spec.
	defaultStopSignal = syscall.SIGTERM

	// reapLoopInterval bounds the SIGCHLD wait4 poll cycle when
	// no children exist. Without this bound, syscall.Wait4 would
	// block forever and a child exiting after the supervisor
	// finished would stay a zombie until guest-init exits. 100 ms
	// is well below the §4.3 idle reaper window (seconds) so no
	// zombie accumulates long enough to surface as a customer
	// issue.
	reapLoopInterval = 100 * time.Millisecond
)

// stopSignal parses the AppManifest.StopSignal string into a
// syscall.Signal. Empty / unknown values fall back to SIGTERM so
// the customer contract is "always stoppable" rather than "fail
// closed on a typo". Returns the parsed signal + a stable name
// for the slog fields.
func parseStopSignal(s string) syscall.Signal {
	s = strings.TrimSpace(strings.ToUpper(s))
	switch s {
	case "":
		return defaultStopSignal
	case "SIGTERM", "TERM", "15":
		return syscall.SIGTERM
	case "SIGINT", "INT", "2":
		return syscall.SIGINT
	case "SIGQUIT", "QUIT", "3":
		return syscall.SIGQUIT
	case "SIGHUP", "HUP", "1":
		return syscall.SIGHUP
	case "SIGUSR1", "USR1", "10":
		return syscall.SIGUSR1
	case "SIGUSR2", "USR2", "12":
		return syscall.SIGUSR2
	default:
		return defaultStopSignal
	}
}

// StopSignalFromManifest is the test-visible wrapper around
// parseStopSignal. guest/init is a package-main so unit tests in
// the same package can call parseStopSignal directly, but tests
// outside guest/init need the exported entrypoint. Kept tiny so
// the test surface mirrors the production surface exactly.
func StopSignalFromManifest(s string) syscall.Signal { return parseStopSignal(s) }

// runSignalHandlers is the PID 1 entry point. It installs a
// process-wide signal handler that fans out:
//
//   - SIGTERM → sup.Stop(ctx) with the customer's grace window.
//   - SIGCHLD → reap one child via syscall.Wait4(-1, WNOHANG).
//   - AppManifest.StopSignal (parsed at boot) → sup.Stop(ctx).
//
// The function returns when the supervisor's Run() finishes.
// Errors from the reaper are swallowed (warn-logged) because a
// transient reap failure is recoverable on the next iteration.
//
// ctx carries the parent boot context — cancellation propagates
// to sup.Stop's grace timer so an external shutdown (vsock
// resume hook timeout, schedd eviction) short-circuits the grace
// window instead of waiting it out.
func runSignalHandlers(ctx context.Context, manifest api.AppManifest, sup *Supervisor, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}

	stopSignal := parseStopSignal(manifest.StopSignal)
	grace := manifest.StopGracePeriod
	if grace <= 0 {
		grace = MaxAppManifestStopGracePeriodFallback
	}

	// supStopOnce guarantees sup.Stop runs exactly once even if
	// the customer declares two signals in the manifest (rare,
	// but a manifest validation bug shouldn't be able to leak two
	// grace timers into the same VM lifecycle).
	var supStopOnce sync.Once
	supStop := func(reason string) {
		supStopOnce.Do(func() {
			stopCtx, cancel := context.WithTimeout(ctx, grace)
			defer cancel()
			log.Info("guest-init: stop signal received; forwarding to supervisor",
				"signal", stopSignal.String(), "grace", grace.String(), "reason", reason)
			// sup.Stop is best-effort — the timeout-driven
			// SIGKILL escalation inside Supervisor.Stop
			// guarantees the customer workload exits within
			// `grace`, regardless of which error surface.
			// Capture + log so a typed mismatch surfaces in the
			// guest-init slog stream without aborting the
			// signal-handler loop (the loop must stay alive
			// for SIGCHLD reaping through the stop sequence).
			if stopErr := sup.Stop(stopCtx, stopSignal, grace); stopErr != nil {
				log.Warn("guest-init: supervisor stop returned err", "err", stopErr)
			}
		})
	}

	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh,
		defaultStopSignal,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGHUP,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
		syscall.SIGCHLD,
	)

	// runErr holds the supervisor's terminal error so the
	// caller can stamp the exit code onto the poweroff cycle.
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- sup.Run()
	}()

	reapTick := time.NewTicker(reapLoopInterval)
	defer reapTick.Stop()

	for {
		select {
		case <-ctx.Done():
			supStop("ctx-cancel")
			return ctx.Err()
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGCHLD:
				reapOne(log)
			case stopSignal:
				supStop("manifest-stop-signal")
				if err := <-runErrCh; err != nil {
					return err
				}
				return nil
			default:
				// Forward to the supervisor if it matches a
				// configured forwarding target; otherwise log
				// and ignore. The customer's STOPSIGNAL is the
				// authoritative signal — anything else arriving
				// here (SIGINT, SIGQUIT, etc.) is honoured by
				// the kernel default for guest-init (terminate
				// = VM exit) and forwarded verbatim to the
				// workload so it sees the same intent.
				if ss, ok := sig.(syscall.Signal); ok {
					if err := sup.ForwardSignal(ss); err != nil && !errors.Is(err, os.ErrProcessDone) {
						log.Warn("guest-init: forward signal failed",
							"signal", sig.String(), "err", err)
					}
				}
				if sig == defaultStopSignal {
					supStop("forwarded-default-stop")
					if err := <-runErrCh; err != nil {
						return err
					}
					return nil
				}
			}
		case <-reapTick.C:
			reapOne(log)
		case err := <-runErrCh:
			// Supervisor exited cleanly or exhausted restarts
			// without an external stop signal. Treat as natural
			// termination.
			return err
		}
	}
}

// reapOne drains ONE ready zombie via syscall.Wait4 with
// WNOHANG. A return value of -1 with errno=ECHILD means "no
// children" — that's the steady state after the supervisor's
// fork() returns; we tolerate it silently because the tick
// continues. Any other errno is warn-logged so a real reap
// failure surfaces in the boot log without aborting guest-init.
//
// We use the raw syscall.Wait4 rather than syscall.Waitid so
// this code can run on Linux 4.x guest kernels without depending
// on the Waitid wrapper (some embedded kernels disable it).
func reapOne(log *slog.Logger) {
	var status syscall.WaitStatus
	pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
	if err != nil {
		if errors.Is(err, syscall.ECHILD) {
			return // no children to reap — steady state
		}
		if log != nil {
			log.Warn("guest-init: reap failed", "err", err)
		}
		return
	}
	if pid <= 0 {
		return // no child was ready — also steady state
	}
	if log != nil {
		log.Debug("guest-init: reaped child",
			"pid", pid,
			"exited", status.Exited(),
			"signaled", status.Signaled(),
			"signal", status.Signal(),
			"exit_status", status.ExitStatus())
	}
}

// MaxAppManifestStopGracePeriodFallback is the default grace
// window used when AppManifest.StopGracePeriod is unset (the
// common case today). Matches the gross cap from commit 5 and
// is tightened per-plan in commit 10. Exposed as a constant so
// the test surface pins the same value the production boot
// path uses.
const MaxAppManifestStopGracePeriodFallback = 30 * time.Second
