package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPReadinessMaintenance(args []string) int {
	if len(args) == 0 || args[0] != "list" && args[0] != "create" && args[0] != "cancel" {
		return printErr("MCP maintenance", errors.New("use maintenance list, create or cancel"))
	}
	fs := newFlagSet("mcp-maintenance-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	scope := fs.String("scope", api.DefaultEnvScope, "exact environment")
	start := fs.String("start", "", "RFC3339 start, defaults to now")
	end := fs.String("end", "", "RFC3339 end")
	duration := fs.Duration("duration", 0, "duration starting now or at --start, at most 720h")
	reason := fs.String("reason", "", "reason for creating or cancelling")
	id := fs.String("id", "", "maintenance window UUID to cancel")
	before := fs.String("before", "", "list cursor")
	limit := fs.Int("limit", 20, "page size 1–50")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	invalid := fs.NArg() != 0 || !api.ValidAppSlug(*app) || api.ValidateScope(*scope) != nil
	allowed := map[string]bool{"app": true}
	switch args[0] {
	case "list":
		for _, k := range []string{"scope", "before", "limit"} {
			allowed[k] = true
		}
		_, _, err := api.ParseMCPReadinessIncidentCursor(*before)
		invalid = invalid || err != nil || *limit < 1 || *limit > 50
	case "create":
		for _, k := range []string{"scope", "start", "end", "duration", "reason"} {
			allowed[k] = true
		}
	case "cancel":
		allowed["id"] = true
		allowed["reason"] = true
		invalid = invalid || uuid.Validate(*id) != nil || !api.ValidMCPMaintenanceReason(*reason)
	}
	fs.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			invalid = true
		}
	})
	req := api.CreateMCPReadinessMaintenanceRequest{Scope: *scope, Reason: *reason}
	if args[0] == "create" {
		var err error
		if *start != "" {
			req.StartsAt, err = time.Parse(time.RFC3339, *start)
			invalid = invalid || err != nil
		}
		if *end != "" {
			req.EndsAt, err = time.Parse(time.RFC3339, *end)
			invalid = invalid || err != nil
		}
		if *duration != 0 {
			if *end != "" || *duration <= 0 {
				invalid = true
			}
			at := req.StartsAt
			if at.IsZero() {
				at = time.Now().UTC()
				req.StartsAt = at
			}
			req.EndsAt = at.Add(*duration)
		}
		invalid = invalid || api.ValidateMCPReadinessMaintenance(req, time.Now().UTC()) != nil
	}
	if invalid {
		return printErr("MCP maintenance", errors.New("supply a valid app, environment, time range and reason; use list flags only with list and --id only with cancel"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("MCP maintenance", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out any
	switch args[0] {
	case "list":
		out, err = client.ListMCPReadinessMaintenance(ctx, *app, *scope, *before, *limit)
	case "create":
		out, err = client.CreateMCPReadinessMaintenance(ctx, *app, req)
	case "cancel":
		out, err = client.CancelMCPReadinessMaintenance(ctx, *app, *id, api.CancelMCPReadinessMaintenanceRequest{Reason: *reason})
	}
	if err != nil {
		return printErr("MCP maintenance", err)
	}
	return writeJSONStdout(out)
}
