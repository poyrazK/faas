package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
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
	if *app == "" {
		if linked, linkErr := resolveAppFlagOrContext(""); linkErr == nil && linked != "" {
			*app = linked
		}
	}
	if *app == "" {
		if _, isRevision := parseRevisionRef(pos[0]); isRevision {
			return printErr("Could not resolve deployment", fmt.Errorf("revision %s needs --app <slug> or a linked project", pos[0]))
		}
		dep, err := client.GetDeployment(context.Background(), pos[0])
		if err != nil {
			return printErr("Could not resolve deployment", err)
		}
		slug, ok := appSlugsByID(client)[dep.AppID]
		if !ok {
			return printErr("Could not resolve deployment", fmt.Errorf("could not find the app for deployment %s; pass --app <slug>", pos[0]))
		}
		*app = slug
	}
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
