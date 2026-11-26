package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdPostgresPreviews sets what a database gives PR preview environments
// (ADR-20261102123523): each preview environment of an app bound to it gets
// its own database according to the selected mode.
func cmdPostgresPreviews(args []string) int {
	const usage = "usage: gregale postgres previews set DATABASE empty|copy|off"
	fs := newFlagSet("postgres previews", flag.ContinueOnError)
	policyFile := fs.String("policy", "", "JSON masking policy for masked previews")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	request := api.SetManagedPostgresPreviewsRequest{Previews: fs.Arg(1)}
	switch request.Previews {
	case "empty", "copy", "masked", "off":
	default:
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	if (request.Previews == "masked") != (*policyFile != "") {
		printCommandValidation(os.Stderr, "--policy is required for masked previews and applies only to that mode\n")
		return 1
	}
	if *policyFile != "" {
		tables, err := readMaskedBranchPolicy(*policyFile)
		if err != nil {
			return printErr("Could not read masking policy", err)
		}
		request.MaskingPolicy = &api.ManagedPostgresPreviewMaskingPolicy{Tables: tables}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	database, err := resolveManagedPostgresDatabase(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	updated, err := client.SetManagedPostgresPreviewsWithPolicy(context.Background(), database.ID, request)
	if err != nil {
		return printErr("Could not change the database's PR previews", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	switch updated.Previews {
	case "":
		_, _ = fmt.Fprintf(osStdout, "%s: PR previews get no database; existing preview databases go with their pull requests\n", updated.Name)
	case "copy":
		_, _ = fmt.Fprintf(osStdout, "%s: each PR preview environment gets a copy of this database as of its setup, bound like production and removed with the preview. Code under review can read the copy.\n", updated.Name)
	case "masked":
		_, _ = fmt.Fprintf(osStdout, "%s: each new PR preview environment gets a validated masked database using the saved policy\n", updated.Name)
	default:
		_, _ = fmt.Fprintf(osStdout, "%s: each PR preview environment gets its own empty database, bound like production and removed with the preview\n", updated.Name)
	}
	return 0
}
