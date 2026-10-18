// Command mail-feedback submits an enrolled provider's original signed report
// to maild's private Unix socket. The source collector retains durable custody
// until this helper exits zero. It is neither a public listener nor a poller.
package main

import (
	"context"
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
	// Default-off means no arguments, credentials, mail or socket access.
	if !internalmail.FeedbackEnabled(getenv) || len(args) != 0 {
		return deferFeedback(output)
	}
	socket := getenv("FAAS_INTERNAL_MAIL_SOCKET")
	if socket == "" {
		socket = internalmail.DefaultSocket
	}
	client, err := internalmail.NewFeedbackClient(socket, getenv("FAAS_INTERNAL_MAIL_FEEDBACK_SOURCE"), getenv("FAAS_INTERNAL_MAIL_FEEDBACK_TOKEN"), func() bool { return internalmail.FeedbackEnabled(getenv) })
	if err != nil {
		return deferFeedback(output)
	}
	if err := client.Submit(ctx, input); err != nil {
		return deferFeedback(output)
	}
	return 0
}

func deferFeedback(output io.Writer) int {
	_, _ = fmt.Fprintln(output, "4.3.0 Gregale feedback not recorded; retain for review or retry")
	return temporaryFailure
}
