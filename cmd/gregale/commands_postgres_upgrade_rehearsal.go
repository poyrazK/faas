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

func cmdPostgresUpgradeRehearsal(args []string) int {
	const usage = "usage: gregale postgres upgrade-rehearsal DATABASE (--major N [--request-id UUID] | --rehearsal-id UUID)"
	flags := newFlagSet("postgres upgrade-rehearsal", 0)
	major := flags.Int("major", 0, "target PostgreSQL major version")
	request := flags.String("request-id", "", "UUID to reuse when retrying a start")
	rehearsal := flags.String("rehearsal-id", "", "read the status of this rehearsal")
	if err := flags.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	read := flagWasSet(flags, "rehearsal-id")
	suppliedKey := flagWasSet(flags, "request-id")
	if flags.NArg() != 1 || (read && (flagWasSet(flags, "major") || suppliedKey)) || (!read && *major <= 0) {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	key := *request
	if read {
		key = *rehearsal
	}
	if read || suppliedKey {
		id, err := uuid.Parse(key)
		if err != nil || id == uuid.Nil {
			printCommandValidation(os.Stderr, "gregale postgres upgrade-rehearsal: expected a nonzero UUID\n")
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
	requestBody := api.RequestManagedPostgresUpgradeRehearsal{RequestID: key, PostgresMajor: *major}
	var result api.ManagedPostgresUpgradeRehearsal
	switch {
	case read && org != "":
		result, err = client.GetOrgManagedPostgresUpgradeRehearsal(ctx, org, id, key)
	case read:
		result, err = client.GetManagedPostgresUpgradeRehearsal(ctx, id, key)
	case org != "":
		result, err = client.StartOrgManagedPostgresUpgradeRehearsal(ctx, org, id, requestBody)
	default:
		result, err = client.StartManagedPostgresUpgradeRehearsal(ctx, id, requestBody)
	}
	if err != nil {
		if read {
			return printErr("Could not read upgrade rehearsal", err)
		}
		return printErr("Could not start upgrade rehearsal (request ID "+key+")", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderUpgradeRehearsal(osStdout, result)
	return 0
}

func renderUpgradeRehearsal(w io.Writer, result api.ManagedPostgresUpgradeRehearsal) {
	_, _ = fmt.Fprintf(w, "Upgrade rehearsal %s (%s)\n", result.RehearsalID, strings.ReplaceAll(result.Outcome, "_", " "))
	_, _ = fmt.Fprintf(w, "  PostgreSQL %d → %d\n  Request ID: %s\n", result.SourceMajor, result.TargetMajor, result.RequestID)
	if result.Phase != "" {
		_, _ = fmt.Fprintf(w, "  Phase: %s\n", result.Phase)
	}
	if result.RecoveryPoint != nil {
		_, _ = fmt.Fprintf(w, "  Recovery point: %s\n", formatBackupTimestamp(*result.RecoveryPoint))
	}
	if result.RecoveredTo != nil {
		_, _ = fmt.Fprintf(w, "  Replayed to: %s\n", formatBackupTimestamp(*result.RecoveredTo))
	}
	if result.DurationSeconds != nil {
		_, _ = fmt.Fprintf(w, "  Elapsed: %s\n", time.Duration(*result.DurationSeconds)*time.Second)
	}
	if result.FailedPhase != "" {
		_, _ = fmt.Fprintf(w, "  Failed phase: %s\n", result.FailedPhase)
	}
	if result.ErrorCode != "" {
		_, _ = fmt.Fprintf(w, "  Reason: %s\n", strings.ReplaceAll(result.ErrorCode, "_", " "))
	}
}
