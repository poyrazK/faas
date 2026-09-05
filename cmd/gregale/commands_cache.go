package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// cmdCache implements `gregale cache purge <slug> [--path GLOB]`.
func cmdCache(args []string) int {
	if len(args) == 0 || args[0] != "purge" {
		PrintUsage(os.Stderr, "usage: gregale cache purge <slug> [--path GLOB]", "cache")
		return 1
	}
	fs := flag.NewFlagSet("cache purge", flag.ContinueOnError)
	pathGlob := fs.String("path", "", "optional normalized request path glob")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !validCLISlug(fs.Arg(0)) {
		PrintUsage(os.Stderr, "usage: gregale cache purge <slug> [--path GLOB]", "cache")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.PurgeAppCache(context.Background(), fs.Arg(0), *pathGlob); err != nil {
		return printErr("Cache purge failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"purged": true,
			"app":    fs.Arg(0),
			"path":   *pathGlob,
		}))
	}
	if *pathGlob == "" {
		_, _ = fmt.Fprintf(osStdout, "Purged response cache for %s\n", fs.Arg(0))
	} else {
		_, _ = fmt.Fprintf(osStdout, "Purged response cache for %s (%s)\n", fs.Arg(0), *pathGlob)
	}
	return 0
}
