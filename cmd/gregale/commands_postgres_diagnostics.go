package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresDiagnostics(args []string) int {
	fs := newFlagSet("postgres diagnostics", flag.ContinueOnError)
	after := fs.String("after", "", "resume after the database ID returned as next_cursor")
	limit := fs.Int("limit", 50, "maximum databases in this page (1-100)")
	if err := fs.Parse(normalizePostgresAttachArgs(args)); err != nil || fs.NArg() != 1 ||
		uuid.Validate(fs.Arg(0)) != nil || (*after != "" && uuid.Validate(*after) != nil) || *limit < 1 || *limit > 100 {
		PrintUsage(os.Stderr, "usage: gregale postgres diagnostics ACCOUNT_ID [--after UUID] [--limit 1-100]", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.ListManagedPostgresAccountingDiagnostics(context.Background(), fs.Arg(0), *after, *limit)
	if err != nil {
		return printErr("Could not load managed PostgreSQL accounting diagnostics", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresDiagnostics(result)
	return 0
}

func renderPostgresDiagnostics(page api.ManagedPostgresAccountingDiagnosticsResponse) {
	_, _ = fmt.Fprintf(osStdout, "managed postgres accounting: %s (policy enabled: %t)\n", page.AccountID, page.PolicyEnabled)
	for _, d := range page.Items {
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s  blocking=%t\n", d.DatabaseID, d.Name, d.State, d.Blocking)
		_, _ = fmt.Fprintf(osStdout, "  accounting_root: %s; identity_known: %t\n", d.AccountingDatabaseID, d.IdentityKnown)
		_, _ = fmt.Fprintf(osStdout, "  reasons: %s\n", strings.Join(d.Reasons, ", "))
		_, _ = fmt.Fprintf(osStdout, "  required: %s — %s\n", formatPostgresDiagnosticTime(d.RequiredFrom), formatPostgresDiagnosticTime(d.RequiredUntil))
		_, _ = fmt.Fprintf(osStdout, "  collected: %s — %s; observed: %s\n", formatPostgresDiagnosticTime(d.CollectedFrom), formatPostgresDiagnosticTime(d.CollectedUntil), formatPostgresObservedAt(d.ObservedAt))
		if d.CorrectionRequiredAt != nil {
			_, _ = fmt.Fprintf(osStdout, "  final_correction: %s (required: %s)\n", formatPostgresObservedAt(d.CorrectionObservedAt), formatPostgresObservedAt(d.CorrectionRequiredAt))
		}
		if d.LeaseUntil != nil {
			_, _ = fmt.Fprintf(osStdout, "  lease_until: %s\n", formatPostgresObservedAt(d.LeaseUntil))
		}
	}
	if page.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "next_cursor: %s\n", page.NextCursor)
	}
}

func formatPostgresDiagnosticTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339)
}
