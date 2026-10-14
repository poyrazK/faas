package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func cmdMCPConnectionHealth(args []string) int {
	fs := newFlagSet("mcp-health", flag.ContinueOnError)
	app := fs.String("app", "", "filter one app; omit for the account fleet")
	offset := fs.Int("offset", 0, "issue page offset")
	timeout := fs.Duration("timeout", 30*time.Second, "API deadline")
	if fs.Parse(args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *offset < 0 || *offset > api.MCPConnectionHealthMaxOffset || *timeout <= 0 || *timeout > time.Duration(api.MCPCatalogOAuthFlowSeconds)*time.Second {
		return printErr("MCP health", errors.New("invalid flags"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := client.GetMCPConnectionHealth(ctx, *app, *offset)
	if err != nil {
		return printErr("MCP health", errors.New("could not read connection health; administrator access is required"))
	}
	if jsonOutput {
		return writeJSONStdout(result)
	}
	return writeMCPConnectionHealth(result)
}
func writeMCPConnectionHealth(result api.MCPConnectionHealthSnapshot) int {
	_, _ = fmt.Fprintf(osStdout, "MCP connection health: %d tracked issues\n", result.TotalIssues)
	if !result.Complete {
		_, _ = fmt.Fprintln(osStdout, "Inventory is incomplete; filter with --app to inspect a smaller scope.")
	}
	for _, issue := range result.Items {
		_, _ = fmt.Fprintf(osStdout, "  %s/%s: %s [%s]\n    Recovery: %s — %s\n", issue.AppSlug, issue.Role, issue.Kind, issue.State, issue.RecoveryAction, issue.RecoveryPath)
		if issue.RetirementID != "" {
			_, _ = fmt.Fprintf(osStdout, "    Retirement: %s\n", issue.RetirementID)
		}
	}
	if result.NextOffset > 0 {
		_, _ = fmt.Fprintf(osStdout, "Next page: --offset %d\n", result.NextOffset)
	}
	return 0
}
