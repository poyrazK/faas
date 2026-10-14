package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func cmdMCPOAuthRetirements(args []string) int {
	action := "list"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	if action != "list" && action != "retry" && action != "acknowledge-manual" {
		return printErr("MCP retirement", errors.New("use list, retry or acknowledge-manual"))
	}
	fs := newFlagSet("mcp-retirements-"+action, flag.ContinueOnError)
	id := fs.String("id", "", "retirement UUID")
	offset := fs.Int("offset", 0, "history page offset")
	allow := fs.Bool("allow-active-grant-revocation", false, "explicitly allow provider retirement to invalidate a matching active connection")
	timeout := fs.Duration("timeout", 30*time.Second, "API deadline")
	if fs.Parse(args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *timeout <= 0 || *timeout > time.Duration(api.MCPCatalogOAuthFlowSeconds)*time.Second || *offset < 0 || *offset > api.MCPOAuthRetirementMaxOffset {
		return printErr("MCP retirement", errors.New("invalid flags"))
	}
	if action == "list" && (*id != "" || *allow) || action != "list" && *offset != 0 || action == "acknowledge-manual" && *allow {
		return printErr("MCP retirement", errors.New("flags do not apply to this action"))
	}
	if action != "list" {
		if _, err := uuid.Parse(*id); err != nil {
			return printErr("MCP retirement", errors.New("supply a valid --id"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if action == "list" {
		out, err := client.ListMCPOAuthRetirements(ctx, *offset)
		if err != nil {
			return printErr("MCP retirement", errors.New("could not read account retirement history; administrator access is required"))
		}
		if jsonOutput {
			return writeJSONStdout(out)
		}
		for _, record := range out.Items {
			writeMCPOAuthRetirement(record)
		}
		if out.NextOffset > 0 {
			_, _ = fmt.Fprintf(osStdout, "Next page: --offset %d\n", out.NextOffset)
		}
		return 0
	}
	request := api.MCPOAuthRetirementAction{Action: "retry", AllowActiveGrantRevocation: *allow}
	if action == "acknowledge-manual" {
		request.Action = "acknowledge_manual"
	}
	out, err := client.RecoverMCPOAuthRetirement(ctx, *id, request)
	if err != nil {
		return printErr("MCP retirement", errors.New("could not recover retirement; inspect its state and retention deadline"))
	}
	if jsonOutput {
		return writeJSONStdout(out)
	}
	writeMCPOAuthRetirement(out)
	return 0
}
func writeMCPOAuthRetirement(record api.MCPOAuthRetirement) {
	_, _ = fmt.Fprintf(osStdout, "%s [%s/%s]: %s (%s)\n", record.ID, record.AppSlug, record.Role, record.State, record.Reason)
	_, _ = fmt.Fprintf(osStdout, "  Credential retention ends: %s\n", record.RetainUntil.Format(time.RFC3339))
	if record.State == "held" {
		_, _ = fmt.Fprintln(osStdout, "  Matching active connection: wait for disconnection, or retry with --allow-active-grant-revocation after reviewing provider behavior.")
	}
	if record.State == "manual_required" {
		_, _ = fmt.Fprintln(osStdout, "  Revoke at the provider if necessary, then acknowledge-manual; retries require retained credentials and a configured endpoint.")
	}
}
