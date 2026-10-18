package main

import (
	"context"
	"errors"
	"flag"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func cmdMCPReadinessIncidents(args []string) int {
	fs := newFlagSet("mcp-readiness-incidents", flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	scope := fs.String("scope", api.DefaultEnvScope, "environment; empty selects all")
	before := fs.String("before", "", "timeline cursor")
	limit := fs.Int("limit", 20, "page size 1–50")
	if fs.Parse(args) != nil {
		return 1
	}
	_, _, err := api.ParseMCPReadinessIncidentCursor(*before)
	if fs.NArg() != 0 || !api.ValidAppSlug(*app) || *scope != "" && api.ValidateScope(*scope) != nil || *limit < 1 || *limit > 50 || err != nil {
		return printErr("MCP readiness incidents", errors.New("supply a valid app, environment, cursor and page size"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("MCP readiness incidents", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := client.ListMCPReadinessIncidents(ctx, *app, *scope, *before, *limit)
	if err != nil {
		return printErr("MCP readiness incidents", err)
	}
	return writeJSONStdout(out)
}
