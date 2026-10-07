package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresCutover(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale postgres cutover <prepare|get|verify|cancel> ...", "postgres")
		return 1
	}
	if args[0] == "prepare" {
		return cmdPostgresCutoverPrepare(args[1:])
	}
	switch args[0] {
	case "get", "verify", "cancel":
	default:
		PrintUsage(osStderr, "usage: gregale postgres cutover <prepare|get|verify|cancel> ...", "postgres")
		return 1
	}
	id, ok := onePostgresID("postgres cutover "+args[0], args[1:])
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var result api.ManagedPostgresCutover
	switch args[0] {
	case "get":
		result, err = client.GetManagedPostgresCutover(context.Background(), id)
	case "verify":
		result, err = client.VerifyManagedPostgresCutover(context.Background(), id)
	case "cancel":
		result, err = client.CancelManagedPostgresCutover(context.Background(), id)
	}
	if err != nil {
		return printErr("Could not "+args[0]+" PostgreSQL cutover", err)
	}
	return outputPostgresCutover(result)
}
func cmdPostgresCutoverPrepare(args []string) int {
	fs := newFlagSet("postgres cutover prepare", flag.ContinueOnError)
	scope := fs.String("scope", "", "environment scope (defaults to linked project environment, otherwise production)")
	if err := fs.Parse(normalizePostgresAttachArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 3 {
		PrintUsage(osStderr, "usage: gregale postgres cutover prepare SOURCE TARGET APP [--scope SCOPE]", "postgres")
		return 1
	}
	resolvedScope, err := resolveEnvironmentFlagOrContext(*scope)
	if err != nil {
		return printErr("Could not read local project context", err)
	}
	*scope = resolvedScope
	if *scope == "" {
		*scope = api.DefaultEnvScope
	}
	if err := api.ValidateScope(*scope); err != nil {
		return printErr("Invalid environment scope", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	source, err := resolveManagedPostgresDatabase(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find source database", err)
	}
	target, err := resolveManagedPostgresDatabase(ctx, client, fs.Arg(1))
	if err != nil {
		return printErr("Could not find target database", err)
	}
	app, err := client.GetApp(ctx, fs.Arg(2))
	if err != nil {
		return printErr("Could not find app", err)
	}
	result, err := client.PrepareManagedPostgresCutover(ctx, api.PrepareManagedPostgresCutoverRequest{SourceDatabaseID: source.ID, TargetDatabaseID: target.ID, AppID: app.ID, Scope: *scope})
	if err != nil {
		return printErr("Could not prepare PostgreSQL cutover", err)
	}
	return outputPostgresCutover(result)
}
func outputPostgresCutover(result api.ManagedPostgresCutover) int {
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresCutover(osStdout, result)
	return 0
}
func renderPostgresCutover(w io.Writer, c api.ManagedPostgresCutover) {
	_, _ = fmt.Fprintf(w, "postgres cutover %s\n  state:              %s\n  source_database:    %s\n  target_database:    %s\n  app_id:             %s\n  scope:              %s\n  verification_fresh: %t\n", c.ID, c.State, c.SourceDatabaseID, c.TargetDatabaseID, c.AppID, c.Scope, c.VerificationFresh)
	if c.LastErrorCode != "" {
		_, _ = fmt.Fprintf(w, "  last_error_code:    %s\n", c.LastErrorCode)
	}
	if c.VerifiedAt != "" {
		_, _ = fmt.Fprintf(w, "  verified_at:        %s\n", c.VerifiedAt)
	}
	for _, m := range c.Members {
		_, _ = fmt.Fprintf(w, "  %s (%s): %s\n", m.EnvironmentKey, m.Access, m.State)
	}
	_, _ = fmt.Fprintln(w, "Workloads continue using the source database.")
}
