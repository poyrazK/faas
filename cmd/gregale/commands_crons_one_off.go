package main

// One-off requests under `crons` — ADR-20261026145832: cron owns every
// schedule, so a request that runs once at a time is created, listed, read
// and cancelled with the cron verbs. The work is a delayed task, and the
// delayed-task commands keep working.

import (
	"fmt"
	"slices"
	"strings"
)

// cronRecurringFlags only make sense for a recurring cron.
var cronRecurringFlags = []string{
	"schedule", "interactive", "command", "arg", "shell", "timeout-seconds", "max-output-bytes",
	"timezone", "skip-if-running", "retry-max", "retry-backoff-seconds", "schedule-policy", "failure-rules",
}

// cronOneOffFlags maps crons add's one-off flags to delayed-task add's.
var cronOneOffFlags = map[string]string{"at": "scheduled-at", "in": "delay"}

// flagName returns the name of a --flag or --flag=value token.
func flagName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
		return "", false
	}
	name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
	return name, true
}

// takeBoolFlag removes --name from args and reports whether it was there.
func takeBoolFlag(args []string, name string) ([]string, bool) {
	rest := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		if got, ok := flagName(arg); ok && got == name && (!strings.Contains(arg, "=") || strings.HasSuffix(arg, "=true")) {
			found = true
			continue
		}
		rest = append(rest, arg)
	}
	return rest, found
}

// cronOneOffArgs reports whether crons add was asked for a one-off request
// and returns delayed-task add's spelling of its arguments.
func cronOneOffArgs(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		name, ok := flagName(arg)
		if target, oneOff := cronOneOffFlags[name]; ok && oneOff {
			found = true
			arg = "--" + target + strings.TrimPrefix(strings.TrimLeft(arg, "-"), name)
		}
		out = append(out, arg)
	}
	return out, found
}

func cmdCronsAddOneOff(original, args []string) int {
	for _, arg := range original {
		if name, ok := flagName(arg); ok && slices.Contains(cronRecurringFlags, name) {
			return printErr("Invalid cron options", fmt.Errorf("--at and --in schedule one request; --%s belongs to a recurring cron (use --schedule without --at or --in)", name))
		}
	}
	return cmdDelayedTaskAdd(args)
}

// cronCancelsOneOff reports `crons cancel <task-id> [--app SLUG]`: one id,
// where cancelling a command-cron run takes a cron id and a run id.
func cronCancelsOneOff(args []string) bool {
	ids := 0
	for i := 0; i < len(args); i++ {
		if name, ok := flagName(args[i]); ok {
			if name == "app" && !strings.Contains(args[i], "=") {
				i++
			}
			continue
		}
		ids++
	}
	return ids == 1
}
