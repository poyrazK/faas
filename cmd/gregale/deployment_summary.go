package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDeploymentSummary renders the app-scoped release cockpit. The endpoint
// is app-scoped, so the slug comes from --app, the linked project, or, for a
// deployment UUID, the deployment itself. production-us hunt #4: a bare
// `deployment summary <uuid>` was rejected although the UUID names its app.
func cmdDeploymentSummary(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newFlagSet("deployment summary", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to the linked project, or the deployment's own app)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || (*app != "" && !validCLISlug(*app)) || !validDeploymentRef(pos[0]) {
		PrintUsage(os.Stderr, "usage: gregale deployment summary <id|vN> [--app SLUG]", "deployment")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	slug, err := deploymentAppSlug(client, *app, pos[0])
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	*app = slug
	// ADR-198 — --app is already required here, so a vN handle is
	// unambiguous without consulting the linked project.
	deploymentID, err := resolveDeploymentRef(context.Background(), client, *app, pos[0])
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	summary, err := client.GetAppDeploymentSummary(context.Background(), *app, deploymentID)
	if err != nil {
		return printErr("Could not fetch deployment summary", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}

	_, _ = fmt.Fprintf(osStdout, "deployment: %s (%s)\n", summary.Deployment.ID, summary.Deployment.Status)
	if summary.Previous == nil {
		_, _ = fmt.Fprintln(osStdout, "previous: none (initial deployment)")
	} else {
		_, _ = fmt.Fprintf(osStdout, "previous: %s (%s)\n", summary.Previous.ID, summary.Previous.Status)
	}
	if summary.RollbackTargetID == "" {
		_, _ = fmt.Fprintln(osStdout, "rollback_target: none")
	} else {
		_, _ = fmt.Fprintf(osStdout, "rollback_target: %s\n", summary.RollbackTargetID)
	}
	if len(summary.Changes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "changes: none")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "changes:")
	for _, change := range summary.Changes {
		_, _ = fmt.Fprintf(osStdout, "  %-18s %s -> %s\n", change.Field,
			formatSummaryValue(change.Before), formatSummaryValue(change.After))
	}
	return 0
}

func formatSummaryValue(value any) string {
	if value == nil {
		return "null"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

// deploymentAppSlug finds the app an app-scoped deployment command addresses:
// the explicit --app, else the linked project, else (for a deployment UUID)
// the deployment's own app. production-us hunt #4: `deployment summary`,
// `deploys cancel` and `deploys clear` demanded --app for a UUID that already
// names its app.
func deploymentAppSlug(client *api.Client, explicit, ref string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if linked, err := resolveAppFlagOrContext(""); err == nil && linked != "" {
		return linked, nil
	}
	if _, isRevision := parseRevisionRef(ref); isRevision {
		return "", fmt.Errorf("revision %s needs --app <slug> or a linked project", ref)
	}
	dep, err := client.GetDeployment(context.Background(), ref)
	if err != nil {
		return "", err
	}
	slug, ok := appSlugsByID(client)[dep.AppID]
	if !ok {
		return "", fmt.Errorf("could not find the app for deployment %s; pass --app <slug>", ref)
	}
	return slug, nil
}
