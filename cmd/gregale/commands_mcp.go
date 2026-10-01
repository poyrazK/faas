package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func cmdMCP(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale mcp init|deploy|doctor|tools|call|config [flags]", "mcp")
		return 1
	}
	switch args[0] {
	case "init":
		return cmdMCPInit(args[1:])
	case "deploy":
		return cmdMCPDeploy(args[1:])
	case "doctor", "tools", "call", "config":
		return cmdMCPRemote(args[0], args[1:])
	default:
		return printErr("Unknown MCP command", fmt.Errorf("%q", args[0]))
	}
}

func cmdMCPInit(args []string) int {
	fs := newFlagSet("mcp-init", flag.ContinueOnError)
	dir := fs.String("path", "", "empty destination directory")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *dir == "" {
		return printErr("MCP init", errors.New("--path is required"))
	}
	return cmdInit([]string{"--template", "mcp-node", "--path", *dir})
}

func mcpToken(env string) (string, error) {
	if env == "" {
		return "", nil
	}
	token, ok := os.LookupEnv(env)
	if !ok || token == "" {
		return "", fmt.Errorf("client token environment variable %q is empty", env)
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", errors.New("MCP client token contains a newline")
	}
	return token, nil
}

func mcpArguments(inline, file string) (map[string]any, error) {
	if inline != "" && file != "" {
		return nil, errors.New("--arguments and --arguments-file are mutually exclusive")
	}
	var r io.Reader = bytes.NewBufferString(inline)
	if file != "" {
		f, err := openCustomerFile(file)
		if err != nil {
			return nil, fmt.Errorf("read tool arguments: %w", err)
		}
		defer func() { _ = f.Close() }()
		r = f
	} else if inline == "" {
		return map[string]any{}, nil
	}
	body, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read tool arguments: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, errors.New("tool arguments exceed 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var args map[string]any
	if err := d.Decode(&args); err != nil {
		return nil, fmt.Errorf("tool arguments must be a JSON object: %w", err)
	}
	if args == nil {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("tool arguments must contain one JSON object")
	}
	return args, nil
}

func mcpEndpoint(ctx context.Context, endpoint, app, path string) (string, error) {
	if endpoint != "" && app != "" {
		return "", errors.New("choose --url or --app")
	}
	if endpoint != "" {
		return endpoint, nil
	}
	if app == "" {
		return "", errors.New("--url or --app is required")
	}
	c, err := authedClient()
	if err != nil {
		return "", err
	}
	metadata, err := c.GetApp(ctx, app)
	if err != nil {
		return "", err
	}
	return (mcphosting.Config{Endpoint: path}).URL(canonicalAppURL(metadata))
}

func cmdMCPRemote(command string, args []string) int {
	fs := newFlagSet("mcp-"+command, flag.ContinueOnError)
	endpoint := fs.String("url", "", "full MCP endpoint URL")
	app := fs.String("app", "", "Gregale app slug; resolves its public URL")
	path := fs.String("endpoint", "/mcp", "endpoint path when using --app")
	tokenEnv := fs.String("token-env", "", "environment variable containing an MCP client access token")
	legacy := fs.Bool("legacy", false, "also check legacy compatibility (doctor), or use protocol 2025-11-25")
	tool := fs.String("tool", "", "tool to call")
	streamTool := fs.String("stream-tool", "", "explicitly execute this tool to verify live progress (doctor)")
	inline := fs.String("arguments", "", "tool arguments as a JSON object")
	file := fs.String("arguments-file", "", "read tool arguments from a JSON file")
	name := fs.String("name", "gregale", "connection name (config)")
	timeout := fs.Duration("timeout", 30*time.Second, "total diagnostic timeout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		return printErr("Invalid MCP flags", errors.New("unexpected positional arguments or nonpositive timeout"))
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
	if *legacy && command != "doctor" {
		version = mcphosting.LegacyProtocolVersion
	}
	c, err := mcphosting.NewClient(resolved, token, version)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	c.HTTP.Timeout = *timeout
	arguments, err := mcpArguments(*inline, *file)
	if err != nil {
		return printErr("MCP arguments", err)
	}
	return runMCPRemote(ctx, command, c, *legacy, *tool, *streamTool, *name, arguments)
}

func runMCPRemote(ctx context.Context, command string, c *mcphosting.Client, legacy bool, tool, streamTool, name string, args map[string]any) int {
	if command == "config" {
		return jsonOut(writeJSON(mcphosting.ConnectionConfig(name, c.Endpoint)))
	}
	if command == "doctor" {
		report := mcphosting.Doctor(ctx, c, legacy, streamTool, args)
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
		if !report.OK {
			return 1
		}
		return 0
	}
	if err := c.Initialize(ctx); err != nil {
		return printErr("MCP initialize", err)
	}
	tools, _, err := c.Tools(ctx)
	if err != nil {
		return printErr("MCP tool discovery", err)
	}
	if command == "tools" {
		return jsonOut(writeJSON(map[string]any{"tools": tools}))
	}
	if tool == "" {
		return printErr("MCP call", errors.New("--tool is required"))
	}
	for _, candidate := range tools {
		if candidate.Name != tool {
			continue
		}
		x, err := c.Call(ctx, candidate, args, true)
		if x.Result != nil {
			if code := jsonOut(writeJSON(x)); code != 0 {
				return code
			}
		}
		if err != nil {
			return printErr("MCP tool call", err)
		}
		return 0
	}
	return printErr("MCP call", fmt.Errorf("tool %q is not in the server's discovered contract", tool))
}

type mcpDeployOptions struct {
	path, slug, profile, tokenEnv, secretsFile string
	timeout                                    int
}

func cmdMCPDeploy(args []string) int {
	fs := newFlagSet("mcp-deploy", flag.ContinueOnError)
	var o mcpDeployOptions
	fs.StringVar(&o.path, "path", ".", "source directory; uploads the current worktree")
	fs.StringVar(&o.slug, "name", "", "app slug")
	fs.StringVar(&o.profile, "profile", "", "app resource profile")
	fs.StringVar(&o.tokenEnv, "token-env", "", "MCP client token for post-deployment verification")
	fs.StringVar(&o.secretsFile, "secrets-file", "", "sealed app secrets configured before deployment")
	fs.IntVar(&o.timeout, "timeout", 1200, "deployment wait timeout in seconds")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || o.slug == "" || o.timeout <= 0 {
		return printErr("Invalid MCP deploy", errors.New("--name and a positive --timeout are required"))
	}
	return runMCPDeploy(o)
}

func runMCPDeploy(o mcpDeployOptions) int {
	dir, err := filepath.Abs(o.path)
	if err != nil {
		return printErr("MCP source", err)
	}
	cfg, err := mcphosting.Load(dir)
	if err != nil {
		return printErr("MCP configuration", err)
	}
	token, err := mcpToken(o.tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	if cfg.Auth.Mode == "external-oauth" && token == "" {
		return printErr("MCP verification", errors.New("external-oauth deploy requires --token-env with a client access token for verification"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	account, err := c.Whoami(ctx)
	if err != nil {
		return printErr("MCP plan check", err)
	}
	if !api.Plan(account.Plan).StreamingEnabled() {
		return printErr("MCP streaming unavailable", errors.New("the stateless MCP hosting profile requires a plan that supports streaming"))
	}
	if !jsonOutput {
		PrintProgress(osStdout, "Deploying stateless MCP (%s client access)", cfg.Auth.Mode)
	}
	deployArgs := []string{"--path", dir, "--source=worktree", "--name", o.slug, "--app", "--wait", "--timeout", strconv.Itoa(o.timeout)}
	if o.profile != "" {
		deployArgs = append(deployArgs, "--profile", o.profile)
	}
	if o.secretsFile != "" {
		deployArgs = append(deployArgs, "--secrets-file", o.secretsFile)
	}
	// Preserve the existing ingress gate until the new app passes readiness.
	code := quietMCPDeploy(deployArgs)
	if code != 0 {
		return code
	}
	return finishMCPDeploy(ctx, c, o.slug, cfg, token)
}

func quietMCPDeploy(args []string) int {
	old := osStdout
	osStdout = io.Discard
	defer func() { osStdout = old }()
	return cmdDeployTarball(args)
}

func finishMCPDeploy(ctx context.Context, c *Client, slug string, cfg mcphosting.Config, token string) int {
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		return printErr("MCP app lookup", err)
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	if cfg.Auth.Mode == "external-oauth" && cfg.Auth.Resource != endpoint {
		return printErr("MCP audience mismatch", fmt.Errorf("auth.resource must match the app's canonical endpoint %s", endpoint))
	}
	if app.MaintenanceMode {
		return printErr("MCP app in maintenance", errors.New("review the failure and clear maintenance with `gregale app <slug> --no-maintenance` before activating a deployment"))
	}
	enabled, require := true, false
	_, err = c.UpdateApp(ctx, slug, api.UpdateAppRequest{StreamingEnabled: &enabled, RequireAuthn: &require, PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}})
	if err != nil {
		return printErr("MCP ingress configuration", err)
	}
	probe, err := mcphosting.NewClient(endpoint, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	if cfg.Auth.Mode == "external-oauth" {
		probe.ExpectedAuth = &cfg.Auth
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	report := mcphosting.Doctor(probeCtx, probe, cfg.Legacy, "", nil)
	cancel()
	receipt := map[string]any{"app": slug, "endpoint": endpoint, "auth_mode": cfg.Auth.Mode, "verification": report, "connection": mcphosting.ConnectionConfig(slug, endpoint)}
	if !report.OK {
		maintenance := true
		_, closeErr := c.UpdateApp(ctx, slug, api.UpdateAppRequest{MaintenanceMode: &maintenance})
		if closeErr == nil {
			receipt["verification_failure_action"] = "maintenance_enabled"
		} else {
			receipt["verification_failure_action"] = "maintenance_unconfirmed"
			receipt["recovery_command"] = "gregale app " + slug + " --maintenance"
		}
	}
	if code := jsonOut(writeJSON(receipt)); code != 0 {
		return code
	}
	if !report.OK {
		return 1
	}
	return 0
}
