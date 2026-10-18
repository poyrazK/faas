package main

import (
	"context"
	"errors"
	"flag"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"os"
	"time"
)

func cmdMCPReadinessPagerDuty(args []string) int {
	if len(args) > 0 && args[0] == "acknowledgements" {
		return cmdMCPReadinessPagerDutyAcknowledgements(args[1:])
	}
	if len(args) == 0 || args[0] != "get" && args[0] != "set" && args[0] != "disable" {
		return printErr("MCP PagerDuty", errors.New("use pagerduty get, set or disable"))
	}
	fs := newFlagSet("mcp-pagerduty-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	rule := fs.String("rule", "", "readiness alert rule UUID")
	version := fs.Int64("expected-version", -1, "current PagerDuty policy version")
	keyEnv := fs.String("key-env", "", "environment variable holding the PagerDuty integration key")
	region := fs.String("region", "", "PagerDuty Events API region: us (default) or eu; set only")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	enabled := args[0] == "set"
	request := api.SetMCPReadinessPagerDutyRequest{ExpectedVersion: version, Enabled: &enabled}
	if enabled {
		request.IntegrationKey = os.Getenv(*keyEnv)
		request.Region = *region
	}
	invalid := fs.NArg() != 0 || !api.ValidAppSlug(*app) || uuid.Validate(*rule) != nil
	fs.Visit(func(f *flag.Flag) {
		allowed := f.Name == "app" || f.Name == "rule" || args[0] != "get" && f.Name == "expected-version" || enabled && (f.Name == "key-env" || f.Name == "region")
		if !allowed {
			invalid = true
		}
	})
	if args[0] != "get" {
		invalid = invalid || api.ValidateMCPReadinessPagerDutyRequest(request) != nil
	}
	if invalid {
		return printErr("MCP PagerDuty", errors.New("supply app and rule; writes require expected-version; set requires key-env containing a valid integration key and optional region us or eu"))
	}
	c, e := authedClient()
	if e != nil {
		return printErr("MCP PagerDuty", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out api.MCPReadinessPagerDutyPolicy
	if args[0] == "get" {
		out, e = c.GetMCPReadinessPagerDuty(ctx, *app, *rule)
	} else {
		out, e = c.SetMCPReadinessPagerDuty(ctx, *app, *rule, request)
	}
	if e != nil {
		return printErr("MCP PagerDuty", e)
	}
	return writeJSONStdout(out)
}
