package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func validateListStream(stream, all bool) bool {
	if !stream || (jsonOutput && all) {
		return true
	}
	PrintUsage(osStderr, "--stream requires --json and --all", "cli")
	return false
}

func runStreamList[T any](title, cursor string, fetch func(context.Context, string) ([]T, string, error)) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	progress, err := streamListPages(ctx, osStdout, cursor, fetch)
	if err != nil {
		return printErr(title, &listStreamError{cause: err, progress: progress})
	}
	return 0
}
