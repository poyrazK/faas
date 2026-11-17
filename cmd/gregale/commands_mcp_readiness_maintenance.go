package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPReadinessMaintenance(args []string) int {
	if len(args) == 0 || args[0] != "list" && args[0] != "create" && args[0] != "cancel" {
		return printErr("Maintenance window", errors.New("use maintenance list, create or cancel"))
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
	if fs.Parse(args[1:]) != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	req, problem := mcpMaintenanceRequest(fs, args[0], *app, *scope, *start, *end, *duration, *reason, *id, *before, *limit)
	if problem != "" {
		return printErr("Maintenance window", errors.New(problem))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Maintenance window", err)
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
		return printErr("Maintenance window", err)
	}
	if jsonOutput {
		return writeJSONStdout(out)
	}
	return renderMCPMaintenance(out)
}

// mcpMaintenanceRequest validates the flags for one subcommand and returns
// the first problem in words that say what to change (ADR-20261110090000).
func mcpMaintenanceRequest(fs *flag.FlagSet, sub, app, scope, start, end string, duration time.Duration, reason, id, before string, limit int) (api.CreateMCPReadinessMaintenanceRequest, string) {
	req := api.CreateMCPReadinessMaintenanceRequest{Scope: scope, Reason: reason}
	allowed := map[string]bool{"app": true}
	switch sub {
	case "list":
		allowed["scope"], allowed["before"], allowed["limit"] = true, true, true
	case "create":
		for _, k := range []string{"scope", "start", "end", "duration", "reason"} {
			allowed[k] = true
		}
	case "cancel":
		allowed["id"], allowed["reason"] = true, true
	}
	var stray string
	fs.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] && stray == "" {
			stray = f.Name
		}
	})
	switch {
	case fs.NArg() != 0:
		return req, fmt.Sprintf("unexpected argument %q; every value is a flag", fs.Arg(0))
	case stray != "":
		return req, fmt.Sprintf("--%s does not apply to maintenance %s", stray, sub)
	case app == "":
		return req, "--app is required"
	case !api.ValidAppSlug(app):
		return req, fmt.Sprintf("--app %q is not a valid app slug", app)
	case api.ValidateScope(scope) != nil:
		return req, fmt.Sprintf("--scope %q is not a valid environment name", scope)
	}
	switch sub {
	case "list":
		if _, _, err := api.ParseMCPReadinessIncidentCursor(before); err != nil {
			return req, "--before is not a cursor from a previous page"
		}
		if limit < 1 || limit > 50 {
			return req, "--limit must be 1–50"
		}
	case "cancel":
		if uuid.Validate(id) != nil {
			return req, "--id must be a window id from maintenance list"
		}
		if !api.ValidMCPMaintenanceReason(reason) {
			return req, "--reason is required (1–500 bytes): say why the window ends early"
		}
	case "create":
		now := time.Now().UTC()
		var err error
		if start != "" {
			if req.StartsAt, err = time.Parse(time.RFC3339, start); err != nil {
				return req, "--start must be an RFC3339 time such as 2026-11-10T09:00:00Z"
			}
		}
		if end != "" {
			if req.EndsAt, err = time.Parse(time.RFC3339, end); err != nil {
				return req, "--end must be an RFC3339 time such as 2026-11-10T10:00:00Z"
			}
		}
		if duration != 0 {
			if end != "" {
				return req, "use --end or --duration, not both"
			}
			if duration < 0 {
				return req, "--duration must be positive"
			}
			if req.StartsAt.IsZero() {
				req.StartsAt = now
			}
			req.EndsAt = req.StartsAt.Add(duration)
		}
		if err := api.ValidateMCPReadinessMaintenance(req, now); err != nil {
			return req, err.Error()
		}
	}
	return req, ""
}

func renderMCPMaintenance(out any) int {
	switch v := out.(type) {
	case api.MCPReadinessMaintenancePage:
		if len(v.Windows) == 0 {
			_, _ = fmt.Fprintln(osStdout, "(no maintenance windows)")
			return 0
		}
		tw := tabwriter.NewWriter(osStdout, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tSCOPE\tSTARTS\tENDS\tSTATE\tREASON")
		now := time.Now()
		for _, w := range v.Windows {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", w.ID, w.Scope, w.StartsAt.UTC().Format(time.RFC3339), w.EndsAt.UTC().Format(time.RFC3339), mcpMaintenanceState(w, now), terminalSafe(w.Reason))
		}
		_ = tw.Flush()
		if v.NextBefore != "" {
			_, _ = fmt.Fprintf(osStdout, "More: --before %s\n", v.NextBefore)
		}
	case api.MCPReadinessMaintenanceWindow:
		_, _ = fmt.Fprintf(osStdout, "Window %s (%s): %s to %s, %s\n", v.ID, v.Scope, v.StartsAt.UTC().Format(time.RFC3339), v.EndsAt.UTC().Format(time.RFC3339), mcpMaintenanceState(v, time.Now()))
		_, _ = fmt.Fprintln(osStdout, "Silences MCP readiness notifications only; alert rules still deliver.")
	}
	return 0
}

func mcpMaintenanceState(w api.MCPReadinessMaintenanceWindow, now time.Time) string {
	switch {
	case w.CancelledAt != nil:
		return "cancelled"
	case now.Before(w.StartsAt):
		return "scheduled"
	case now.Before(w.EndsAt):
		return "active"
	}
	return "ended"
}
