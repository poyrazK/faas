// Command mail-metrics exports the gated private telemetry to node_exporter.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func main() { os.Exit(run(context.Background(), os.Getenv, os.Args[1:], os.Stderr)) }

func run(ctx context.Context, getenv func(string) string, args []string, output io.Writer) int {
	if !internalmail.ReportsEnabled(getenv) {
		return 0
	}
	flags := flag.NewFlagSet("mail-metrics", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("output", "/var/lib/node_exporter/textfile_collector/internal-mail.prom", "aggregate monitoring file")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return fail(output)
	}
	socket := getenv("FAAS_INTERNAL_MAIL_SOCKET")
	if socket == "" {
		socket = internalmail.DefaultSocket
	}
	bridge, err := internalmail.NewPostfixBridge(socket, getenv("FAAS_INTERNAL_MAIL_REPORT_TOKEN"), func() bool { return internalmail.ReportsEnabled(getenv) })
	if err != nil {
		return fail(output)
	}
	if internalmail.BackpressureEnabled(getenv) {
		bridge, err = bridge.WithRelayHealth(func() bool { return internalmail.BackpressureEnabled(getenv) }, getenv("FAAS_INTERNAL_MAIL_SMTP_ADDR"))
		if err != nil {
			return fail(output)
		}
	}
	if err := bridge.ExportMonitoring(ctx, *path); err != nil && !errors.Is(err, internalmail.ErrDisabled) {
		return fail(output)
	}
	return 0
}

func fail(output io.Writer) int {
	_, _ = fmt.Fprintln(output, "internal mail metrics collection failed")
	return 1
}
