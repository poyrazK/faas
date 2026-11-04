package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdIssueTeams(args []string) int {
	usage := "usage: gregale debug issue teams <list|get|save|delete|inbox> --app SLUG [--key KEY --name NAME --members ACCOUNT_UUID,... --cursor CURSOR]"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, usage)
		return 0
	}
	action := args[0]
	fs := newFlagSet("issues teams", flag.ContinueOnError)
	app := fs.String("app", "", "application slug")
	key := fs.String("key", "", "stable team key")
	name := fs.String("name", "", "team name")
	members := fs.String("members", "", "comma-separated eligible account UUIDs")
	cursor := fs.String("cursor", "", "team inbox cursor")
	flags, positionals := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "key": true, "name": true, "members": true, "cursor": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *app == "" || len(positionals) != 0 || action != "list" && action != "get" && action != "save" && action != "delete" && action != "inbox" {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		if action != "save" && (f.Name == "members" || f.Name == "name") || action != "inbox" && f.Name == "cursor" || action == "list" && f.Name == "key" {
			invalid = true
		}
	})
	if invalid {
		return printErr("Invalid team flags", fmt.Errorf("name/members apply to save, cursor to inbox, and key to get/save/delete/inbox"))
	}
	if action != "list" {
		if err := api.ValidateIssueSavedViewKey(*key); err != nil {
			return printErr("Invalid team key", err)
		}
	}
	in := api.PutIssueTeamRequest{Name: *name, MemberAccountIDs: strings.Split(*members, ",")}
	if action == "save" {
		var err error
		in, err = api.NormalizeIssueTeam(in)
		if err != nil {
			return printErr("Invalid team", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch action {
	case "list":
		out, err := c.ListIssueTeams(ctx, *app)
		if err != nil {
			return printErr("Could not list teams", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		for _, team := range out.Items {
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%d members\n", team.Key, team.ID, team.Name, len(team.MemberAccountIDs))
		}
	case "get", "save":
		var out api.IssueTeam
		if action == "save" {
			out, err = c.PutIssueTeam(ctx, *app, *key, in)
		} else {
			out, err = c.GetIssueTeam(ctx, *app, *key)
		}
		if err != nil {
			return printErr("Could not manage team", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "%s: %s (%s)\nMembers: %s\n", out.Key, out.Name, out.ID, strings.Join(out.MemberAccountIDs, ","))
	case "delete":
		if err := c.DeleteIssueTeam(ctx, *app, *key); err != nil {
			return printErr("Could not delete team", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"deleted": true, "key": *key}))
		}
		_, _ = fmt.Fprintf(osStdout, "Deleted team %s.\n", *key)
	case "inbox":
		out, err := c.ListIssuesForTeam(ctx, *app, *key, *cursor)
		if err != nil {
			return printErr("Could not list team inbox", err)
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
