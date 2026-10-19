package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPReadinessNotifications(args []string) int {
	if len(args) == 0 || args[0] != "list" && args[0] != "retry" && args[0] != "abandon" {
		return printErr("MCP notifications", errors.New("use notifications list, retry or abandon"))
	}
	fs := newFlagSet("mcp-notifications-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "owning app slug")
	scope := fs.String("scope", "", "environment filter; includes all-environment rules")
	after := fs.String("after", "", "rule UUID cursor")
	limit := fs.Int("limit", 20, "page size 1–50")
	destination := fs.String("destination", "", "receiver queue: primary or fallback; omit for the whole event")
	event := fs.String("event", "", "head event UUID")
	version := fs.Int64("expected-version", -1, "delivery version from current queue health")
	request := fs.String("request-id", "", "stable request UUID; reuse on retries")
	reason := fs.String("reason", "", "operator reason, 1–500 bytes")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	invalid := fs.NArg() != 0 || !api.ValidAppSlug(*app)
	allowed := map[string]bool{"app": true}
	req := api.MCPReadinessNotificationAction{Destination: *destination, RequestID: *request, EventID: *event, Action: args[0], ExpectedVersion: version, Reason: *reason}
	if args[0] == "list" {
		for _, k := range []string{"scope", "after", "limit"} {
			allowed[k] = true
		}
		invalid = invalid || *scope != "" && api.ValidateScope(*scope) != nil || *after != "" && uuid.Validate(*after) != nil || *limit < 1 || *limit > 50
	} else {
		for _, k := range []string{"event", "expected-version", "request-id", "reason", "destination"} {
			allowed[k] = true
		}
		invalid = invalid || api.ValidateMCPReadinessNotificationAction(req) != nil
	}
	fs.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			invalid = true
		}
	})
	if invalid {
		return printErr("MCP notifications", errors.New("supply a valid app and list filters, or an event, expected version, stable request ID and reason for an action"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("MCP notifications", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out any
	if args[0] == "list" {
		out, err = client.ListMCPReadinessNotificationQueues(ctx, *app, *scope, *after, *limit)
	} else {
		out, err = client.ActMCPReadinessNotification(ctx, *app, req)
	}
	if err != nil {
		return printErr("MCP notifications", err)
	}
	return writeJSONStdout(out)
}
