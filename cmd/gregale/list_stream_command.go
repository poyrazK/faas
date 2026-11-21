package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func validateListStream(stream, all bool, command string) bool {
	violation := listFlagRuleViolation(listStreamRequiresAllJSON, listFlagRuleState{Stream: stream, All: all, JSON: jsonOutput}, command)
	if violation == nil {
		return true
	}
	printListFlagRuleUsage(violation)
	return false
}

func runStreamList[T any](parent context.Context, title, cursor string, fetch func(context.Context, string) ([]T, string, error)) int {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	progress, err := streamListPages(ctx, osStdout, cursor, fetch)
	err = listReadError(ctx, err)
	if err != nil {
		progress.Partial = progress.Partial || progress.ItemsEmitted > 0
		return printErr(title, &listStreamError{cause: err, progress: progress})
	}
	return 0
}
