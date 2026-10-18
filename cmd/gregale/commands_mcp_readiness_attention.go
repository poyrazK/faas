package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPReadinessAttention(args []string) int {
	if len(args) == 0 || args[0] != "list" && args[0] != "history" && args[0] != "acknowledge" && args[0] != "release" && args[0] != "configure" {
		return printErr("MCP incident attention", errors.New("use attention list, history, acknowledge, release or configure"))
	}
	fs := newFlagSet("mcp-attention-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	scope := fs.String("scope", "", "environment; includes all-environment incidents")
	after := fs.String("after", "", "incident UUID cursor")
	before := fs.String("before", "", "action history cursor")
	limit := fs.Int("limit", 20, "page size 1–50")
	include := fs.Bool("include-resolved", false, "also list resolved incidents")
	id := fs.String("incident", "", "incident UUID")
	version := fs.Int64("expected-version", -1, "current attention version")
	request := fs.String("request-id", "", "stable decision UUID; reuse on retries")
	reason := fs.String("reason", "", "operator reason, 1–500 UTF-8 bytes")
	expires := fs.String("expires-at", "", "optional RFC3339 acknowledgement expiry; omit for no expiry")
	interval := fs.Duration("interval", 0, "whole-second reminder interval, 1m–24h; zero disables with a zero budget")
	maximum := fs.Int("max-reminders", 0, "lifetime reminder limit 1–10, or zero to disable")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	invalid := fs.NArg() != 0 || !api.ValidAppSlug(*app)
	allowed := map[string]bool{"app": true}
	visited := map[string]bool{}
	req := api.MCPReadinessAttentionAction{RequestID: *request, ExpectedVersion: version, Action: args[0], Reason: *reason}
	switch args[0] {
	case "list":
		for _, k := range []string{"scope", "after", "limit", "include-resolved"} {
			allowed[k] = true
		}
		invalid = invalid || *scope != "" && api.ValidateScope(*scope) != nil || *after != "" && uuid.Validate(*after) != nil || *limit < 1 || *limit > 50
	case "history":
		for _, k := range []string{"incident", "before", "limit"} {
			allowed[k] = true
		}
		_, _, err := api.ParseMCPReadinessIncidentCursor(*before)
		invalid = invalid || uuid.Validate(*id) != nil || err != nil || *limit < 1 || *limit > 50
	default:
		for _, k := range []string{"incident", "expected-version", "request-id", "reason"} {
			allowed[k] = true
		}
		if args[0] == "acknowledge" {
			allowed["expires-at"] = true
			if *expires != "" {
				at, err := time.Parse(time.RFC3339, *expires)
				invalid = invalid || err != nil
				req.ExpiresAt = &at
			}
		}
		if args[0] == "configure" {
			allowed["interval"] = true
			allowed["max-reminders"] = true
			req.Action = "configure_reminders"
			req.ReminderPolicy = &api.MCPReadinessReminderPolicy{IntervalSeconds: int(*interval / time.Second), MaxReminders: *maximum}
			invalid = invalid || *interval%time.Second != 0
		}
		invalid = invalid || uuid.Validate(*id) != nil || api.ValidateMCPReadinessAttentionAction(req) != nil
	}
	fs.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
		if !allowed[f.Name] {
			invalid = true
		}
	})
	if args[0] == "configure" {
		invalid = invalid || !visited["interval"] || !visited["max-reminders"]
	}
	if invalid {
		return printErr("MCP incident attention", errors.New("supply valid list/history filters, or an incident, current version, stable request ID and reason; configure requires interval and max-reminders"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("MCP incident attention", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out any
	switch args[0] {
	case "list":
		out, err = client.ListMCPReadinessAttention(ctx, *app, *scope, *after, *limit, *include)
	case "history":
		out, err = client.ListMCPReadinessAttentionHistory(ctx, *app, *id, *before, *limit)
	default:
		out, err = client.ActMCPReadinessAttention(ctx, *app, *id, req)
	}
	if err != nil {
		return printErr("MCP incident attention", err)
	}
	return writeJSONStdout(out)
}
