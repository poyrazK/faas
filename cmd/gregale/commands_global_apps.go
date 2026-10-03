package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
)

func cmdGlobalApp(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale global-app create|list|show|attach|detach|resolve", "global-app")
		return 1
	}
	if args[0] == "attach" || args[0] == "detach" || args[0] == "resolve" {
		return cmdGlobalAppTarget(args[0], args[1:])
	}
	if (args[0] == "list" && len(args) != 1) || (args[0] != "list" && (len(args) != 2 || !globaldirectory.ValidSlug(args[1]))) {
		return printErr("Invalid global app command", fmt.Errorf("use list, create <slug>, or show <slug>"))
	}
	if args[0] != "list" && args[0] != "create" && args[0] != "show" {
		return printErr("Invalid global app command", fmt.Errorf("unknown verb"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if args[0] == "list" {
		apps, err := client.ListGlobalApps(ctx)
		if err != nil {
			return printErr("Could not list global apps", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(apps))
		}
		for _, app := range apps {
			printGlobalApp(app)
		}
		return 0
	}
	var app api.GlobalAppResponse
	if args[0] == "create" {
		app, err = client.CreateGlobalApp(ctx, api.CreateGlobalAppRequest{Slug: args[1]})
	} else {
		app, err = client.GetGlobalApp(ctx, args[1])
	}
	if err != nil {
		return printErr("Could not read or register global app", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(app))
	}
	printGlobalApp(app)
	return 0
}

func printGlobalApp(app api.GlobalAppResponse) {
	_, _ = fmt.Fprintf(osStdout, "%s\t%s\tpolicy=%s\t%d regional identity target(s)\n", app.Slug, app.ID, app.RoutingPolicy, len(app.Targets))
	for _, target := range app.Targets {
		_, _ = fmt.Fprintf(osStdout, "  %s\t%s\t%s\t%s\n", target.Region, target.RegionalAppSlug, target.RegionalAppID, target.ResolutionStatus)
	}
}

func cmdGlobalAppTarget(verb string, args []string) int {
	if len(args) == 0 || !globaldirectory.ValidSlug(args[0]) {
		return printErr("Invalid global app target", fmt.Errorf("a global app slug is required"))
	}
	fs := newFlagSet("global-app "+verb, flag.ContinueOnError)
	region := fs.String("region", "", "selected regional cell (required)")
	var localSlug, tokenEnv *string
	if verb == "attach" {
		localSlug = fs.String("regional-app", "", "existing regional app slug (required)")
		tokenEnv = fs.String("regional-token-env", "", "environment variable holding the regional account-admin key (required)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *region == "" || !api.ValidComputeRegion(*region) {
		return printErr("Invalid global app target", fmt.Errorf("an explicit canonical --region is required"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if verb == "attach" {
		return attachGlobalTargetCLI(ctx, client, args[0], *region, *localSlug, *tokenEnv)
	}
	if verb == "detach" {
		if err := client.DetachGlobalAppTarget(ctx, args[0], *region); err != nil {
			return printErr("Could not detach regional identity", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]bool{"detached": true}))
		}
		_, _ = fmt.Fprintf(osStdout, "Detached %s from %s\n", *region, args[0])
		return 0
	}
	resolved, err := client.ResolveGlobalApp(ctx, args[0], *region)
	if err != nil {
		return printErr("Could not resolve regional identity", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resolved))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", resolved.Region, resolved.APIBase, resolved.RegionalAppSlug, resolved.RegionalAppID)
	return 0
}

func attachGlobalTargetCLI(ctx context.Context, client *Client, slug, region, localSlug, tokenEnv string) int {
	if !globaldirectory.ValidSlug(localSlug) || !validDirectoryTokenEnv(tokenEnv) {
		return printErr("Invalid regional identity", fmt.Errorf("--regional-app and --regional-token-env are required"))
	}
	token := os.Getenv(tokenEnv)
	if !api.ValidAPIKeyFormat(token) {
		return printErr("Regional key unavailable", fmt.Errorf("the named environment variable must contain a regional account-admin API key"))
	}
	target, err := client.AttachGlobalAppTarget(ctx, slug, region, api.AttachGlobalAppTargetRequest{RegionalAppSlug: localSlug, RegionalAPIKey: token})
	if err != nil {
		return printErr("Could not verify and attach regional identity", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(target))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", target.Region, target.RegionalAppSlug, target.RegionalAppID, target.ResolutionStatus)
	return 0
}

func validDirectoryTokenEnv(name string) bool {
	if name == "" {
		return false
	}
	for i, ch := range name {
		if ch != '_' && (ch < 'A' || ch > 'Z') && (ch < 'a' || ch > 'z') && (i == 0 || ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}
