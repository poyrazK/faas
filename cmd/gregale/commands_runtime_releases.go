package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdDeploymentRuntime(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("deployment runtime", flag.ContinueOnError)
	app := fs.String("app", "", "app slug for a vN revision")
	target := fs.String("target", "", "published runtime release ID to preview")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validDeploymentRef(positional[0]) {
		return printErr("Invalid runtime preview", errors.New("usage: gregale deployment runtime <ID|vN> [--app SLUG] [--target RELEASE_ID]"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	id, err := resolveDeploymentArg(ctx, client, *app, positional[0])
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	if *target != "" {
		return printRuntimeUpgradePreview(ctx, client, id, *target)
	}
	out, err := client.GetDeploymentRuntime(ctx, id)
	if err != nil {
		return printErr("Could not read runtime identity", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Runtime identity: %s\n%s\n", previewReportText(out.Status), previewReportText(out.Reason))
	if out.Current != nil {
		_, _ = fmt.Fprintf(osStdout, "Current release: %s (%s / %s)\n", out.Current.ID, out.Current.Runtime, out.Current.Architecture)
	}
	for _, r := range out.Releases {
		_, _ = fmt.Fprintf(osStdout, "Published: %s — qualification %s\n", r.ID, previewReportText(r.Qualification))
	}
	return 0
}
func printRuntimeUpgradePreview(ctx context.Context, client *api.Client, id, target string) int {
	out, err := client.PreviewRuntimeUpgrade(ctx, id, target)
	if err != nil {
		return printErr("Could not preview runtime update", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Runtime update preview: %s\nTarget: %s\nRebuild required: %t\nCold start required: %t\n", previewReportText(out.Disposition), out.Target.ID, out.RebuildRequired, out.ColdStartRequired)
	for _, reason := range out.Blockers {
		_, _ = fmt.Fprintf(osStdout, "Blocker: %s\n", previewReportText(reason))
	}
	for _, step := range out.RequiredSteps {
		_, _ = fmt.Fprintf(osStdout, "Required: %s\n", previewReportText(step))
	}
	return 0
}
