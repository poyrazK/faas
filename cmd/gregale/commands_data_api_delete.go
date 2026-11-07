package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDataAPIDelete tears a Data API down: it revokes the app's managed
// PostgreSQL bindings, which deleting the app alone refuses to leave live,
// and then deletes the app. An app that still owns object storage buckets is
// refused before anything is revoked, because deleting the app refuses it too.
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
	if code := requireStoredWorkflowConfirmation(*yes, "--yes"); code != 0 {
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
	if code := refuseDataAPIDeleteWithBuckets(ctx, client, slug); code != 0 {
		return code
	}
	bindings, err := appPostgresBindings(ctx, client, slug, app.ID)
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
	revoked, unreachable, err := revokeDataAPIBindings(ctx, client, bindings)
	if err != nil {
		return printErr("Could not revoke a database binding", err)
	}
	if err := client.DeleteApp(ctx, slug); err != nil {
		if len(unreachable) > 0 {
			err = fmt.Errorf("%w; bindings %s could not be revoked from here: they belong to a project environment's database copy, which tearing down that environment removes", err, strings.Join(unreachable, ", "))
		}
		return printErr("Bindings revoked; could not delete the app", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"slug": slug, "deleted": true, "revoked_bindings": revoked}))
	}
	PrintOK(osStdout, "Revoked %d database binding(s) and deleted %s", len(revoked), slug)
	return 0
}

// refuseDataAPIDeleteWithBuckets stops before any credential is revoked when
// the app owns object storage buckets (data-api storage enable creates one):
// deleting the app refuses them, and a revoked API with a live app would be
// left behind.
func refuseDataAPIDeleteWithBuckets(ctx context.Context, client *api.Client, slug string) int {
	buckets, err := client.ListObjectBuckets(ctx, slug)
	if err != nil {
		if routeNotOffered(err) || problemStatus(err) == http.StatusNotFound || problemStatus(err) == http.StatusServiceUnavailable {
			return 0 // Object storage is unavailable here: the app owns no buckets.
		}
		return printErr("Could not list the app's storage buckets", err)
	}
	if len(buckets.Items) == 0 {
		return 0
	}
	names := make([]string, 0, len(buckets.Items))
	for _, bucket := range buckets.Items {
		names = append(names, fmt.Sprintf("%s (%s)", bucket.Name, bucket.ID))
	}
	return printErr("Delete the Data API's storage first", fmt.Errorf(
		"%s owns object storage bucket(s) %s, and an app with buckets cannot be deleted; nothing was revoked. Empty and delete each with gregale bucket delete %s BUCKET_ID, then run gregale data-api delete %s again",
		slug, strings.Join(names, ", "), slug, slug))
}

// revokeDataAPIBindings revokes each binding not already being revoked.
// unreachable lists bindings the binding API does not show the caller: those
// on a project environment's database copy, which only the environment's
// teardown revokes (or bindings that finished revoking meanwhile).
func revokeDataAPIBindings(ctx context.Context, client *api.Client, bindings []api.AppPostgresBinding) (revoked, unreachable []string, err error) {
	revoked = make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if binding.State != "deleting" {
			if _, err := client.DeleteManagedPostgresBinding(ctx, binding.ID); isNotFound(err) {
				unreachable = append(unreachable, binding.ID)
				continue
			} else if err != nil {
				return revoked, unreachable, fmt.Errorf("revoke %s: %w", binding.ID, err)
			}
		}
		revoked = append(revoked, binding.ID)
	}
	return revoked, unreachable, nil
}

// appPostgresBindings lists the app's managed PostgreSQL bindings that are
// not yet deleted, on every database the app is bound to. A control plane
// without the app-scoped route (an older release) is asked database by
// database, which sees only the caller's own databases.
func appPostgresBindings(ctx context.Context, client *api.Client, slug, appID string) ([]api.AppPostgresBinding, error) {
	list, err := client.ListAppPostgresBindings(ctx, slug)
	if err == nil {
		return list.Items, nil
	}
	if !routeNotOffered(err) {
		if status := problemStatus(err); status == http.StatusNotFound || status == http.StatusServiceUnavailable {
			return nil, nil // Managed PostgreSQL is unavailable here: the app has no bindings.
		}
		return nil, err
	}
	databases, err := client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		if status := problemStatus(err); status == http.StatusNotFound || status == http.StatusServiceUnavailable {
			return nil, nil
		}
		return nil, err
	}
	var out []api.AppPostgresBinding
	for _, database := range databases.Items {
		list, err := client.ListManagedPostgresBindings(ctx, database.ID)
		if err != nil {
			return nil, err
		}
		for _, binding := range list.Items {
			if binding.AppID == appID && binding.State != "deleted" {
				out = append(out, api.AppPostgresBinding{ID: binding.ID, Database: database.Name, Scope: binding.Scope,
					EnvironmentKey: binding.EnvironmentKey, Access: binding.Access, State: binding.State})
			}
		}
	}
	return out, nil
}

// problemStatus is the HTTP status of an API problem, or 0.
func problemStatus(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Problem.Status
	}
	return 0
}
