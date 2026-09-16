package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/mail"
)

// mailDryRunUsage is the usage line printed by the dry-run subcommand.
// Mirrors the `gregale throttle-suggestions <slug> [--dry-run]` pattern
// (cmd/gregale/commands_metrics.go:172) so the help system treats both
// identically.
const mailDryRunUsage = "usage: gregale mail dry-run [--unsubscribe-url URL]"

// mailDryRunDocsTopic is the man-page / docs topic. The closed-set
// templates enumeration lives in the topic file the help system
// generates; the value here is just the anchor.
const mailDryRunDocsTopic = "mail-dry-run"

// cmdMail dispatches `gregale mail …` subcommands. Today the only
// subcommand is `dry-run`, which renders every production mail
// template against a fixture account + day and writes the wire
// payload to stdout. Operators run this before flipping a box to
// FAAS_MAIL_TRANSPORT=resend in production — it's the eyeball
// gate for the bulk-sender compliance work (issue #246
// acceptance item 6).
//
// The function lives in its own file so the dispatcher in
// main.go stays compact and the dry-run command can grow
// (per-template flags, fixture override, etc.) without
// sprawling across commands*.go.
func cmdMail(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, mailDryRunUsage, mailDryRunDocsTopic)
		return 1
	}
	switch args[0] {
	case "dry-run":
		return cmdMailDryRun(args[1:])
	default:
		PrintUsage(os.Stderr, mailDryRunUsage+"\nerror: unknown subcommand "+args[0], mailDryRunDocsTopic)
		return 1
	}
}

// cmdMailDryRun renders every production mail template against a
// fixture account + day, applies marketing headers when an
// unsubscribe URL is supplied, and writes the wire payload to
// stdout as pretty-printed JSON.
//
// Flags:
//
//	--unsubscribe-url URL    the same URL the operator wired into
//	                         meterd via FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL.
//	                         Empty = no List-Unsubscribe header,
//	                         matching the dev-box default. When set,
//	                         validated via mail.ValidateUnsubscribeURL.
//
// The fixture is synthetic (see pkg/mail/dryrun.go::RenderAllTemplates)
// so a dry-run never reads customer PII. The output is one JSON
// object per template so an operator can pipe it through `jq`.
func cmdMailDryRun(args []string) int {
	fs := newFlagSet("mail dry-run", flag.ContinueOnError)
	setFlagOutput(fs, os.Stderr)
	unsub := fs.String("unsubscribe-url", "", "List-Unsubscribe URL (RFC 8058); empty disables the header")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, mailDryRunUsage+"\nunexpected positional args: "+fmt.Sprint(fs.Args()), mailDryRunDocsTopic)
		return 1
	}

	renders, err := mail.RenderAllTemplates(*unsub, time.Now())
	if err != nil {
		PrintUsage(os.Stderr, mailDryRunUsage+"\nerror: "+err.Error(), mailDryRunDocsTopic)
		return 1
	}
	if err := writeMailDryRun(os.Stdout, renders); err != nil {
		PrintUsage(os.Stderr, mailDryRunUsage+"\nerror: write stdout: "+err.Error(), mailDryRunDocsTopic)
		return 1
	}
	return 0
}

// writeMailDryRun is the io.Writer seam so the dry-run command
// tests can assert against a bytes.Buffer without standing up a
// full stdout pipe.
func writeMailDryRun(w io.Writer, renders []mail.RenderTemplate) error {
	return mail.WriteDryRunJSON(w, renders)
}
