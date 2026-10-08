package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRoutesHealthProfileHistory(args []string) int {
	flags, positional := splitArgsForFlags(args, "deployment", "limit", "before")
	fs := newFlagSet("routes health profile-history", flag.ContinueOnError)
	var deployment, before string
	limit := api.ProfileCanaryHistoryPageSize
	fs.StringVar(&deployment, "deployment", "", "canary deployment UUID")
	fs.IntVar(&limit, "limit", api.ProfileCanaryHistoryPageSize, "history page size (maximum 10)")
	fs.StringVar(&before, "before", "", "opaque cursor from the previous page")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		return printErr("Invalid canary profile history command", errors.New("supply an app slug"))
	}
	id, err := uuid.Parse(deployment)
	if err != nil || id.String() != deployment {
		return printErr("Invalid canary profile history command", errors.New("supply --deployment with a canonical UUID"))
	}
	if limit < 1 || limit > api.ProfileCanaryHistoryMaxPage {
		return printErr("Invalid canary profile history command", fmt.Errorf("--limit must be between 1 and %d", api.ProfileCanaryHistoryMaxPage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	page, err := client.ListProfileCanaryChecks(ctx, positional[0], deployment, limit, before)
	if err != nil {
		return printErr("Could not read canary profile history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Entries) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No retained canary profile assessments for deployment %s.\n", deployment)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Canary profile history for deployment %s (%d stage assessments)\n", deployment, len(page.Entries))
	for i := range page.Entries {
		renderCanaryProfileSignal(&page.Entries[i], positional[0])
	}
	if page.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: gregale routes health profile-history %s --deployment %s --limit %d --before %s\n", url.PathEscape(positional[0]), deployment, limit, page.NextCursor)
	}
	return 0
}
