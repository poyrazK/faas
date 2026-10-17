// Command apid — control API (spec §4.2). The daemon lives in
// pkg/apid/apidserver (ADR-977); local mail-outbox mode stays with the same owner.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mail-outbox" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := mailoutbox.RunOperator(ctx, os.Args[2:], os.Getenv, os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	}
	apidserver.Main()
}
