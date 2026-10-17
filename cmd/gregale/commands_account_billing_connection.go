package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
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

const accountBillingConnectionUsage = "usage: gregale account billing-connection {connect|status|disconnect} {stripe|paddle|lago} [--api-url URL] [--collection none|send|charge] [--days-until-due N] [--automatic-tax]  (connect reads the key from stdin)"

// cmdAccountBillingConnection manages the account's billing provider keys
// for statement invoice exports (ADR-1502, ADR-1507): connect reads the key
// from stdin so it never appears in shell history or the process list.
func cmdAccountBillingConnection(args []string) int {
	fs := newFlagSet("account billing-connection", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "Lago API URL for the EU region or a self-hosted Lago (connect; default https://api.getlago.com)")
	collection := fs.String("collection", "", "what the provider does with exported invoices: none (drafts, default), send (Stripe) or charge (Stripe, Lago) (connect)")
	daysUntilDue := fs.Int("days-until-due", 0, "days until a sent invoice is due, 1-365 (connect --collection send; default 30)")
	automaticTax := fs.Bool("automatic-tax", false, "have Stripe Tax calculate tax on exported invoices (connect; Stripe only)")
	if len(args) < 2 {
		PrintUsage(osStderr, accountBillingConnectionUsage, "account")
		return 1
	}
	action, provider := args[0], args[1]
	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 || (provider != "stripe" && provider != "paddle" && provider != "lago") {
		PrintUsage(osStderr, accountBillingConnectionUsage, "account")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch action {
	case "connect":
		key, err := readSecretLine(stdinReader, provider)
		if err != nil {
			return printErr("Invalid "+provider+" key", err)
		}
		conn, err := client.PutBillingConnection(ctx, provider, api.PutBillingConnectionRequest{SecretKey: key, APIURL: strings.TrimSpace(*apiURL),
			Collection: strings.TrimSpace(*collection), DaysUntilDue: *daysUntilDue, AutomaticTax: *automaticTax})
		if err != nil {
			return printErr("Could not connect "+provider, err)
		}
		return printBillingConnection(conn)
	case statusLiteral:
		conn, err := client.GetBillingConnection(ctx, provider)
		if err != nil {
			return printErr("No "+provider+" connection", err)
		}
		return printBillingConnection(conn)
	case "disconnect":
		if err := client.DeleteBillingConnection(ctx, provider); err != nil {
			return printErr("Could not disconnect "+provider, err)
		}
		_, _ = fmt.Fprintf(osStdout, "%s disconnected.\n", provider)
		return 0
	}
	PrintUsage(osStderr, accountBillingConnectionUsage, "account")
	return 1
}

func readSecretLine(r io.Reader, provider string) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("pipe the key on stdin, e.g. gregale account billing-connection connect %s < key.txt", provider)
	}
	return line, nil
}

func printBillingConnection(conn api.BillingConnectionResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(conn))
	}
	mode := "test"
	if conn.Livemode {
		mode = "live"
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Provider\t%s\nKey\t…%s (%s mode)\n", conn.Provider, conn.KeyLast4, mode)
	if conn.APIURL != "" {
		_, _ = fmt.Fprintf(tw, "API URL\t%s\n", conn.APIURL)
	}
	_, _ = fmt.Fprintf(tw, "Updated\t%s\n", conn.UpdatedAt.Format(time.RFC3339))
	_ = tw.Flush()
	return 0
}

func printStatementInvoiceExport(tw *tabwriter.Writer, v api.StatementInvoiceExportResponse) {
	link := v.DashboardURL
	if link == "" {
		link = v.HostedInvoiceURL
	}
	_, _ = fmt.Fprintf(tw, "Statement\t%s\nInvoice\t%s %s (%s)\nCustomer\t%s\nAmount\t%s\nOpen\t%s\n",
		v.StatementID, v.Provider, v.InvoiceID, v.Status, v.Customer, formatMillicents(v.Currency, v.AmountMinor*1000), dashIfEmpty(link))
}
