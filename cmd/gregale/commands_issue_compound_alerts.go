package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuealerts"
)

func cmdIssueCompoundAlerts(args []string) int {
	usage := "usage: gregale issues compound-alerts <get|save|remove|preview> --app SLUG [--key KEY --environment NAME --minimum-events N --minimum-customers N --window-seconds N --cooldown-seconds N --enabled=true|false --expected-revision N --issue UUID]"
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprintln(os.Stderr, usage)
		return 0
	}
	action := args[0]
	fs := newFlagSet("issues compound-alerts "+action, flag.ContinueOnError)
	app := fs.String("app", "", "application slug")
	key := fs.String("key", "", "stable compound rule key")
	environment := fs.String("environment", "", "exact environment; empty matches all")
	events := fs.Int64("minimum-events", 0, "minimum retained accepted events")
	customers := fs.Int64("minimum-customers", 0, "minimum verified distinct customers in the same window")
	window := fs.Int64("window-seconds", 0, "rolling observation window")
	cooldown := fs.Int64("cooldown-seconds", 0, "minimum time between matching alerts")
	enabled := fs.Bool("enabled", true, "enable rule; use --enabled=false to disable")
	revision := fs.Int64("expected-revision", -1, "current policy revision, required for save/remove")
	issue := fs.String("issue", "", "issue UUID for a read-only draft preview")
	flags, positionals := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "key": true, "environment": true, "minimum-events": true, "minimum-customers": true, "window-seconds": true, "cooldown-seconds": true, "expected-revision": true, "issue": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *app == "" || len(positionals) != 0 || action != "get" && action != "save" && action != "remove" && action != "preview" {
		_, _ = fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	invalid := ""
	fs.Visit(func(f *flag.Flag) {
		if action == "get" && f.Name != "app" || action != "preview" && f.Name == "issue" || (action == "get" || action == "preview") && f.Name == "expected-revision" || action == "remove" && f.Name != "app" && f.Name != "key" && f.Name != "expected-revision" {
			invalid = f.Name
		}
	})
	if invalid != "" {
		return printErr("Invalid compound alert option", fmt.Errorf("--%s is not supported by %s", invalid, action))
	}
	if (action == "save" || action == "remove") && *revision < 0 {
		return printErr("Expected revision required", fmt.Errorf("read compound alerts before saving"))
	}
	form := url.Values{"action": {action}, "key": {*key}, "environment": {*environment}, "minimum_events": {strconv.FormatInt(*events, 10)}, "minimum_customers": {strconv.FormatInt(*customers, 10)}, "window_seconds": {strconv.FormatInt(*window, 10)}, "cooldown_seconds": {strconv.FormatInt(*cooldown, 10)}, "expected_revision": {strconv.FormatInt(*revision, 10)}}
	if *enabled {
		form.Set("enabled", "on")
	}
	var rule api.IssueCompoundAlertRule
	var err error
	if action == "save" || action == "preview" {
		rule, err = issuealerts.CompoundRuleForm(form)
		if err != nil {
			return printErr("Invalid compound rule", err)
		}
	}
	if action == "remove" && *key == "" {
		return printErr("Invalid rule", fmt.Errorf("provide --key"))
	}
	if action == "preview" {
		id, e := uuid.Parse(*issue)
		if e != nil || id == uuid.Nil {
			return printErr("Invalid preview", fmt.Errorf("provide --issue UUID"))
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out any
	switch action {
	case "get":
		out, err = c.GetIssueCompoundAlertPolicy(ctx, *app)
	case "preview":
		out, err = c.PreviewIssueCompoundAlert(ctx, *app, api.PreviewIssueCompoundAlertRequest{IssueID: *issue, Rule: rule})
	default:
		current, e := c.GetIssueCompoundAlertPolicy(ctx, *app)
		if e != nil {
			return printErr("Could not read compound rules", e)
		}
		in, e := issuealerts.CompoundFormUpdate(current, form)
		if e != nil {
			return printErr("Invalid compound rule", e)
		}
		out, err = c.SetIssueCompoundAlertPolicy(ctx, *app, in)
	}
	if err != nil {
		return printErr("Could not manage compound alerts", err)
	}
	return jsonOut(writeJSON(out))
}
