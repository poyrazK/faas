package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPReadinessFallback(args []string) int {
	if len(args) == 0 || args[0] != "get" && args[0] != "set" && args[0] != "disable" {
		return printErr("MCP fallback", errors.New("use fallback get, set or disable"))
	}
	fs := newFlagSet("mcp-fallback-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	rule := fs.String("rule", "", "readiness alert rule UUID")
	version := fs.Int64("expected-version", -1, "current fallback policy version")
	provider := fs.String("provider", "", "fallback provider: webhook (default) or pagerduty")
	region := fs.String("region", "", "PagerDuty fallback region: us (default) or eu")
	keyEnv := fs.String("key-env", "", "environment variable containing PagerDuty fallback integration key")
	target := fs.String("webhook-url", "", "public fallback HTTP(S) receiver")
	secretEnv := fs.String("secret-env", "", "environment variable containing fallback signing secret")
	failures := fs.Int("after-failures", 0, "completed failed primary dispatches before failover, 1–10")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	enabled := args[0] == "set"
	req := api.SetMCPReadinessFallbackRequest{Provider: *provider, Region: *region, ExpectedVersion: version, Enabled: &enabled, WebhookURL: *target, AfterFailures: *failures}
	if enabled {
		if *provider == "pagerduty" {
			req.IntegrationKey = os.Getenv(*keyEnv)
		} else {
			req.WebhookSecret = os.Getenv(*secretEnv)
		}
	}
	invalid := fs.NArg() != 0 || !api.ValidAppSlug(*app) || uuid.Validate(*rule) != nil
	fs.Visit(func(f *flag.Flag) {
		allowed := f.Name == "app" || f.Name == "rule" || args[0] != "get" && f.Name == "expected-version" || enabled && (f.Name == "provider" || f.Name == "after-failures" || *provider == "pagerduty" && (f.Name == "region" || f.Name == "key-env") || *provider != "pagerduty" && (f.Name == "webhook-url" || f.Name == "secret-env"))
		if !allowed {
			invalid = true
		}
	})
	if args[0] != "get" {
		invalid = invalid || api.ValidateMCPReadinessFallbackRequest(req) != nil
	}
	if invalid {
		return printErr("MCP fallback", errors.New("supply app and rule; writes require expected-version; set requires after-failures plus webhook-url/secret-env or provider=pagerduty/key-env/region"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("MCP fallback", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out api.MCPReadinessFallbackPolicy
	if args[0] == "get" {
		out, err = client.GetMCPReadinessFallback(ctx, *app, *rule)
	} else {
		out, err = client.SetMCPReadinessFallback(ctx, *app, *rule, req)
	}
	if err != nil {
		return printErr("MCP fallback", err)
	}
	return writeJSONStdout(out)
}
