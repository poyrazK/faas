package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func cmdPostgresMigrate(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "check":
			return checkPostgresMigration(args[1:])
		case "activate":
			return activatePostgresMigration(args[1:])
		case "start", "prepare", "verify", "status", "stop":
			return livePostgresMigration(args[0], args[1:])
		}
	}
	PrintUsage(os.Stderr, "usage: gregale postgres migrate {check|start|prepare|verify|activate|status|stop} DATABASE", "postgres")
	return 1
}

func checkPostgresMigration(args []string) int {
	const usage = "usage: gregale postgres migrate check DATABASE [--source-env ENV]"
	if len(args) == 0 {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	fs := newFlagSet("postgres migrate check", 0)
	sourceEnv := fs.String("source-env", "SOURCE_DATABASE_URL", "environment variable with the source PostgreSQL URL")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *sourceEnv == "" {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	source, err := parsePostgresImportURL(os.Getenv(*sourceEnv))
	if err != nil {
		return printErr("Invalid source database URL in "+*sourceEnv, err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	var result api.ManagedPostgresMigrationCheck
	if org := client.ProjectOrganization(); org != "" {
		result, err = client.CheckOrgManagedPostgresMigration(ctx, org, target, source)
	} else {
		result, err = client.CheckManagedPostgresMigration(ctx, target, source)
	}
	if err != nil {
		return printErr("Could not check PostgreSQL migration", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "  PostgreSQL migration check: %s\n", result.Outcome)
		if result.SourceMajor > 0 {
			_, _ = fmt.Fprintf(osStdout, "  Source / target PostgreSQL: %d / %d\n", result.SourceMajor, result.TargetMajor)
			_, _ = fmt.Fprintf(osStdout, "  Source tables / sequences: %d / %d\n", result.TableCount, result.SequenceCount)
			_, _ = fmt.Fprintf(osStdout, "  Free source slots / WAL senders: %d / %d\n", result.FreeSourceSlots, result.FreeSourceSenders)
			retention := formatPostgresStorage(result.SourceWALLimitBytes)
			if result.SourceWALLimitBytes < 0 {
				retention = "unlimited"
			}
			_, _ = fmt.Fprintf(osStdout, "  Source WAL retention cap: %s\n", retention)
			_, _ = fmt.Fprintf(osStdout, "  Source size / target free storage: %s / %s\n", formatPostgresStorage(result.SourceBytes), formatPostgresStorage(result.TargetFreeBytes))
		}
		for _, reason := range result.Reasons {
			_, _ = fmt.Fprintf(osStdout, "  - %s\n", strings.ReplaceAll(reason, "_", " "))
		}
		if result.UnsupportedTableCount > 0 {
			_, _ = fmt.Fprintf(osStdout, "  Unsupported tables: %d\n", result.UnsupportedTableCount)
			for _, table := range result.UnsupportedTables {
				_, _ = fmt.Fprintf(osStdout, "  - %s\n", table)
			}
		}
	}
	if result.Outcome != "compatible" {
		return 1
	}
	return 0
}

func livePostgresMigration(action string, args []string) int {
	fs := newFlagSet("postgres migrate "+action, 0)
	usage := "usage: gregale postgres migrate " + action + " DATABASE"
	var request api.ManagedPostgresImportRequest
	var operation, sourceEnv *string
	if action != "status" {
		operation = fs.String("request-id", "", "original migration operation UUID")
		sourceEnv = fs.String("source-env", "SOURCE_DATABASE_URL", "environment variable with the source PostgreSQL URL")
		usage += " --request-id UUID [--source-env ENV]"
	}
	var verification *string
	var writesPaused *bool
	if action == "verify" {
		verification = fs.String("verification-id", "", "UUID for this continuous source write pause")
		writesPaused = fs.Bool("source-writes-paused", false, "confirm source writers are paused and drained")
		usage += " --verification-id UUID --source-writes-paused"
	}
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || operation != nil && !dbguest.ValidOperationID(*operation) ||
		verification != nil && (!dbguest.ValidOperationID(*verification) || !*writesPaused) {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	if operation != nil {
		source, err := parsePostgresImportURL(os.Getenv(*sourceEnv))
		if err != nil {
			return printErr("Invalid source database URL in "+*sourceEnv, err)
		}
		request = api.ManagedPostgresImportRequest{OperationID: *operation, Source: source}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	var result api.ManagedPostgresMigrationProgress
	org := client.ProjectOrganization()
	switch action {
	case "start":
		if org != "" {
			result, err = client.StartOrgManagedPostgresMigration(ctx, org, target, request)
		} else {
			result, err = client.StartManagedPostgresMigration(ctx, target, request)
		}
	case "prepare":
		if org != "" {
			result, err = client.PrepareOrgManagedPostgresMigration(ctx, org, target, request)
		} else {
			result, err = client.PrepareManagedPostgresMigration(ctx, target, request)
		}
	case "verify":
		verify := api.ManagedPostgresMigrationVerifyRequest{
			ManagedPostgresImportRequest: request, VerificationID: *verification, SourceWritesPaused: *writesPaused,
		}
		if org != "" {
			result, err = client.VerifyOrgManagedPostgresMigration(ctx, org, target, verify)
		} else {
			result, err = client.VerifyManagedPostgresMigration(ctx, target, verify)
		}
	case "stop":
		if org != "" {
			result, err = client.StopOrgManagedPostgresMigration(ctx, org, target, request)
		} else {
			result, err = client.StopManagedPostgresMigration(ctx, target, request)
		}
	case "status":
		if org != "" {
			result, err = client.GetOrgManagedPostgresMigration(ctx, org, target)
		} else {
			result, err = client.GetManagedPostgresMigration(ctx, target)
		}
	}
	if err != nil {
		return printErr("Could not "+action+" PostgreSQL migration", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "  PostgreSQL migration: %s\n", result.State)
		if result.OperationID != "" {
			_, _ = fmt.Fprintf(osStdout, "  Operation: %s\n  Tables copied: %d / %d\n  Apply lag: %s\n  Source WAL retained: %s\n",
				result.OperationID, result.TablesReady, result.Tables, formatPostgresStorage(result.ApplyLagBytes), formatPostgresStorage(result.RetainedWALBytes))
		}
		if result.Reason != "" {
			_, _ = fmt.Fprintf(osStdout, "  %s\n", strings.ReplaceAll(result.Reason, "_", " "))
		}
		if p := result.Preparation; p != nil {
			_, _ = fmt.Fprintf(osStdout, "  Cutover preparation: %s\n  Supported indexes ready: %d / %d\n  Sequences to reconcile: %d\n",
				p.State, p.IndexesReady, p.IndexesTotal, p.Sequences)
			if p.Reason != "" {
				_, _ = fmt.Fprintf(osStdout, "  %s\n", strings.ReplaceAll(p.Reason, "_", " "))
			}
			for _, blocker := range p.Blockers {
				_, _ = fmt.Fprintf(osStdout, "  - %s (%d)\n", strings.ReplaceAll(blocker.Code, "_", " "), blocker.Count)
			}
		}
		if v := result.Verification; v != nil {
			_, _ = fmt.Fprintf(osStdout, "  Replay verification: %s\n  Verification ID: %s\n", v.State, v.ID)
			if v.Reason != "" {
				_, _ = fmt.Fprintf(osStdout, "  %s\n", strings.ReplaceAll(v.Reason, "_", " "))
			}
			if !v.VerifiedAt.IsZero() {
				_, _ = fmt.Fprintf(osStdout, "  Verified at: %s\n  Durable replay position: %s\n  Sequences synchronized: %d\n",
					v.VerifiedAt.UTC().Format(time.RFC3339), v.AppliedLSN, v.SequencesSynced)
			}
		}
	}
	if action == "verify" && (result.Verification == nil || result.Verification.State == "blocked" || result.Verification.State == "paused") {
		return 1
	}
	if action == "prepare" && (result.Preparation == nil || result.Preparation.State == "blocked" || result.Preparation.State == "paused") {
		return 1
	}
	stopped := result.State == "stopped" || result.State == "activated" && result.Reason == ""
	if result.State == "resync_required" || action == "stop" && !stopped || action == "start" && result.State == "paused" {
		return 1
	}
	return 0
}
