package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresReconcile(args []string) int {
	fs := newFlagSet("postgres reconcile", flag.ContinueOnError)
	file := fs.String("file", "", "verified identity/shutdown JSON file (required)")
	apply := fs.Bool("apply", false, "apply the reviewed preview revision")
	sessionFile := fs.String("session-file", "", "private operator session cookie file (required for apply)")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *file == "" {
		PrintUsage(osStderr, "usage: gregale postgres reconcile ACCOUNT_ID --file FILE [--apply --session-file FILE]", "postgres")
		return 1
	}
	account, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		return printErr("Invalid account ID", err)
	}
	request, err := readPostgresAccountingEvidence[api.ManagedPostgresAccountingReconciliationRequest](*file, 32<<10)
	if err != nil {
		return printErr("Invalid reconciliation file", err)
	}
	if *apply && request.ExpectedRevision == "" {
		return printErr("Preview revision is required for --apply", fmt.Errorf("set expected_revision to the revision returned by preview"))
	}
	if !*apply && request.ExpectedRevision != "" {
		return printErr("Preview file contains expected_revision", fmt.Errorf("remove expected_revision before previewing"))
	}
	client, err := postgresAccountingClient(*sessionFile, *apply)
	if err != nil {
		return printErr("Not logged in", err)
	}
	var result api.ManagedPostgresAccountingReconciliationResult
	if *apply {
		result, err = client.ApplyManagedPostgresAccountingReconciliation(context.Background(), account.String(), request)
	} else {
		result, err = client.PreviewManagedPostgresAccountingReconciliation(context.Background(), account.String(), request)
	}
	if err != nil {
		return printErr("Could not reconcile managed PostgreSQL accounting", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Accounting reconciliation %s (applied=%t)\n  database: %s\n  shutdown: %s\n  evidence observed at: %s\n  shared accounting: %t\n  usage recovery required: %t\n  revision: %s\n",
		result.ReconciliationID, result.Applied, result.DatabaseID, result.ShutdownAt.Format("2006-01-02T15:04:05Z07:00"), result.ObservedAt.Format("2006-01-02T15:04:05Z07:00"), result.SharedAccounting, result.RequiresUsageRecovery, result.Revision)
	return 0
}
