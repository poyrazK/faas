package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjectsEnvironmentReleaseSets(args []string) int {
	flags, positional := splitArgsForFlags(args, "all")
	fs := newFlagSet("projects-environments-release-sets", flag.ContinueOnError)
	before := fs.String("before", "", "opaque cursor from a previous page")
	fs.StringVar(before, "cursor", "", "alias for --before")
	all := fs.Bool("all", false, "walk every page using --limit and --cursor")
	limit := fs.Int("limit", api.ProjectReleaseSetPageDefault, "page size")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) {
		PrintUsage(os.Stderr, "usage: gregale projects environments release-sets <project> <environment> [--cursor CURSOR] [--all] [--limit N]", "projects environments")
		return 1
	}
	if *limit < 1 || *limit > api.ProjectReleaseSetPageMax {
		return printErr("Invalid page size", fmt.Errorf("limit must be between 1 and %d", api.ProjectReleaseSetPageMax))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var page api.ProjectReleaseSetListResponse
	items, next, err := collectListPages(context.Background(), *before, *all, func(ctx context.Context, cursor string) ([]api.ProjectReleaseSetResponse, string, error) {
		current, err := client.ListProjectReleaseSets(ctx, positional[0], positional[1], cursor, *limit)
		return current.Items, current.NextBefore, err
	})
	page.Items, page.NextBefore = items, next
	if err != nil {
		return printErr("Could not list project release sets", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			api.ProjectReleaseSetListResponse
			NextCursor string `json:"next_cursor,omitempty"`
		}{page, next}))
	}
	_, _ = fmt.Fprintf(osStdout, "Release sets %s/%s\n%-36s %-6s %-8s %-30s %s\n", positional[0], positional[1], "RELEASE", "ACTIVE", "MEMBERS", "CREATED", "EXPIRES")
	for _, release := range page.Items {
		expiry := "—"
		if release.ExpiresAt != nil {
			expiry = release.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
		}
		_, _ = fmt.Fprintf(osStdout, "%-36s %-6t %-8d %-30s %s\n", release.ID, release.Active, len(release.Members), release.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), expiry)
	}
	if page.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "next page: gregale projects environments release-sets %s %s --cursor '%s' --limit %d", positional[0], positional[1], strings.ReplaceAll(page.NextBefore, "'", "'\"'\"'"), *limit)
		_, _ = fmt.Fprintln(osStdout)
	}
	return 0
}
