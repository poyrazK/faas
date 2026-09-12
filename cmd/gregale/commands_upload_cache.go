package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

const dispatchUploadCache = "upload-cache"

func cmdUploadCache(args []string) int {
	if len(args) == 0 || hasHelpFlag(args) {
		PrintUsage(osStdout, "usage: gregale upload-cache <list|cleanup> [--older-than D] [--max-entries N] [--dry-run]", "upload-cache")
		if len(args) == 0 {
			return 1
		}
		return 0
	}
	switch args[0] {
	case "list":
		return cmdUploadCacheList(args[1:])
	case "cleanup":
		return cmdUploadCacheCleanup(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown upload-cache subcommand %q\n", args[0])
		return 1
	}
}

func uploadCacheFlags(name string, args []string) (uploadCachePolicy, bool, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	olderThan := fs.Duration("older-than", uploadCacheMaxAge, "remove recovery state older than this duration")
	maxEntries := fs.Int("max-entries", uploadCacheMaxEntries, "maximum resumable recovery records to retain")
	dryRun := fs.Bool("dry-run", false, "show cleanup actions without deleting files")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *olderThan < 0 || *maxEntries < 0 {
		PrintUsage(os.Stderr, "usage: gregale upload-cache "+name+" [--older-than D] [--max-entries N] [--dry-run]", "upload-cache")
		return uploadCachePolicy{}, false, false
	}
	return uploadCachePolicy{MaxAge: *olderThan, MaxEntries: *maxEntries}, *dryRun, true
}

func cmdUploadCacheList(args []string) int {
	policy, _, ok := uploadCacheFlags("list", args)
	if !ok {
		return 1
	}
	entries, err := inspectUploadCache(time.Now(), policy)
	if err != nil {
		return printErr("Could not inspect upload cache", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(entries))
	}
	if len(entries) == 0 {
		fmt.Fprintln(osStdout, "Upload cache is empty.")
		return 0
	}
	for _, entry := range entries {
		action := "keep"
		if entry.Action != "" {
			action = entry.Action
		}
		fmt.Fprintf(osStdout, "%s\t%s\t%s\n", entry.Key, entry.Status, action)
	}
	return 0
}

func cmdUploadCacheCleanup(args []string) int {
	policy, dryRun, ok := uploadCacheFlags("cleanup", args)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, entries, err := cleanupUploadCacheExclusive(ctx, policy, !dryRun)
	if err != nil {
		return printErr("Could not clean upload cache", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			DryRun  bool                     `json:"dry_run"`
			Result  uploadCacheCleanupResult `json:"result"`
			Entries []uploadCacheEntry       `json:"entries,omitempty"`
		}{DryRun: dryRun, Result: result, Entries: entries}))
	}
	verb := "Removed"
	if dryRun {
		verb = "Would remove"
	}
	fmt.Fprintf(osStdout, "%s %d upload cache entr%s; kept %d.\n", verb, removableUploadCacheEntries(entries), pluralY(removableUploadCacheEntries(entries)), result.Kept)
	return 0
}

func removableUploadCacheEntries(entries []uploadCacheEntry) int {
	n := 0
	for _, entry := range entries {
		if entry.Action != "" {
			n++
		}
	}
	return n
}
