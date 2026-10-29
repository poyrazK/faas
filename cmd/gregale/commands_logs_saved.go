package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const subLogsSaved = "saved"

// cmdLogsSaved implements `gregale logs saved [list|save|delete]`: an app's
// server-side saved searches (ADR-20261018083536 slice D). The local
// `gregale logs views` keep working beside them.
func cmdLogsSaved(args []string) int {
	sub := "list"
	if len(args) > 0 && (args[0] == "list" || args[0] == "save" || args[0] == "delete") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "save":
		return cmdLogsSavedSave(args)
	case "delete":
		return cmdLogsSavedDelete(args)
	default:
		return cmdLogsSavedList(args)
	}
}

func cmdLogsSavedList(args []string) int {
	const usage = "usage: gregale logs saved [list] [<slug>]"
	fs := newFlagSet("logs saved", flag.ContinueOnError)
	f := newLogSearchFlags(fs)
	slug, ok := parseLogSearchArgs(fs, f, args, usage)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListAppLogSavedSearches(context.Background(), slug)
	if err != nil {
		return printErr("Listing saved log searches failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderLogSavedSearches(osStdout, slug, out.Searches)
	return 0
}

// cmdLogsSavedSave implements `gregale logs saved save <name> [<slug>]` with
// the search filter flags; windows are not stored.
func cmdLogsSavedSave(args []string) int {
	const usage = "usage: gregale logs saved save <name> [<slug>] [--q TEXT] [--source S] [--level error,warn] [--route R] [--status 5xx] [--release ID] [--request ID] [--trace ID]"
	fs := newFlagSet("logs saved save", flag.ContinueOnError)
	f := newLogSearchFlags(fs)
	name, slug, ok := parseLogSavedNameArgs(fs, f, args, usage)
	if !ok {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	query := f.storedValues()
	if len(query) == 0 {
		printErr("Nothing to save", fmt.Errorf("give at least one filter, such as --level error or --q timeout"))
		PrintUsage(os.Stderr, usage, "logs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	saved, err := client.PutAppLogSavedSearch(context.Background(), slug, name, query.Encode())
	if err != nil {
		return printErr("Saving the log search failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(saved))
	}
	_, _ = fmt.Fprintf(osStdout, "saved %s on %s: %s\nsearch it with: gregale logs search %s --saved %s\n", saved.Name, slug, saved.Query, slug, saved.Name)
	return 0
}

// cmdLogsSavedDelete implements `gregale logs saved delete <name> [<slug>]`.
func cmdLogsSavedDelete(args []string) int {
	const usage = "usage: gregale logs saved delete <name> [<slug>]"
	fs := newFlagSet("logs saved delete", flag.ContinueOnError)
	f := newLogSearchFlags(fs)
	name, slug, ok := parseLogSavedNameArgs(fs, f, args, usage)
	if !ok {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteAppLogSavedSearch(context.Background(), slug, name); err != nil {
		return printErr("Deleting the saved log search failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"deleted": name, "app": slug}))
	}
	_, _ = fmt.Fprintf(osStdout, "deleted saved search %s on %s\n", name, slug)
	return 0
}

// parseLogSavedNameArgs parses `<name> [<slug>]` around the flags.
func parseLogSavedNameArgs(fs *flag.FlagSet, f logSearchFlags, args []string, usage string) (string, string, bool) {
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return "", "", false
	}
	if len(pos) == 0 || len(pos) > 2 {
		printErr("Invalid arguments", fmt.Errorf("give the saved search name and optionally the app"))
		PrintUsage(os.Stderr, usage, "logs")
		return "", "", false
	}
	rest, err := mergeAppFlag(pos[1:], *f.app, 1)
	if err != nil {
		printErr("Invalid app target", err)
		PrintUsage(os.Stderr, usage, "logs")
		return "", "", false
	}
	target := ""
	if len(rest) == 1 {
		target = rest[0]
	}
	slug, err := resolveLogsAppTarget(target, false)
	if err != nil {
		printErr("Invalid app target", err)
		return "", "", false
	}
	return pos[0], slug, true
}

func renderLogSavedSearches(w io.Writer, slug string, searches []api.LogSavedSearch) {
	if len(searches) == 0 {
		_, _ = fmt.Fprintf(w, "%s has no saved log searches; save one with: gregale logs saved save NAME %s --level error\n", slug, slug)
		return
	}
	for _, s := range searches {
		_, _ = fmt.Fprintf(w, "%-24s %s\n", s.Name, s.Query)
	}
}
