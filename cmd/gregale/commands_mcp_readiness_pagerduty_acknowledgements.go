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

func cmdMCPReadinessPagerDutyAcknowledgements(args []string) int {
	if len(args) == 0 || args[0] != "get" && args[0] != "set" && args[0] != "disable" && args[0] != "events" {
		return printErr("MCP PagerDuty acknowledgements", errors.New("use acknowledgements get, set, disable or events"))
	}
	fs := newFlagSet("mcp-pagerduty-acknowledgements-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	rule := fs.String("rule", "", "readiness alert rule UUID")
	version := fs.Int64("expected-version", -1, "current webhook policy version")
	destination := fs.String("destination", "", "PagerDuty destination: primary (default) or fallback")
	fallback := fs.Int64("expected-fallback-version", 0, "current native PagerDuty fallback policy version")
	syncRelease := fs.Bool("sync-unacknowledgements", false, "resume reminders after matching PagerDuty unacknowledgements (opt-in)")
	primary := fs.Int64("expected-pagerduty-version", 0, "current native PagerDuty policy version")
	service := fs.String("service-id", "", "PagerDuty service ID for this integration")
	secretEnv := fs.String("secret-env", "", "environment variable holding the webhook subscription signing secret")
	before := fs.String("before", "", "opaque provider event cursor")
	limit := fs.Int("limit", api.MCPReadinessPagerDutyWebhookPageDefault, "event page size")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	enabled := args[0] == "set"
	write := enabled || args[0] == "disable"
	req := api.SetMCPReadinessPagerDutyAcknowledgementsRequest{Destination: *destination, ExpectedVersion: version, Enabled: &enabled}
	if enabled {
		req.SyncUnacknowledgements = *syncRelease
		req.Destination = *destination
		if *destination == "fallback" {
			req.ExpectedFallbackVersion = fallback
		} else {
			req.ExpectedPagerDutyVersion = primary
		}
		req.ServiceID = *service
		req.SigningSecret = os.Getenv(*secretEnv)
	}
	invalid := (*destination != "" && *destination != "primary" && *destination != "fallback") || fs.NArg() != 0 || !api.ValidAppSlug(*app) || uuid.Validate(*rule) != nil
	fs.Visit(func(f *flag.Flag) {
		allowed := f.Name == "app" || f.Name == "rule" || write && f.Name == "expected-version" || args[0] != "events" && f.Name == "destination" || enabled && (f.Name == "sync-unacknowledgements" || f.Name == "destination" || *destination == "fallback" && f.Name == "expected-fallback-version" || *destination != "fallback" && f.Name == "expected-pagerduty-version" || f.Name == "service-id" || f.Name == "secret-env") || args[0] == "events" && (f.Name == "before" || f.Name == "limit")
		if !allowed {
			invalid = true
		}
	})
	if write {
		invalid = invalid || api.ValidateMCPReadinessPagerDutyAcknowledgementsRequest(req) != nil
	}
	if args[0] == "events" {
		_, _, err := api.ParseMCPReadinessIncidentCursor(*before)
		invalid = invalid || err != nil || *limit < 1 || *limit > api.MCPReadinessPagerDutyWebhookPageMax
	}
	if invalid {
		return printErr("MCP PagerDuty acknowledgements", errors.New("supply app and rule; writes require expected-version; set also requires the selected destination version, service-id and secret-env"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("MCP PagerDuty acknowledgements", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out any
	switch args[0] {
	case "get":
		out, err = c.GetMCPReadinessPagerDutyAcknowledgements(ctx, *app, *rule, *destination)
	case "events":
		out, err = c.ListMCPReadinessPagerDutyAcknowledgementEvents(ctx, *app, *rule, *before, *limit)
	default:
		out, err = c.SetMCPReadinessPagerDutyAcknowledgements(ctx, *app, *rule, req)
	}
	if err != nil {
		return printErr("MCP PagerDuty acknowledgements", err)
	}
	return writeJSONStdout(out)
}
