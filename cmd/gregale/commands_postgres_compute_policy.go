package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresComputePolicy(args []string) int {
	fs := newFlagSet("postgres compute-policy", flag.ContinueOnError)
	policy := fs.String("scale-to-zero", "", "true for idle suspension, false for always-on")
	request := fs.String("request-id", "", "stable nonzero UUID; reuse after an uncertain response")
	if err := fs.Parse(normalizePostgresAttachArgs(args)); err != nil {
		return 1
	}
	id, err := uuid.Parse(*request)
	if fs.NArg() != 1 || (*policy != "true" && *policy != "false") || err != nil || id == uuid.Nil || id.String() != *request {
		PrintUsage(os.Stderr, "usage: gregale postgres compute-policy DATABASE --scale-to-zero true|false --request-id UUID", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	database, err := resolveManagedPostgresDatabase(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	scaleToZero := *policy == "true"
	result, err := client.ChangeManagedPostgresComputePolicy(context.Background(), database.ID, api.ChangeManagedPostgresComputePolicyRequest{RequestID: *request, ScaleToZero: &scaleToZero})
	if err != nil {
		return printErr("Could not request managed PostgreSQL compute policy change", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresComputePolicy(result)
	return 0
}

func cmdPostgresComputePolicyStatus(args []string) int {
	fs := newFlagSet("postgres compute-policy-status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
		PrintUsage(os.Stderr, "usage: gregale postgres compute-policy-status DATABASE REQUEST_UUID", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	database, err := resolveManagedPostgresDatabase(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	result, err := client.GetManagedPostgresComputePolicyChange(context.Background(), database.ID, fs.Arg(1))
	if err != nil {
		return printErr("Could not load managed PostgreSQL compute policy change", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresComputePolicy(result)
	return 0
}

func renderPostgresComputePolicy(result api.ManagedPostgresComputePolicyChange) {
	_, _ = fmt.Fprintf(osStdout, "compute policy %s: scale-to-zero %t → %t (%s, generation %d)\n", result.ID, result.FromScaleToZero, result.TargetScaleToZero, result.State, result.Generation)
	if result.ConnectionInterruptionExpected {
		_, _ = fmt.Fprintln(osStdout, "Clients may disconnect during the policy change; reconnect using existing credentials.")
	}
	if result.LastErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, "  last_error_code: %s\n", result.LastErrorCode)
	}
}
