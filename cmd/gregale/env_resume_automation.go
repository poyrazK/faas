package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// envResumeAutomation enables the crons a full clone copied paused into a
// stage (ADR-20261104120000): the ones the source ran. A cron the source had
// disabled, or one changed since, stays as it is.
func envResumeAutomation(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("env-resume-automation", flag.ContinueOnError)
	project := fs.String("project", "", "project slug (defaults to linked project)")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale env resume-automation <stage> [--project <slug>]", "env")
		return 1
	}
	projectSlug, err := environmentProjectSlug(*project)
	if err != nil {
		return printErr("Could not resolve project", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ResumeEnvironmentAutomation(context.Background(), projectSlug, positional[0])
	if err != nil {
		return printErr("Resume automation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	switch resp.CronsResumed {
	case 0:
		fmt.Printf("No paused crons to resume in %s.\n", resp.Environment)
	case 1:
		fmt.Printf("Resumed 1 cron in %s.\n", resp.Environment)
	default:
		fmt.Printf("Resumed %d crons in %s.\n", resp.CronsResumed, resp.Environment)
	}
	return 0
}
