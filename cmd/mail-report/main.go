// Command mail-report is a Postfix pipe delivery helper, never a public API or
// daemon. A nonzero exit preserves the queued DSN for Postfix's normal retry.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

const temporaryFailure = 75 // sysexits.h EX_TEMPFAIL

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Args[1:], os.Stdin, os.Stdout))
}

func run(ctx context.Context, getenv func(string) string, args []string, input io.Reader, output io.Writer) int {
	// Check both gates before arguments, input, credentials or socket access.
	if !internalmail.ReportsEnabled(getenv) {
		return deferReport(output)
	}
	flags := flag.NewFlagSet("mail-report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sender := flags.String("sender", "", "Postfix envelope sender")
	recipient := flags.String("recipient", "", "Postfix bounce envelope recipient")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return deferReport(output)
	}
	socket := getenv("FAAS_INTERNAL_MAIL_SOCKET")
	if socket == "" {
		socket = internalmail.DefaultSocket
	}
	bridge, err := internalmail.NewPostfixBridge(socket, getenv("FAAS_INTERNAL_MAIL_REPORT_TOKEN"), func() bool { return internalmail.ReportsEnabled(getenv) })
	if err != nil {
		return deferReport(output)
	}
	if err := bridge.Submit(ctx, *sender, *recipient, input); err != nil {
		return deferReport(output)
	}
	return 0
}

func deferReport(output io.Writer) int {
	// Postfix gives an enhanced status code precedence over the exit code.
	// Use only a fixed 4.x.x diagnostic, even for malformed/auth failures: do
	// not silently discard unrecorded evidence or leak mail into a bounce.
	_, _ = fmt.Fprintln(output, "4.3.0 Gregale report not recorded; retain for retry")
	return temporaryFailure
}
