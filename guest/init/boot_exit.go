package main

import (
	"fmt"
	"io"
)

// finishBoot reports boot's outcome on the console and returns guest-init's
// exit status. drain waits until the console has sent everything guest-init
// and the workload wrote, and it runs on every path, a clean one included.
//
// PID 1's exit panics the kernel at once, and the panic dump cuts off what the
// console has not sent yet. vmmd tells an app that exited during startup from
// a guest that failed by guest-init's own last lines. Hunt 34 H34-35 drained
// only the error path. An app whose process exits 0 is not restarted, so
// boot returns nil after the supervisor prints "guest-init: app exited
// cleanly (status 0, …)". Without the drain the console sent only
// "guest-init: app " before the panic dump. vmmd found no supervisor line and
// failed the deploy as internal, with none of the app's context. Coding-agent
// eval 2026-11-17: a FastAPI source build whose start command fell back to
// `python main.py`.
func finishBoot(err error, console io.Writer, drain func()) int {
	status := 0
	if err != nil {
		_, _ = fmt.Fprintf(console, "guest-init: %v\n", err)
		status = 1
	}
	drain()
	return status
}
