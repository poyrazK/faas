package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func cmdMCPValidate(args []string) int {
	fs := newFlagSet("mcp-validate", flag.ContinueOnError)
	app := fs.String("app", "", "validate running deployment replicas through the existing exact-instance API")
	deployment := fs.String("deployment", "", "deployment ID (required with --app)")
	endpointPath := fs.String("endpoint-path", "/mcp", "configured endpoint path for exact-instance validation")
	endpoint := fs.String("url", "", "local or public MCP endpoint")
	tokenEnv := fs.String("token-env", "", "environment variable containing an MCP bearer token")
	version := fs.String("protocol-version", mcphosting.ProtocolVersion, "protocol version to validate")
	timeout := fs.Duration("timeout", 30*time.Second, "overall validation deadline")
	if fs.Parse(args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *endpoint == "" || *timeout <= 0 || *timeout > time.Hour || !api.ValidMCPProbeProtocolVersion(*version) {
		return printErr("MCP validate", errors.New("--url, a supported protocol version and a positive timeout up to 1h are required"))
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP validate", err)
	}
	if (*app == "") != (*deployment == "") {
		return printErr("MCP validate", errors.New("--app and --deployment must be supplied together"))
	}
	if *app != "" {
		client, e := authedClient()
		if e != nil {
			return printErr("Not logged in", e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		role := mcpReleaseRole{Name: "default", token: token, contract: mcphosting.Contract{ProtocolVersion: *version}}
		result := qualifyMCPProtocolDeployment(ctx, client, *app, *deployment, *endpoint, *endpointPath, []mcpReleaseRole{role}, *timeout)
		if code := writeJSONStdout(result); code != 0 {
			return code
		}
		if !result.Qualified {
			return 1
		}
		return 0
	}
	report, err := validateMCPEndpoint(context.Background(), *endpoint, token, *version, *timeout)
	if err != nil {
		return printErr("MCP validate", err)
	}
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

func validateMCPEndpoint(ctx context.Context, endpoint, token, version string, timeout time.Duration) (mcphosting.ValidationReport, error) {
	client, err := mcphosting.NewClient(endpoint, token, version)
	if err != nil {
		return mcphosting.ValidationReport{}, err
	}
	transport := &mcpPublicProtocolRoundTripper{next: http.DefaultTransport, version: version}
	client.HTTP = &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	report := mcphosting.ValidateCompatibility(ctx, client, nil)
	check := mcphosting.Check{Name: "transport_headers", Status: "passed"}
	if !report.OK {
		check.Status = "skipped"
		check.Detail = "Catalog validation did not complete."
	}
	if transport.protocolErr != nil {
		report.OK = false
		check.Status = "failed"
		check.Detail = "Successful responses must use the selected MCP protocol version and omit stateful session headers."
	}
	report.Checks = append(report.Checks, check)
	return report, nil
}
