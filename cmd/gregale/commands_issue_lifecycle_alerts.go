package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdIssueLifecycleAlerts(args []string) int {
	usage := "usage: gregale issues lifecycle-alerts <get|save|preview> --app SLUG [--environments production,staging --cooldown-seconds N --expected-revision N --issue UUID --event created|regressed]"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, usage)
		return 0
	}
	action := args[0]
	fs := newFlagSet("issues lifecycle-alerts "+action, flag.ContinueOnError)
	app := fs.String("app", "", "application slug")
	environments := fs.String("environments", "", "comma-separated exact names; empty means all environments")
	cooldown := fs.Int64("cooldown-seconds", 0, "shared new-issue/regression cooldown; zero disables throttling")
	revision := fs.Int64("expected-revision", -1, "revision from get; required when saving")
	issue := fs.String("issue", "", "existing issue UUID for draft preview")
	event := fs.String("event", "regressed", "hypothetical event: created or regressed")
	flags, positionals := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "environments": true, "cooldown-seconds": true, "expected-revision": true, "issue": true, "event": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *app == "" || len(positionals) != 0 || action != "get" && action != "save" && action != "preview" {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	invalid := ""
	fs.Visit(func(f *flag.Flag) {
		if action == "get" && f.Name != "app" || action != "preview" && (f.Name == "issue" || f.Name == "event") || action != "save" && f.Name == "expected-revision" {
			invalid = f.Name
		}
	})
	if invalid != "" {
		return printErr("Invalid flag for lifecycle alert action", fmt.Errorf("--%s is not supported by %s", invalid, action))
	}
	names := []string{}
	if *environments != "" {
		for _, name := range strings.Split(*environments, ",") {
			names = append(names, strings.TrimSpace(name))
		}
	}
	names, err := api.NormalizeIssueLifecycleAlertPolicy(names, *cooldown)
	if err != nil {
		return printErr("Invalid lifecycle alert policy", err)
	}
	if action == "save" && *revision < 0 {
		return printErr("Expected revision required", fmt.Errorf("read the current policy before saving"))
	}
	if action == "preview" {
		id, e := uuid.Parse(*issue)
		if e != nil || id == uuid.Nil || *event != "created" && *event != "regressed" {
			return printErr("Invalid preview sample", fmt.Errorf("provide --issue UUID and --event created|regressed"))
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
		out, err = c.GetIssueLifecycleAlertPolicy(ctx, *app)
	case "save":
		out, err = c.SetIssueLifecycleAlertPolicy(ctx, *app, api.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: revision, Environments: names, CooldownSeconds: *cooldown})
	case "preview":
		out, err = c.PreviewIssueLifecycleAlert(ctx, *app, api.PreviewIssueLifecycleAlertRequest{IssueID: *issue, Event: *event, Environments: names, CooldownSeconds: *cooldown})
	}
	if err != nil {
		return printErr("Could not manage lifecycle alerts", err)
	}
	// These small policy/snapshot records have the same stable JSON form in both modes.
	return jsonOut(writeJSON(out))
}
