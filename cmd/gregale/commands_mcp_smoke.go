package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpSmokeGate struct {
	suite      mcphosting.SmokeSuite
	allowTools bool
}

func loadMCPSmokeGate(path string, allowTools bool) (*mcpSmokeGate, error) {
	if path == "" {
		if allowTools {
			return nil, errors.New("--allow-tool-execution requires --fixtures")
		}
		return nil, nil
	}
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, errors.New("could not open fixture file")
	}
	defer func() { _ = file.Close() }()
	suite, err := mcphosting.LoadSmokeSuite(file)
	if err != nil {
		return nil, err
	}
	if suite.RequiresToolExecution() && !allowTools {
		return nil, errors.New("tool fixtures require --allow-tool-execution")
	}
	return &mcpSmokeGate{suite: suite, allowTools: allowTools}, nil
}
func cmdMCPSmoke(args []string) int {
	fs := newFlagSet("mcp-test", flag.ContinueOnError)
	endpoint := fs.String("url", "", "MCP endpoint under test")
	fixtures := fs.String("fixtures", "gregale-mcp.tests.json", "reviewed fixture file")
	tokenEnv := fs.String("token-env", "", "environment variable containing the bearer token")
	allowTools := fs.Bool("allow-tool-execution", false, "execute reviewed tool fixtures once; tools may have side effects")
	timeout := fs.Duration("timeout", 30*time.Second, "overall suite deadline")
	if fs.Parse(args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *endpoint == "" || *timeout <= 0 || *timeout > time.Hour {
		return printErr("MCP test", errors.New("--url and a positive timeout up to 1h are required"))
	}
	gate, err := loadMCPSmokeGate(*fixtures, *allowTools)
	if err != nil || gate == nil {
		if err == nil {
			err = errors.New("--fixtures is required")
		}
		return printErr("MCP test", err)
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP test", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report := runMCPSmokeGate(ctx, *endpoint, token, gate)
	if jsonOutput {
		if code := writeJSONStdout(report); code != 0 {
			return code
		}
	} else {
		for _, check := range report.Checks {
			_, _ = fmt.Fprintf(osStdout, "%s: %s %s\n", check.Name, check.Status, check.Detail)
		}
	}
	if !report.OK {
		return 1
	}
	return 0
}
func runMCPSmokeGate(ctx context.Context, endpoint, token string, gate *mcpSmokeGate) mcphosting.SmokeReport {
	client, err := mcphosting.NewClient(endpoint, token, mcphosting.ProtocolVersion)
	if err != nil {
		return mcphosting.SmokeReport{Checks: []mcphosting.Check{{Name: "endpoint", Status: "failed", Detail: "Invalid MCP endpoint."}}}
	}
	client.HTTP = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return mcphosting.RunSmoke(ctx, client, gate.suite, gate.allowTools)
}
