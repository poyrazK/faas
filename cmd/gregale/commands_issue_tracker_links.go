package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdIssueTrackerLinks(args []string) int {
	usage := "usage: gregale debug issue links <list|save|delete> --app SLUG --issue UUID [--url URL --title TEXT --link UUID --cursor CURSOR]"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, usage)
		return 0
	}
	action := args[0]
	fs := newFlagSet("issues links", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	issue := fs.String("issue", "", "issue UUID")
	link := fs.String("link", "", "tracker link UUID")
	url := fs.String("url", "", "GitHub or Linear issue URL")
	title := fs.String("title", "", "optional tracker title")
	cursor := fs.String("cursor", "", "tracker-link cursor")
	flags, pos := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "issue": true, "link": true, "url": true, "title": true, "cursor": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	id, err := uuid.Parse(*issue)
	if *app == "" || len(pos) != 0 || err != nil || id == uuid.Nil || action != "list" && action != "save" && action != "delete" {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "cursor" && action != "list" || (f.Name == "url" || f.Name == "title") && action != "save" || f.Name == "link" && action != "delete" {
			invalid = true
		}
	})
	if invalid {
		return printErr("Invalid tracker-link flags", fmt.Errorf("URL/title apply to save, link ID to delete, cursor to list"))
	}
	in := api.PutIssueTrackerLinkRequest{URL: *url, Title: *title}
	if action == "save" {
		normalized, err := api.NormalizeIssueTrackerLink(in)
		if err != nil {
			return printErr("Invalid tracker link", err)
		}
		in = normalized.PutIssueTrackerLinkRequest
	}
	if action == "delete" {
		parsed, err := uuid.Parse(*link)
		if err != nil || parsed == uuid.Nil {
			return printErr("Invalid link ID", fmt.Errorf("delete requires --link UUID"))
		}
		*link = parsed.String()
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch action {
	case "list":
		out, err := c.ListIssueTrackerLinks(ctx, *app, id.String(), *cursor)
		if err != nil {
			return printErr("Could not list tracker links", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		for _, item := range out.Items {
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", item.ID, item.Provider, item.ExternalKey, item.URL)
		}
		if out.NextCursor != "" {
			_, _ = fmt.Fprintf(osStdout, "Next cursor: %s\n", out.NextCursor)
		}
	case "save":
		out, err := c.PutIssueTrackerLink(ctx, *app, id.String(), in)
		if err != nil {
			return printErr("Could not save tracker link", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\n", out.ID, out.URL)
	case "delete":
		if err := c.DeleteIssueTrackerLink(ctx, *app, id.String(), *link); err != nil {
			return printErr("Could not unlink tracker issue", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"deleted": true, "id": *link}))
		}
		_, _ = fmt.Fprintf(osStdout, "Unlinked %s.\n", *link)
	}
	return 0
}
