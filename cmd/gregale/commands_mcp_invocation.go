package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// This worker consumes a native queued receipt. It does not create a grant,
// accept a URL, or resolve latest. Arguments arrive on stdin, never argv.
func cmdMCPInvocationRun(args []string) int {
	fs := newFlagSet("mcp-invocation-run", flag.ContinueOnError)
	id := fs.String("id", "", "native invocation UUID")
	delegated := fs.Bool("delegated", false, "use the current connection's exact native invocation grant")
	tokenEnv := fs.String("token-env", "", "named MCP_* application OAuth token")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *id == "" || *delegated && *tokenEnv != "" {
		return printErr("MCP invocation", errors.New("--id is required; argument JSON is read from stdin"))
	}
	body, err := io.ReadAll(io.LimitReader(osStdin, api.ToolInvocationArgsMaxBytes+1))
	if err != nil {
		return printErr("MCP invocation arguments", errors.New("could not read bounded argument JSON"))
	}
	native, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	// An unavailable named token is a known preflight failure, recorded by
	// the runner before it can acquire dispatch authority.
	token, _ := mcpToken(*tokenEnv)
	record, err := mcphosting.RunRegisteredInvocation(context.Background(), mcphosting.RegisteredInvocationOptions{Delegated: *delegated, Native: native, ID: *id, Arguments: body, TokenEnv: *tokenEnv, Token: token,
		VerifyPin: func(ctx context.Context, target api.ToolInvocationTarget, endpoint string) error {
			health := probeMCPCatalogDeployment(ctx, native, target.App, target.DeploymentID, endpoint, 2*time.Second)
			if !health.Supported || !health.Healthy || health.CatalogSHA256 != target.CatalogSHA256 {
				return errors.New("registered deployment catalog pin unavailable or changed")
			}
			return nil
		}})
	if err != nil {
		return printErr("MCP invocation", errors.New("invocation could not be finalized; inspect its native receipt before starting work"))
	}
	return jsonOut(writeJSON(record))
}
