package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPReadinessPolicyHistory(args []string) int {
	fs := newFlagSet("mcp-readiness-policy-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	scope := fs.String("scope", api.DefaultEnvScope, "readiness environment")
	var before, from, to, revision, expected, reason string
	limit := 20
	switch args[0] {
	case "history":
		fs.StringVar(&before, "before-revision", "", "history page cursor")
		fs.IntVar(&limit, "limit", 20, "page size 1–50")
	case "diff":
		fs.StringVar(&from, "from-revision", "", "historical revision")
		fs.StringVar(&to, "to-revision", "", "comparison revision; omitted means current")
	case "restore":
		fs.StringVar(&revision, "revision", "", "historical revision")
		fs.StringVar(&expected, "expected-revision", "", "expected current policy revision")
		fs.StringVar(&reason, "reason", "", "audit reason")
	}
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	request := api.RestoreMCPReadinessPolicyRequest{Scope: *scope, ExpectedRevision: expected, Reason: reason}
	if fs.NArg() != 0 || !api.ValidAppSlug(*app) || api.ValidateScope(*scope) != nil || limit < 1 || limit > 50 || before != "" && uuid.Validate(before) != nil || args[0] == "diff" && (uuid.Validate(from) != nil || to != "" && uuid.Validate(to) != nil) || args[0] == "restore" && api.ValidateRestoreMCPReadinessPolicy(request, revision) != nil {
		return printErr("MCP readiness policy", errors.New("supply a valid app, scope, revisions, page size and audit reason for the selected command"))
	}
	client, e := authedClient()
	if e != nil {
		return printErr("MCP readiness policy", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out any
	switch args[0] {
	case "history":
		out, e = client.ListMCPReadinessPolicyHistory(ctx, *app, *scope, before, limit)
	case "diff":
		out, e = client.DiffMCPReadinessPolicy(ctx, *app, *scope, from, to)
	case "restore":
		out, e = client.RestoreMCPReadinessPolicy(ctx, *app, revision, request)
	}
	if e != nil {
		return printErr("MCP readiness policy", e)
	}
	return writeJSONStdout(out)
}
