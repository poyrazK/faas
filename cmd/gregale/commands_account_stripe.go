package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// stdinReader is swapped by tests.
var stdinReader io.Reader = os.Stdin

// cmdAccountStripe manages the account's Stripe connection for statement
// invoice exports (ADR-1300): connect reads the key from stdin so it never
// appears in shell history or the process list.
func cmdAccountStripe(args []string) int {
	const usage = "usage: gregale account stripe {connect|status|disconnect}  (connect reads the key from stdin)"
	if len(args) != 1 {
		PrintUsage(osStderr, usage, "account")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch args[0] {
	case "connect":
		key, err := readSecretLine(stdinReader)
		if err != nil {
			return printErr("Invalid Stripe key", err)
		}
		conn, err := client.PutStripeConnection(ctx, api.PutStripeConnectionRequest{SecretKey: key})
		if err != nil {
			return printErr("Could not connect Stripe", err)
		}
		return printStripeConnection(conn)
	case statusLiteral:
		conn, err := client.GetStripeConnection(ctx)
		if err != nil {
			return printErr("No Stripe connection", err)
		}
		return printStripeConnection(conn)
	case "disconnect":
		if err := client.DeleteStripeConnection(ctx); err != nil {
			return printErr("Could not disconnect Stripe", err)
		}
		_, _ = fmt.Fprintln(osStdout, "Stripe disconnected.")
		return 0
	}
	PrintUsage(osStderr, usage, "account")
	return 1
}

func readSecretLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("pipe the Stripe key on stdin, e.g. gregale account stripe connect < key.txt")
	}
	return line, nil
}

func printStripeConnection(conn api.BillingConnectionResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(conn))
	}
	mode := "test"
	if conn.Livemode {
		mode = "live"
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Provider\t%s\nKey\t…%s (%s mode)\nUpdated\t%s\n", conn.Provider, conn.KeyLast4, mode, conn.UpdatedAt.Format(time.RFC3339))
	_ = tw.Flush()
	return 0
}

func printStatementInvoiceExport(tw *tabwriter.Writer, v api.StatementInvoiceExportResponse) {
	_, _ = fmt.Fprintf(tw, "Statement\t%s\nStripe invoice\t%s (draft, %s)\nCustomer\t%s\nAmount\t%s\nOpen\t%s\n",
		v.StatementID, v.InvoiceID, v.Status, v.Customer, formatMillicents(v.Currency, v.AmountMinor*1000), v.DashboardURL)
}
