package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDeploymentSummary renders the app-scoped release cockpit. The endpoint
// is app-scoped, so the slug comes from --app, the linked project, or, for a
// deployment UUID, the deployment itself. production-us hunt #4: a bare
// `deployment summary <uuid>` was rejected although the UUID names its app.
func cmdDeploymentSummary(args []string) int {
	flags, pos := splitArgsForFlags(args, "interactive")
	fs := newFlagSet("deployment summary", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose a release from app history")
	app := fs.String("app", "", "app slug (defaults to the linked project, or the deployment's own app)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *interactive {
		if len(pos) != 0 {
			return printErr("Invalid interactive summary arguments", fmt.Errorf("choose the release in the flow instead of supplying an ID"))
		}
		if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
			return printErr("Interactive terminal required", fmt.Errorf("use deployment summary ID --app APP for scripts"))
		}
		slug, err := resolveReadAppTarget(*app)
		if err != nil {
			return readAppTargetError(err)
		}
		return cmdDeploymentSummaryInteractive(slug)
	}
	if len(pos) != 1 || (*app != "" && !validCLISlug(*app)) || !validDeploymentRef(pos[0]) {
		PrintUsage(os.Stderr, "usage: gregale release summary <id|vN> [--app SLUG] | gregale release summary --interactive [--app SLUG]", "deployment")
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

	return renderDeploymentSummary(summary)
}

func renderDeploymentSummary(summary api.DeploymentSummaryResponse) int {
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
	renderRolloutDecisions(osStdout, summary.Deployment)
	if line := releaseSummaryLine(summary.Deployment.Release); line != "" {
		_, _ = fmt.Fprintln(osStdout, line)
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

// releaseChangeLines renders the post-deploy "Changes since vN" list. The
// API compares the two rows as they stand now, so the list after every deploy
// led with `status "superseded" -> "live"` and `traffic_percent 0 -> 100`,
// named the internal upload URL, and printed the whole build_plan twice to
// show one changed hash (prod hunt r49). Lifecycle fields and upload URLs are
// left out here (`release summary` keeps every field), and build_plan shows
// only its keys that changed and are not listed already.
func releaseChangeLines(changes []api.DeploymentChange) []string {
	listed := map[string]bool{}
	for _, change := range changes {
		listed[change.Field] = true
	}
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		switch change.Field {
		case "status", "traffic_percent", "rollout_state":
			continue
		case "source_url":
			if internalSourceURL(change.Before) && internalSourceURL(change.After) {
				continue
			}
			lines = append(lines, fmt.Sprintf("%-18s %s -> %s", change.Field, sourceURLLabel(change.Before), sourceURLLabel(change.After)))
			continue
		case "build_plan":
			lines = append(lines, buildPlanChangeLines(change.Before, change.After, listed)...)
			continue
		}
		lines = append(lines, fmt.Sprintf("%-18s %s -> %s", change.Field, formatSummaryValue(change.Before), formatSummaryValue(change.After)))
	}
	return lines
}

// internalSourceURL reports an upload-session URL or no URL: neither names
// a source a person can recognise.
func internalSourceURL(value any) bool {
	s, _ := value.(string)
	return s == "" || strings.HasPrefix(s, "local-tar://")
}

// sourceURLLabel names one side of a source_url change: an upload reads as
// "local upload" so a switch between git and a local deploy stays visible.
func sourceURLLabel(value any) string {
	if s, _ := value.(string); strings.HasPrefix(s, "local-tar://") {
		return "local upload"
	}
	return formatSummaryValue(value)
}

// buildPlanChangeLines lists the build_plan keys whose values differ.
func buildPlanChangeLines(before, after any, listed map[string]bool) []string {
	b, a := summaryObject(before), summaryObject(after)
	if b == nil && a == nil {
		return []string{fmt.Sprintf("%-18s changed", "build_plan")}
	}
	keys := make([]string, 0, len(a)+len(b))
	for key := range a {
		keys = append(keys, key)
	}
	for key := range b {
		if _, ok := a[key]; !ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var lines []string
	for _, key := range keys {
		if listed[key] || reflect.DeepEqual(b[key], a[key]) {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-18s %s -> %s", "build_plan."+key, formatSummaryValue(b[key]), formatSummaryValue(a[key])))
	}
	return lines
}

// summaryObject reads a change value as a JSON object, or nil.
func summaryObject(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(encoded, &out) != nil {
		return nil
	}
	return out
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
