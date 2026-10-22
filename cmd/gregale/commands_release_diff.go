package main

// ADR-20261021101117: release diff.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDeploymentDiff prints what changed between a release and the one
// before it, or --against another release.
func cmdDeploymentDiff(args []string) int {
	fs := newFlagSet("deployment diff", flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve vN revisions (defaults to linked project app)")
	against := fs.String("against", "", "release to compare with (default: the release before it)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale deployment diff <id|vN> [--against <id|vN>] [--app <slug>]", "deployment")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	id, err := resolveDeploymentArg(ctx, client, *app, fs.Arg(0))
	if err != nil {
		return printErr("Could not resolve the deployment", err)
	}
	base := ""
	if *against != "" {
		if base, err = resolveDeploymentArg(ctx, client, *app, *against); err != nil {
			return printErr("Could not resolve --against", err)
		}
	}
	diff, err := client.GetReleaseDiff(ctx, id, base)
	if err != nil {
		return printErr("Could not compare the releases", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(diff))
	}
	_, _ = fmt.Fprint(osStdout, strings.Join(releaseDiffLines(diff), "\n")+"\n")
	return 0
}

// releaseDiffLines renders a diff for a terminal, one fact per line.
func releaseDiffLines(d api.ReleaseDiff) []string {
	name := func(s api.ReleaseDiffSide) string {
		if s.Revision > 0 {
			return fmt.Sprintf("v%d (%s)", s.Revision, s.DeploymentID)
		}
		return s.DeploymentID
	}
	if d.From == nil {
		return []string{fmt.Sprintf("%s: %s is the app's first release.", d.App, name(d.To))}
	}
	lines := []string{fmt.Sprintf("%s: %s → %s", d.App, name(*d.From), name(d.To)), "Summary: " + d.Summary}
	if d.Code.Changed {
		line := fmt.Sprintf("Code: %s → %s", orDash(shortCommit(d.Code.FromCommit)), orDash(shortCommit(d.Code.ToCommit)))
		if d.Code.ImageChanged {
			line += " (new image)"
		}
		lines = append(lines, line)
		if d.Code.CompareURL != "" {
			lines = append(lines, "  "+d.Code.CompareURL)
		}
	}
	if !d.Config.Known {
		lines = append(lines, "Config: unknown ("+d.Config.Note+")")
	}
	for _, k := range d.Config.Variables {
		lines = append(lines, fmt.Sprintf("Variable %s %s", k.Key, k.Change))
	}
	for _, k := range d.Config.Secrets {
		lines = append(lines, fmt.Sprintf("Secret %s %s", k.Key, k.Change))
	}
	if !d.Dependencies.Known {
		lines = append(lines, "Packages: unknown ("+d.Dependencies.Note+")")
	}
	for _, p := range d.Dependencies.Changed {
		lines = append(lines, fmt.Sprintf("Package %s %s → %s", p.Name, p.From, p.To))
	}
	for _, p := range d.Dependencies.Added {
		lines = append(lines, fmt.Sprintf("Package %s added (%s)", p.Name, p.To))
	}
	for _, p := range d.Dependencies.Removed {
		lines = append(lines, fmt.Sprintf("Package %s removed (was %s)", p.Name, p.From))
	}
	if d.Dependencies.Truncated {
		lines = append(lines, fmt.Sprintf("Packages: lists cut at %d; use --json for what was returned", api.ReleaseDiffPackagesMax))
	}
	for _, f := range d.Runtime {
		lines = append(lines, fmt.Sprintf("Setting %s: %s → %s", f.Field, orDash(f.From), orDash(f.To)))
	}
	if d.Database.Changed {
		lines = append(lines, fmt.Sprintf("Release command: %s → %s", orDash(d.Database.FromReleaseCommand), orDash(d.Database.ToReleaseCommand)))
	}
	return lines
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
