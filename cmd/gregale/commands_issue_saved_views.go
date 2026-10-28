package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdIssueSavedViews(args []string) int {
	usage := "usage: gregale debug issue views <list|get|save|delete|apply> --app SLUG [--key KEY --name NAME --state open --assignee me --sort impact --min-customers N --tag component=payments --cursor CURSOR]"
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stderr, usage)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	action := args[0]
	fs := newFlagSet("issues views "+action, flag.ContinueOnError)
	app := fs.String("app", "", "application slug")
	key := fs.String("key", "", "stable shared view key")
	name := fs.String("name", "", "shared view name")
	stateFilter := fs.String("state", "", "issue lifecycle state")
	environment := fs.String("environment", "", "issue environment")
	team := fs.String("team", "", "team UUID for a shared team inbox")
	assignee := fs.String("assignee", "", "me, unassigned, or an eligible account UUID")
	sort := fs.String("sort", "recent", "recent or impact")
	minimum := fs.Int64("min-customers", 0, "minimum verified customers in 24h")
	tag := fs.String("tag", "", "allowed operational key=value")
	cursor := fs.String("cursor", "", "cursor from the same saved filter set")
	flags, positionals := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "key": true, "name": true, "state": true, "environment": true, "team": true, "assignee": true, "sort": true, "min-customers": true, "tag": true, "cursor": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	invalidFlag := ""
	fs.Visit(func(f *flag.Flag) {
		if action != "save" && (f.Name == "name" || f.Name == "state" || f.Name == "environment" || f.Name == "team" || f.Name == "assignee" || f.Name == "sort" || f.Name == "min-customers" || f.Name == "tag") {
			invalidFlag = f.Name
		}
		if f.Name == "cursor" && action != "apply" || f.Name == "key" && action == "list" {
			invalidFlag = f.Name
		}
	})
	if invalidFlag != "" {
		return printErr("Flag is not valid for this view action", fmt.Errorf("--%s is not supported by %s", invalidFlag, action))
	}
	if *app == "" || len(positionals) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if action != "list" && action != "get" && action != "save" && action != "delete" && action != "apply" {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if action != "list" {
		if err := api.ValidateIssueSavedViewKey(*key); err != nil {
			return printErr("Invalid view key", err)
		}
	}
	in := api.PutIssueSavedViewRequest{Name: *name, Filters: api.IssueSavedViewFilters{Team: *team, State: *stateFilter, Environment: *environment, Assignee: *assignee, Sort: *sort, MinCustomers: *minimum, Tag: *tag}}
	if action == "save" {
		var err error
		in, err = api.NormalizeIssueSavedView(in)
		if err != nil {
			return printErr("Invalid saved view", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch action {
	case "list":
		out, err := c.ListIssueSavedViews(ctx, *app)
		if err != nil {
			return printErr("Could not list saved views", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		for _, view := range out.Items {
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\n", view.Key, view.Name)
		}
	case "get", "save":
		var out api.IssueSavedView
		if action == "save" {
			out, err = c.PutIssueSavedView(ctx, *app, *key, in)
		} else {
			out, err = c.GetIssueSavedView(ctx, *app, *key)
		}
		if err != nil {
			return printErr("Could not manage saved view", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "%s: %s\nFilters: %s\n", out.Key, out.Name, out.Filters.Query().Encode())
	case "delete":
		if err := c.DeleteIssueSavedView(ctx, *app, *key); err != nil {
			return printErr("Could not delete saved view", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"deleted": true, "key": *key}))
		}
		_, _ = fmt.Fprintf(osStdout, "Deleted saved view %s.\n", *key)
	case "apply":
		out, err := c.ListIssuesForSavedView(ctx, *app, *key, *cursor)
		if err != nil {
			return printErr("Could not apply saved view", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		for _, issue := range out.Items {
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\n", issue.ID, issue.State, issue.Title)
		}
		if out.NextCursor != "" {
			_, _ = fmt.Fprintf(osStdout, "Next cursor: %s\n", out.NextCursor)
		}
	}
	return 0
}
