package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

const devTriggerUsage = "usage: gregale dev trigger [--path DIR] [--name PROJECT] <invoke|cron|delayed-task> [args]"

// loadDevSession reads the selected developer environment without renewing
// its lease. A missing session gets a next-step hint instead of a bare 404,
// because the usual cause is simply that `gregale dev` has not run yet from
// this source directory (or its 24-hour lease expired).
func loadDevSession(client *Client, target devTarget) (api.DevSessionResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := client.GetDevSession(ctx, target.Project, target.WorkspaceID)
	if isNotFound(err) {
		return session, fmt.Errorf("no developer environment for %s from this source directory; start one with `gregale dev`", target.Project)
	}
	return session, err
}

// cmdDevInfo prints the selected developer environment: its stable URL, the
// backing app slug that other `gregale` commands accept, the lease expiry,
// and the safe managed PostgreSQL state. It never extends the lease.
func cmdDevInfo(args []string) int {
	fs := newFlagSet("dev info", flag.ContinueOnError)
	name := fs.String("name", "", "developer-session project name (default: selected source directory)")
	sourcePath := fs.String("path", "", "source directory (relative to the current directory)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale dev info [--path DIR] [--name PROJECT]", "dev")
		return 1
	}
	target, targetErr := resolveDevTarget(*sourcePath, *name)
	if targetErr != nil {
		return targetErr.print()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	session, err := loadDevSession(client, target)
	if err != nil {
		return printErr("Could not load developer environment", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(session))
	}
	PrintOK(osStdout, "Developer environment: %s", canonicalAppURL(session.App))
	_, _ = fmt.Fprintf(osStdout, "  project: %s\n  app:     %s\n", target.Project, session.App.Slug)
	if !session.ExpiresAt.IsZero() {
		_, _ = fmt.Fprintf(osStdout, "  lease:   expires %s (renewed by each sync)\n", session.ExpiresAt.Local().Format(time.RFC822))
	}
	if session.Postgres != nil {
		_, _ = fmt.Fprintf(osStdout, "  postgres: %s (%s; binding %s, injected as %s)\n", session.Postgres.Name,
			session.Postgres.State, session.Postgres.BindingState, session.Postgres.EnvironmentKey)
	}
	return 0
}

// cmdDevTrigger runs an existing async/invocation command against the
// developer environment's app. Selection flags come before the verb so they
// never collide with the delegated command's own flags (invoke has its own
// --path for the URL path).
func cmdDevTrigger(args []string) int {
	fs := newFlagSet("dev trigger", flag.ContinueOnError)
	name := fs.String("name", "", "developer-session project name (default: selected source directory)")
	sourcePath := fs.String("path", "", "source directory (relative to the current directory)")
	if err := fs.Parse(args); err != nil || fs.NArg() == 0 {
		PrintUsage(osStderr, devTriggerUsage, "dev")
		return 1
	}
	verb, rest := fs.Arg(0), fs.Args()[1:]
	switch verb {
	case "invoke", "cron", "delayed-task":
	default:
		PrintUsage(osStderr, devTriggerUsage, "dev")
		return 1
	}
	if code := rejectDevTriggerAppSelection(verb, rest); code != 0 {
		return code
	}
	target, targetErr := resolveDevTarget(*sourcePath, *name)
	if targetErr != nil {
		return targetErr.print()
	}
	var cronPath string
	if verb == "cron" {
		path, err := selectDevCronPath(target, rest)
		if err != nil {
			return printErr("No matching cron trigger", err)
		}
		cronPath = path
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	session, err := loadDevSession(client, target)
	if err != nil {
		return printErr("Could not load developer environment", err)
	}
	return runDevTrigger(verb, session.App.Slug, cronPath, rest)
}

// rejectDevTriggerAppSelection keeps the developer app the only target: a
// stray positional slug or --app would silently retarget production.
func rejectDevTriggerAppSelection(verb string, rest []string) int {
	switch verb {
	case "invoke":
		if _, positional := splitArgsForFlags(rest, "async"); len(positional) != 0 {
			return printErr("Invalid arguments", fmt.Errorf("dev trigger invoke targets the developer app; remove the positional %q", positional[0]))
		}
	case "delayed-task":
		for _, arg := range rest {
			if arg == "--app" || arg == "-app" || strings.HasPrefix(arg, "--app=") || strings.HasPrefix(arg, "-app=") {
				return printErr("Invalid arguments", errors.New("dev trigger delayed-task targets the developer app; remove --app"))
			}
		}
	case "cron":
		if len(rest) > 1 {
			PrintUsage(osStderr, "usage: gregale dev trigger [--path DIR] [--name PROJECT] cron [ROUTE]", "dev")
			return 1
		}
	}
	return 0
}

// runDevTrigger delegates to the production command implementations so the
// developer path shares their validation, output, and exit codes.
func runDevTrigger(verb, slug, cronPath string, rest []string) int {
	switch verb {
	case "invoke":
		return cmdInvoke(append(append([]string(nil), rest...), slug))
	case "cron":
		// Scheduled crons dispatch POST to the trigger path with no body;
		// a synchronous invoke reproduces that request and reports the
		// handler's result instead of a fire-and-forget request id.
		return cmdInvoke([]string{"--method", "POST", "--path", cronPath, slug})
	default: // delayed-task
		return cmdDelayedTaskAdd(append([]string{"--app", slug}, rest...))
	}
}

// selectDevCronPath picks one cron trigger declared for this project in the
// source directory's gregale.yaml. Manifest crons are keyed to the
// production app slug, so they never run on a schedule in a developer
// environment; firing one by its route is the developer equivalent.
func selectDevCronPath(target devTarget, rest []string) (string, error) {
	manifest, ok, err := gregalemanifest.Load(target.SourceDir)
	if err != nil {
		return "", err
	}
	var paths []string
	if ok {
		seen := map[string]bool{}
		for _, trigger := range manifest.Triggers {
			// Two schedules on one route fire the same request; list it once.
			if trigger.Kind == gregalemanifest.TriggerKindCron && trigger.App == target.Project && !seen[trigger.Path] {
				seen[trigger.Path] = true
				paths = append(paths, trigger.Path)
			}
		}
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("gregale.yaml declares no cron triggers for app %q; use `gregale dev trigger invoke --method POST --path ROUTE` for an ad-hoc request", target.Project)
	}
	if len(rest) == 1 {
		for _, path := range paths {
			if path == rest[0] {
				return path, nil
			}
		}
		return "", fmt.Errorf("no cron trigger for app %q has route %q; declared routes: %s", target.Project, rest[0], strings.Join(paths, ", "))
	}
	if len(paths) > 1 {
		return "", fmt.Errorf("app %q declares %d cron triggers; pass one route: %s", target.Project, len(paths), strings.Join(paths, ", "))
	}
	return paths[0], nil
}
