package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func cmdMCPComplete(args []string) int {
	fs := newFlagSet("mcp-complete", flag.ContinueOnError)
	endpoint := fs.String("url", "", "full MCP endpoint URL")
	app := fs.String("app", "", "Gregale app slug; resolves its public URL")
	path := fs.String("endpoint", "/mcp", "endpoint path with --app (default /mcp)")
	tokenEnv := fs.String("token-env", "", "environment variable containing an MCP client token")
	legacy := fs.Bool("legacy", false, "use protocol 2025-11-25")
	prompt := fs.String("prompt", "", "prompt name to complete")
	resourceTemplate := fs.String("resource-template", "", "resource URI template to complete")
	argument := fs.String("argument", "", "prompt argument or template variable name")
	value := fs.String("value", "", "partial value; an empty value requests the first suggestions")
	contextInline := fs.String("context", "", "previously resolved arguments as a JSON object")
	contextFile := fs.String("context-file", "", "read previous arguments from a JSON file")
	timeout := fs.Duration("timeout", 30*time.Second, "total completion request timeout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	valueSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "value" {
			valueSet = true
		}
	})
	if fs.NArg() != 0 || *timeout <= 0 {
		return printErr("Invalid MCP completion flags", errors.New("unexpected positional arguments or nonpositive timeout"))
	}
	if (*prompt == "") == (*resourceTemplate == "") || *argument == "" || !valueSet {
		return printErr("Invalid MCP completion flags", errors.New("choose exactly one of --prompt or --resource-template, and provide --argument and --value"))
	}
	contextValues, err := mcpArguments(*contextInline, *contextFile)
	if err != nil {
		return printErr("MCP completion context", err)
	}
	contextArguments := make(map[string]string, len(contextValues))
	for key, item := range contextValues {
		text, ok := item.(string)
		if !ok {
			return printErr("MCP completion context", fmt.Errorf("argument %q must be a string", key))
		}
		contextArguments[key] = text
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	resolved, err := mcpEndpoint(ctx, *endpoint, *app, *path)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	version := mcphosting.ProtocolVersion
	if *legacy {
		version = mcphosting.LegacyProtocolVersion
	}
	client, err := mcphosting.NewClient(resolved, token, version)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	client.HTTP.Timeout = *timeout
	if err := client.Initialize(ctx); err != nil {
		return printErr("MCP initialize", err)
	}
	reference := mcphosting.CompletionReference{Type: "ref/prompt", Name: *prompt}
	if *resourceTemplate != "" {
		reference = mcphosting.CompletionReference{Type: "ref/resource", URI: *resourceTemplate}
	}
	suggestions, err := client.Complete(ctx, reference, *argument, *value, contextArguments)
	if err != nil {
		return printErr("MCP completion", err)
	}
	return jsonOut(writeJSON(struct {
		Completion mcphosting.CompletionSuggestions `json:"completion"`
	}{Completion: suggestions}))
}
