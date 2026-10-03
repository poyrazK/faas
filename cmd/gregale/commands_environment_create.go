package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// envCreate provides the branch-like shorthand inside a linked project:
// gregale env create staging --from production.
func envCreate(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("env-create", flag.ContinueOnError)
	from := fs.String("from", "", "source environment to clone")
	project := fs.String("project", "", "project slug (defaults to linked project)")
	protected := fs.Bool("protected", false, "protect the new environment")
	shareResources := fs.Bool("share-resources", false, "explicitly share managed database and object-storage data with the source environment")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale env create <environment> --from <environment> [--project <slug>] [--protected] [--share-resources]", "env")
		return 1
	}
	if !api.ValidProjectEnvironmentSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*from) || positional[0] == *from {
		return printErr("Invalid environment", errors.New("target and --from must be different valid environment slugs"))
	}
	projectSlug, err := environmentProjectSlug(*project)
	if err != nil {
		return printErr("Could not resolve project", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	environment, err := client.CreateProjectEnvironment(context.Background(), projectSlug, api.CreateProjectEnvironmentRequest{
		Slug: positional[0], Protected: protected, FromEnvironment: *from, ShareResources: *shareResources,
	})
	if err != nil {
		return printErr("Create failed", err)
	}
	return renderProjectEnvironment(environment)
}

func environmentProjectSlug(explicit string) (string, error) {
	if explicit != "" {
		if !api.ValidProjectSlug(explicit) {
			return "", fmt.Errorf("project %q is not a valid project slug", explicit)
		}
		return explicit, nil
	}
	linked, _, err := findProjectContext(".")
	if errors.Is(err, errProjectContextNotFound) {
		return "", errors.New("run `gregale link <project-slug>` or pass --project")
	}
	if err != nil {
		return "", err
	}
	return linked.Project, nil
}
