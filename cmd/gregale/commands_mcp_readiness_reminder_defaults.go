package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPReadinessReminderDefaults(args []string) int {
	if len(args) == 0 || args[0] != "get" && args[0] != "set" && args[0] != "disable" {
		return printErr("MCP reminder defaults", errors.New("use reminder-defaults get, set or disable"))
	}
	fs := newFlagSet("mcp-reminder-defaults-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	rule := fs.String("rule", "", "readiness alert rule UUID")
	version := fs.Int64("expected-version", -1, "current reminder defaults version")
	interval := fs.Int("interval-seconds", 0, "reminder interval, 60–86400 seconds")
	maximum := fs.Int("max-reminders", 0, "lifetime reminder budget for each new incident, 1–10")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	policy := api.MCPReadinessReminderPolicy{IntervalSeconds: *interval, MaxReminders: *maximum}
	request := api.SetMCPReadinessReminderDefaultsRequest{ExpectedVersion: version, ReminderPolicy: &policy}
	invalid := fs.NArg() != 0 || !api.ValidAppSlug(*app) || uuid.Validate(*rule) != nil
	fs.Visit(func(f *flag.Flag) {
		allowed := f.Name == "app" || f.Name == "rule" || args[0] != "get" && f.Name == "expected-version" || args[0] == "set" && (f.Name == "interval-seconds" || f.Name == "max-reminders")
		if !allowed {
			invalid = true
		}
	})
	if args[0] != "get" {
		invalid = invalid || api.ValidateMCPReadinessReminderDefaultsRequest(request) != nil || args[0] == "set" && policy.MaxReminders == 0
	}
	if invalid {
		return printErr("MCP reminder defaults", errors.New("supply app and rule; writes require expected-version; set requires interval-seconds and max-reminders"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("MCP reminder defaults", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out api.MCPReadinessReminderDefaults
	if args[0] == "get" {
		out, err = client.GetMCPReadinessReminderDefaults(ctx, *app, *rule)
	} else {
		out, err = client.SetMCPReadinessReminderDefaults(ctx, *app, *rule, request)
	}
	if err != nil {
		return printErr("MCP reminder defaults", err)
	}
	return writeJSONStdout(out)
}
