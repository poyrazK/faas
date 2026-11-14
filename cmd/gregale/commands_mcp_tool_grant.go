package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

func cmdMCPToolGrant(args []string) int {
	if len(args) == 0 {
		return printErr("MCP tool grants", errors.New("use create, get, list or revoke"))
	}
	verb := args[0]
	fs := newFlagSet("mcp-tool-grant-"+verb, flag.ContinueOnError)
	var id, key, version, name, expiry, cursor, credential string
	var connection string
	var allow bool
	limit := api.ToolGrantPageMax
	switch verb {
	case "create":
		fs.StringVar(&id, "id", "", "caller-retained grant UUID")
		fs.StringVar(&key, "connection-key", "", "existing native agent connection key UUID")
		fs.StringVar(&version, "version", "", "exact publication version")
		fs.StringVar(&name, "tool", "", "exact published tool name")
		fs.StringVar(&expiry, "expires-at", "", "absolute RFC3339 expiry, within 30 days")
		fs.BoolVar(&allow, "allow-invocation", false, "allow exact hosted invocation, subject to connection consent")
		fs.StringVar(&credential, "credential-secret-ref", "", "existing secret:MCP_* application token to bind to this private tool grant; never a value")
		fs.StringVar(&connection, "credential-connection-id", "", "exact owner-approved execution OAuth connection UUID; exclusive with --credential-secret-ref")
	case "get", "revoke":
		fs.StringVar(&id, "id", "", "exact grant UUID")
	case "list":
		fs.StringVar(&cursor, "cursor", "", "opaque continuation")
		fs.IntVar(&limit, "limit", api.ToolGrantPageMax, "page size 1-20")
	default:
		return printErr("MCP tool grants", errors.New("use create, get, list or revoke"))
	}
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	if fs.NArg() != 0 || (verb != "list" && !toolgrant.ValidID(id)) || (verb == "list" && (limit < 1 || limit > api.ToolGrantPageMax || cursor != "" && !toolgrant.ValidID(cursor))) {
		return printErr("MCP tool grants", errors.New("provide a canonical grant UUID or valid list bounds; no positional arguments are accepted"))
	}
	var in api.ToolGrantIntent
	if verb == "create" {
		expires, err := time.Parse(time.RFC3339, expiry)
		in = api.ToolGrantIntent{KeyID: key, Version: version, Name: name, ExpiresAt: expires.UTC(), AllowInvocation: allow, CredentialSecretRef: credential, CredentialConnectionID: connection}
		if err != nil || toolgrant.ValidateIntent(in) != nil {
			return printErr("MCP tool grants", errors.New("provide an exact version/tool, connection key UUID and RFC3339 expiry"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out any
	switch verb {
	case "create":
		out, err = client.PutToolGrant(ctx, id, in)
	case "get":
		out, err = client.GetToolGrant(ctx, id)
	case "revoke":
		out, err = client.RevokeToolGrant(ctx, id)
	case "list":
		out, err = client.ListToolGrants(ctx, cursor, limit)
	}
	if err != nil {
		return printErr("MCP tool grants", err)
	}
	return jsonOut(writeJSON(out))
}
