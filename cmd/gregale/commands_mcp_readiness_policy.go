package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Policy files contain references and catalog expectations, never probe tokens.
func cmdMCPReadinessPolicy(args []string) int {
	if len(args) > 0 && (args[0] == "history" || args[0] == "diff" || args[0] == "restore") {
		return cmdMCPReadinessPolicyHistory(args)
	}
	if len(args) == 0 || args[0] != "get" && args[0] != "set" {
		return printErr("MCP readiness policy", errors.New("use policy get, set, history, diff or restore"))
	}
	fs := newFlagSet("mcp-readiness-policy-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	scope := fs.String("scope", api.DefaultEnvScope, "readiness environment")
	file := fs.String("file", "", "reviewed policy JSON, including expected_revision and reason")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	if fs.NArg() != 0 || !api.ValidAppSlug(*app) || api.ValidateScope(*scope) != nil || args[0] == "get" && *file != "" || args[0] == "set" && *file == "" {
		return printErr("MCP readiness policy", errors.New("supply --app, a valid --scope and --file for set"))
	}
	var request api.SetMCPReadinessPolicyRequest
	if args[0] == "set" {
		f, e := openCustomerFile(*file)
		if e != nil {
			return printErr("MCP readiness policy", e)
		}
		payload, e := io.ReadAll(io.LimitReader(f, (64<<10)+1))
		closeErr := f.Close()
		if e != nil {
			return printErr("MCP readiness policy", e)
		}
		if closeErr != nil {
			return printErr("MCP readiness policy", closeErr)
		}
		if len(payload) > 64<<10 {
			return printErr("MCP readiness policy", errors.New("policy JSON exceeds 64 KiB"))
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		e = decoder.Decode(&request)
		if e == nil && decoder.Decode(new(any)) != io.EOF {
			e = errors.New("one policy JSON object is required")
		}
		if e != nil {
			return printErr("MCP readiness policy", e)
		}
		if request.Scope != "" && request.Scope != *scope {
			return printErr("MCP readiness policy", errors.New("policy scope must match --scope"))
		}
		request.Scope = *scope
		if e = api.ValidateMCPReadinessPolicy(request); e != nil {
			return printErr("MCP readiness policy", e)
		}
	}
	client, e := authedClient()
	if e != nil {
		return printErr("MCP readiness policy", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out api.MCPReadinessPolicy
	if args[0] == "get" {
		out, e = client.GetMCPReadinessPolicy(ctx, *app, *scope)
	} else {
		out, e = client.SetMCPReadinessPolicy(ctx, *app, request)
	}
	if e != nil {
		return printErr("MCP readiness policy", e)
	}
	return writeJSONStdout(out)
}
