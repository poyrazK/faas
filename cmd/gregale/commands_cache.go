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
	// Accept both the documented positional-first form and the
	// conventional flags-first spelling. The standard flag package
	// otherwise stops parsing as soon as it sees the app slug.
	flags, positional := splitArgsForFlags(args[1:])
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		PrintUsage(os.Stderr, "usage: gregale cache purge <slug> [--path GLOB]", "cache")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	slug := positional[0]
	if err := client.PurgeAppCache(context.Background(), slug, *pathGlob); err != nil {
		return printErr("Cache purge failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"purged": true,
			"app":    slug,
			"path":   *pathGlob,
		}))
	}
	if *pathGlob == "" {
		_, _ = fmt.Fprintf(osStdout, "Purged response cache for %s\n", slug)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Purged response cache for %s (%s)\n", slug, *pathGlob)
	}
	return 0
}
