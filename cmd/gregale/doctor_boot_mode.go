package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A worker or job serves no HTTP, so `--boot` judges it as Gregale's
// characterization does (ADR-051, pkg/fcvm characterizationReadinessMismatch):
// with no listener required, a process still running when the observation
// window (api.CharacterizationDeadline) ends is a worker, one that exited 0
// is a finished job, and any other exit is a startup failure.

// doctorBootModes are the --execution-mode values `doctor --boot` accepts.
var doctorBootModes = []string{api.ExecutionModeRequest, api.ExecutionModeService, api.ExecutionModeWorker, api.ExecutionModeJob}

// doctorBootNoListener reports whether mode serves no HTTP on $PORT.
func doctorBootNoListener(mode string) bool {
	mode = strings.TrimSpace(mode)
	return mode == api.ExecutionModeWorker || mode == api.ExecutionModeJob
}

// doctorBootMode is the mode the boot judges: --execution-mode (or the
// deploy's gregale.yaml), else an existing worker or job app's observed
// class. An explicit health path asks for an HTTP check and keeps it.
func doctorBootMode(explicit, healthPath string, app doctorBootApp) string {
	if mode := strings.TrimSpace(explicit); mode != "" {
		return mode
	}
	if healthPath == "" && doctorBootNoListener(app.workloadClass) {
		_, _ = fmt.Fprintf(osStderr, "App %s runs as a %s; checking the image as one (pass --execution-mode to override).\n", app.slug, app.workloadClass)
		return app.workloadClass
	}
	return ""
}

// checkNoListener watches a worker or job for the observation window.
func (b doctorBoot) checkNoListener(ctx context.Context) []doctorCheck {
	deadline := b.started.Add(doctorBootStayUp)
	for {
		st, err := b.docker.state(ctx, b.name)
		if err != nil {
			return []doctorCheck{{Name: "boot-start", Status: "error", Code: "boot_inspect_failed", Hint: err.Error()}}
		}
		if !st.running {
			return []doctorCheck{b.modeExitedCheck(ctx, st)}
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return []doctorCheck{
				{Name: "boot-start", Status: "ok", Detail: "container running" + b.memoryNote()},
				{Name: "boot-stay-up", Status: "skipped", Reason: "interrupted"},
			}
		case <-time.After(doctorBootPoll):
		}
	}
	checks := []doctorCheck{{Name: "boot-start", Status: "ok", Detail: "container running" + b.memoryNote()}}
	if b.opts.imageHealthcheck != nil && b.opts.mode == api.ExecutionModeWorker {
		checks = append(checks, b.imageHealth(ctx))
	}
	detail := fmt.Sprintf("still running after %s, as a worker must", bootSeconds(doctorBootStayUp))
	if b.opts.mode == api.ExecutionModeJob {
		detail = fmt.Sprintf("still running after %s; a job may run longer, and Gregale counts it done when it exits 0", bootSeconds(doctorBootStayUp))
	}
	return append(checks, doctorCheck{Name: "boot-stay-up", Status: "ok", Detail: detail})
}

// doctorBootRealRunNote warns that a worker or job does its real work in the
// boot, with whatever env it was given.
func doctorBootRealRunNote(mode string) {
	_, _ = fmt.Fprintf(osStderr, "The %s runs for real on this machine for up to %s, with the env you passed; point it at test resources, not production.\n", mode, bootSeconds(doctorBootStayUp))
}

// modeExitedCheck judges a worker or job that exited within the window.
func (b doctorBoot) modeExitedCheck(ctx context.Context, st doctorBootState) doctorCheck {
	after := bootSeconds(time.Since(b.started))
	if st.exitCode == 0 && !st.oomKilled {
		if b.opts.mode == api.ExecutionModeJob {
			return doctorCheck{Name: "boot-start", Status: "ok", Detail: fmt.Sprintf("completed with exit code 0 in %s", after) + b.memoryNote()}
		}
		c := doctorCheck{Name: "boot-start", Status: "warn", Code: "boot_worker_exited",
			Hint: fmt.Sprintf("The worker exited with code 0 after %s. A worker must keep running; on Gregale it would stop, or restart under its restart policy, instead of working.", after),
			Fix:  "Keep the process running (a loop or a queue consumer), or deploy a task that finishes with --execution-mode job."}
		if tail := b.docker.logTail(ctx, b.name); tail != "" {
			c.Output = tail
		}
		return c
	}
	c := b.exitedCheck(ctx, "boot-start", st)
	if !st.oomKilled {
		need := "A worker must keep running"
		if b.opts.mode == api.ExecutionModeJob {
			need = "A job must exit with code 0"
		}
		c.Fix = "Read the container output; an app that needs env vars to start can get them with " + b.opts.envFileFlag() + ". " + need + "; Gregale counts any other exit as a startup failure."
	}
	return c
}
