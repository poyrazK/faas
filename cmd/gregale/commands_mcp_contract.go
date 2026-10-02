package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func cmdMCPLock(args []string) int {
	fs := newFlagSet("mcp-lock", flag.ContinueOnError)
	endpoint := fs.String("url", "", "full MCP endpoint URL")
	app := fs.String("app", "", "Gregale app slug")
	path := fs.String("endpoint", "/mcp", "endpoint path with --app")
	tokenEnv := fs.String("token-env", "", "environment variable containing a client token")
	legacy := fs.Bool("legacy", false, "use protocol 2025-11-25")
	output := fs.String("out", mcphosting.ContractFile, "contract snapshot destination")
	force := fs.Bool("force", false, "replace an existing regular snapshot file")
	timeout := fs.Duration("timeout", 30*time.Second, "total discovery timeout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *timeout <= 0 || *output == "" {
		return printErr("MCP lock", errors.New("unexpected positional arguments, empty output path or nonpositive timeout"))
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
	c, err := mcphosting.NewClient(resolved, token, version)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	c.HTTP.Timeout = *timeout
	return captureMCPContract(ctx, c, *output, *force)
}

func captureMCPContract(ctx context.Context, c *mcphosting.Client, path string, force bool) int {
	if err := c.Initialize(ctx); err != nil {
		return printErr("MCP initialize", err)
	}
	tools, discovery, err := c.Tools(ctx)
	if err != nil {
		return printErr("MCP contract discovery", err)
	}
	if len(discovery.RejectedTools) != 0 {
		if code := jsonOut(writeJSON(map[string]any{"written": false, "error": "discovery rejected tool definitions; fix them before capturing a complete contract", "rejected_tools": discovery.RejectedTools})); code != 0 {
			return code
		}
		return 1
	}
	contract, err := mcphosting.NewContract(c.Version, tools)
	if err != nil {
		return printErr("MCP contract", err)
	}
	body, err := mcphosting.MarshalContract(contract)
	if err != nil {
		return printErr("MCP contract", err)
	}
	if err := writeMCPContract(path, body, force); err != nil {
		return printErr("MCP lock file", err)
	}
	return jsonOut(writeJSON(map[string]any{"written": true, "path": path, "tools": len(tools), "protocol_version": c.Version}))
}

func cmdMCPDiff(args []string) int {
	fs := newFlagSet("mcp-diff", flag.ContinueOnError)
	before := fs.String("before", "", "baseline MCP contract snapshot")
	after := fs.String("after", "", "candidate MCP contract snapshot")
	check := fs.Bool("check", false, "fail on breaking changes or changes needing review")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *before == "" || *after == "" {
		return printErr("MCP diff", errors.New("--before and --after are required; no positional arguments"))
	}
	b, err := readMCPContract(*before)
	if err != nil {
		return printErr("MCP baseline contract", err)
	}
	a, err := readMCPContract(*after)
	if err != nil {
		return printErr("MCP candidate contract", err)
	}
	diff, err := mcphosting.CompareContracts(b, a)
	if err != nil {
		return printErr("MCP contract comparison", err)
	}
	if code := jsonOut(writeJSON(diff)); code != 0 {
		return code
	}
	if *check && !diff.Compatible {
		return 1
	}
	return 0
}

func readMCPContract(path string) (mcphosting.Contract, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return mcphosting.Contract{}, err
	}
	defer func() { _ = f.Close() }()
	return mcphosting.ReadContract(f)
}

// Publish a complete private snapshot atomically. Link without replacement is
// race safe; force replaces the directory entry and never follows its target.
func writeMCPContract(path string, data []byte, force bool) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("snapshot destination must be a regular file")
		}
		if !force {
			return fmt.Errorf("snapshot exists; use a new --out path or --force")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect snapshot destination: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gregale-mcp-lock-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close snapshot: %w", err)
	}
	if force {
		if err := os.Rename(tmp.Name(), path); err != nil {
			return fmt.Errorf("replace snapshot: %w", err)
		}
	} else if err := os.Link(tmp.Name(), path); err != nil {
		return fmt.Errorf("publish snapshot without replacement: %w", err)
	}
	return nil
}
