package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresRestoreDrill(args []string) int {
	const usage = "usage: gregale postgres restore-drill DATABASE [--request-id UUID | --drill-id UUID]"
	flags := newFlagSet("postgres restore-drill", 0)
	request := flags.String("request-id", "", "UUID to reuse when retrying a start")
	drill := flags.String("drill-id", "", "read the status of this drill")
	if err := flags.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	read, suppliedKey := flagWasSet(flags, "drill-id"), flagWasSet(flags, "request-id")
	if flags.NArg() != 1 || read && suppliedKey {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	key := *request
	if read {
		key = *drill
	}
	if read || suppliedKey {
		id, err := uuid.Parse(key)
		if err != nil || id == uuid.Nil {
			printCommandValidation(os.Stderr, "gregale postgres restore-drill: expected a nonzero UUID\n")
			return 1
		}
		key = id.String()
	} else {
		key = uuid.NewString()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, flags.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	org := client.ProjectOrganization()
	var result api.ManagedPostgresRestoreDrill
	switch {
	case read && org != "":
		result, err = client.GetOrgManagedPostgresRestoreDrill(ctx, org, id, key)
	case read:
		result, err = client.GetManagedPostgresRestoreDrill(ctx, id, key)
	case org != "":
		result, err = client.StartOrgManagedPostgresRestoreDrill(ctx, org, id, api.RequestManagedPostgresRestoreDrill{RequestID: key})
	default:
		result, err = client.StartManagedPostgresRestoreDrill(ctx, id, api.RequestManagedPostgresRestoreDrill{RequestID: key})
	}
	if err != nil {
		if read {
			return printErr("Could not read restore drill", err)
		}
		return printErr("Could not start restore drill (request ID "+key+")", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderRequestedRestoreDrill(osStdout, result)
	return 0
}

func renderRequestedRestoreDrill(w io.Writer, result api.ManagedPostgresRestoreDrill) {
	_, _ = fmt.Fprintf(w, "Restore drill %s (%s)\n", result.DrillID, result.Outcome)
	_, _ = fmt.Fprintf(w, "  Request ID: %s\n", result.RequestID)
	if !result.TargetAt.IsZero() {
		_, _ = fmt.Fprintf(w, "  Recovery target: %s\n", formatBackupTimestamp(result.TargetAt))
	}
	if result.RecoveredTo != nil {
		_, _ = fmt.Fprintf(w, "  Replayed to: %s\n", formatBackupTimestamp(*result.RecoveredTo))
	}
	if result.DurationSeconds != nil {
		_, _ = fmt.Fprintf(w, "  Elapsed: %s\n", time.Duration(*result.DurationSeconds)*time.Second)
	}
	if result.ErrorCode != "" {
		_, _ = fmt.Fprintf(w, "  Reason: %s\n", strings.ReplaceAll(result.ErrorCode, "_", " "))
	}
}
