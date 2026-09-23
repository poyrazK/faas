package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// environmentDiff provides the branch-like shorthand inside a linked project:
// gregale diff staging production.
func environmentDiff(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("diff", flag.ContinueOnError)
	project := fs.String("project", "", "project slug (defaults to linked project)")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 {
		PrintUsage(os.Stderr, "usage: gregale diff <from-environment> <to-environment> [--project <slug>]", "diff")
		return 1
	}
	from, to := positional[0], positional[1]
	if !api.ValidProjectEnvironmentSlug(from) || !api.ValidProjectEnvironmentSlug(to) || from == to {
		return printErr("Invalid environments", fmt.Errorf("source and target must be different valid environment slugs"))
	}
	projectSlug, err := environmentProjectSlug(*project)
	if err != nil {
		return printErr("Could not resolve project", err)
	}
	return runProjectEnvironmentDiff(projectSlug, from, to)
}
