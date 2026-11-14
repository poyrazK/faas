package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolconnection"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

func cmdMCPToolConnection(args []string) int {
	if len(args) > 0 && (args[0] == "connect" || args[0] == "reconnect") {
		return cmdMCPToolConnectionBrowser(args)
	}
	if len(args) == 0 {
		return printErr("MCP execution connections", errors.New("use create, connect, reconnect, get, list or revoke"))
	}
	verb := args[0]
	fs := newFlagSet("mcp-tool-connection-"+verb, flag.ContinueOnError)
	var id, path, cursor string
	limit := api.ToolOAuthConnectionPageMax
	switch verb {
	case "create":
		fs.StringVar(&id, "id", "", "caller-retained connection UUID")
		fs.StringVar(&path, "config", "", "owner-reviewed execution OAuth JSON configuration; secret references only")
	case "get", "revoke":
		fs.StringVar(&id, "id", "", "exact connection UUID")
	case "list":
		fs.StringVar(&cursor, "cursor", "", "opaque continuation")
		fs.IntVar(&limit, "limit", api.ToolOAuthConnectionPageMax, "page size 1-20")
	default:
		return printErr("MCP execution connections", errors.New("use create, get, list or revoke"))
	}
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	if fs.NArg() != 0 || verb != "list" && !toolgrant.ValidID(id) || verb == "list" && (limit < 1 || limit > api.ToolOAuthConnectionPageMax || cursor != "" && !toolgrant.ValidID(cursor)) {
		return printErr("MCP execution connections", errors.New("provide a canonical UUID or valid list bounds"))
	}
	var in api.ToolOAuthConnectionIntent
	if verb == "create" {
		var err error
		in, err = readToolOAuthConfig(path)
		if err != nil {
			return printErr("Invalid execution connection", err)
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
		out, err = client.PutToolOAuthConnection(ctx, id, in)
	case "get":
		out, err = client.GetToolOAuthConnection(ctx, id)
	case "list":
		out, err = client.ListToolOAuthConnections(ctx, cursor, limit)
	case "revoke":
		out, err = client.RevokeToolOAuthConnection(ctx, id)
	}
	if err != nil {
		return printErr("MCP execution connections", err)
	}
	return jsonOut(writeJSON(out))
}

func readToolOAuthConfig(path string) (api.ToolOAuthConnectionIntent, error) {
	var out api.ToolOAuthConnectionIntent
	if path == "" {
		return out, errors.New("--config is required")
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.ToolOAuthConnectionIntentMaxBytes+1))
	if err != nil || len(raw) > api.ToolOAuthConnectionIntentMaxBytes {
		return out, errors.New("execution connection config exceeds review bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || toolconnection.ValidateIntent(out) != nil {
		return out, errors.New("invalid owner-reviewed execution OAuth config; use secret references, not credential values")
	}
	return out, nil
}
