package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDataAPIDelete tears a Data API down: it revokes the app's managed
// PostgreSQL bindings, which deleting the app alone refuses to leave live,
// and then deletes the app.
func cmdDataAPIDelete(args []string) int {
	fs := newFlagSet("data-api delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm deletion without prompting")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum time for the API calls")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *timeout <= 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api delete NAME [--yes]", "data-api")
		return 1
	}
	if code := requireAutomationConfirmation(*yes, "--yes"); code != 0 {
		return code
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not read the Data API app", err)
	}
	bindings, err := appPostgresBindings(ctx, client, app.ID)
	if err != nil {
		return printErr("Could not list the app's database bindings", err)
	}
	if !*yes {
		if jsonOutput {
			return printErr("Confirmation required", errors.New("data-api delete requires --yes in JSON mode"))
		}
		fmt.Fprintf(os.Stderr, "Revoke %d database credential(s) of %q and delete the app and all its deployments?\n", len(bindings), slug)
		if !requireTyped(slug) {
			return 130
		}
	}
	revoked := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if binding.State != "deleting" {
			if _, err := client.DeleteManagedPostgresBinding(ctx, binding.ID); err != nil && !isNotFound(err) {
				return printErr("Could not revoke database binding "+binding.ID, err)
			}
		}
		revoked = append(revoked, binding.ID)
	}
	if err := client.DeleteApp(ctx, slug); err != nil {
		return printErr("Bindings revoked; could not delete the app", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"slug": slug, "deleted": true, "revoked_bindings": revoked}))
	}
	PrintOK(osStdout, "Revoked %d database binding(s) and deleted %s", len(revoked), slug)
	return 0
}

// appPostgresBindings lists the app's managed PostgreSQL bindings that are
// not yet deleted, across every database the caller can see.
func appPostgresBindings(ctx context.Context, client *api.Client, appID string) ([]api.ManagedPostgresBinding, error) {
	databases, err := client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.Problem.Status == 404 || apiErr.Problem.Status == 503) {
			// Managed PostgreSQL is unavailable here: the app has no bindings.
			return nil, nil
		}
		return nil, err
	}
	var out []api.ManagedPostgresBinding
	for _, database := range databases.Items {
		list, err := client.ListManagedPostgresBindings(ctx, database.ID)
		if err != nil {
			return nil, err
		}
		for _, binding := range list.Items {
			if binding.AppID == appID && binding.State != "deleted" {
				out = append(out, binding)
			}
		}
	}
	return out, nil
}
