//go:build linux

// ci-runner is a guest workload binary, never an apid/schedd command runner.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/onebox-faas/faas/guest/cirunner"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	code, err := cirunner.RunGuest(ctx, os.Args[1:])
	stop()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Gregale CI runner could not complete")
	}
	os.Exit(code)
}
