package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const deploymentAliasRowFormat = "%-20s %-10s %-36s %s\n"

// cmdDeploymentAliases implements `gregale deployments alias list|set|delete`.
func cmdDeploymentAliases(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale deployments alias [list|set|delete]", "deployments")
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregale deployments alias [list|set|delete]", "deployments")
		return 0
	}
	switch args[0] {
	case "list":
		return cmdDeploymentAliasesList(args[1:])
	case "set":
		return cmdDeploymentAliasSet(args[1:])
	case "delete":
		return cmdDeploymentAliasDelete(args[1:])
	default:
		PrintUsage(os.Stderr, "usage: gregale deployments alias [list|set|delete]", "deployments")
		return 1
	}
}

func cmdDeploymentAliasesList(args []string) int {
	fs := newFlagSet("deployments alias list", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug; defaults to the linked project")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale deployments alias list [--app SLUG]", "deployments")
		return 1
	}
	slug, code := resolveDeploymentAliasApp(*appFlag)
	if code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	page, err := client.ListDeploymentAliases(context.Background(), slug)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Items) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No deployment aliases for app %q.\n", slug)
		_, _ = fmt.Fprintf(osStdout, "Set one: `gregale deployments alias set --app %s --name canary --deployment vN`.\n", slug)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, deploymentAliasRowFormat, "NAME", "REVISION", "DEPLOYMENT", "URL")
	for _, alias := range page.Items {
		url := alias.URL
		if url == "" {
			url = alias.Host
		}
		if url == "" {
			url = "-"
		}
		_, _ = fmt.Fprintf(osStdout, deploymentAliasRowFormat, alias.Name, deploymentAliasRevisionLabel(alias), alias.DeploymentID, url)
	}
	return 0
}

func cmdDeploymentAliasSet(args []string) int {
	fs := newFlagSet("deployments alias set", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug; defaults to the linked project")
	name := fs.String("name", "", "lowercase DNS-label alias name")
	deploymentRef := fs.String("deployment", "", "deployment ID or app revision (vN)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *name == "" || *deploymentRef == "" {
		PrintUsage(os.Stderr, "usage: gregale deployments alias set --name NAME --deployment <ID|vN> [--app SLUG]", "deployments")
		return 1
	}
	if !api.ValidDeploymentAliasName(*name) {
		return printErr("Invalid alias name", fmt.Errorf("%q must be a lowercase DNS label of at most 63 characters", *name))
	}
	if !validDeploymentRef(*deploymentRef) {
		return printErr("Invalid deployment reference", fmt.Errorf("%q must be a deployment ID or revision such as v7", *deploymentRef))
	}
	slug, code := resolveDeploymentAliasApp(*appFlag)
	if code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	deploymentID, err := resolveDeploymentArg(context.Background(), client, slug, *deploymentRef)
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	alias, err := client.SetDeploymentAlias(context.Background(), slug, *name, api.SetDeploymentAliasRequest{DeploymentID: deploymentID})
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(alias))
	}
	if alias.URL == "" {
		PrintOK(osStdout, "Alias %s now points to %s; no public URL is configured.", alias.Name, deploymentAliasRevisionLabel(alias))
	} else {
		PrintOK(osStdout, "Alias %s now points to %s: %s", alias.Name, deploymentAliasRevisionLabel(alias), alias.URL)
	}
	return 0
}

func cmdDeploymentAliasDelete(args []string) int {
	fs := newFlagSet("deployments alias delete", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug; defaults to the linked project")
	name := fs.String("name", "", "lowercase DNS-label alias name")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *name == "" {
		PrintUsage(os.Stderr, "usage: gregale deployments alias delete --name NAME [--app SLUG]", "deployments")
		return 1
	}
	if !api.ValidDeploymentAliasName(*name) {
		return printErr("Invalid alias name", fmt.Errorf("%q must be a lowercase DNS label of at most 63 characters", *name))
	}
	slug, code := resolveDeploymentAliasApp(*appFlag)
	if code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteDeploymentAlias(context.Background(), slug, *name); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"app": slug, "name": *name, "deleted": true}))
	}
	PrintOK(osStdout, "Deleted deployment alias %s from app %s", *name, slug)
	return 0
}

func resolveDeploymentAliasApp(explicit string) (string, int) {
	slug, err := resolveAppFlagOrContext(explicit)
	if err != nil {
		if errors.Is(err, errProjectContextNotFound) {
			return "", printErr("Could not resolve app", errors.New("pass --app <slug> or link this project with `gregale link --app <slug>`"))
		}
		return "", printErr("Could not resolve app", err)
	}
	if strings.TrimSpace(slug) == "" {
		return "", printErr("Could not resolve app", errors.New("pass --app <slug> or link this project with `gregale link --app <slug>`"))
	}
	return slug, 0
}

func deploymentAliasRevisionLabel(alias api.DeploymentAliasResponse) string {
	if label := renderRevision(alias.Revision); label != "" {
		return label
	}
	return alias.DeploymentID
}
