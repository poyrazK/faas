package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// envResumeAutomation enables the crons and event subscriptions a full clone
// copied paused into a stage (ADR-20261104120000): the ones the source ran.
// One the source had disabled, or one changed since, stays as it is.
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
	fmt.Println(resumeAutomationSummary(resp.Environment, resp.CronsResumed, resp.EventSubscriptionsResumed, resp.BucketNotificationsResumed))
	return 0
}

func resumeAutomationSummary(environment string, crons, subscriptions, buckets int) string {
	if crons == 0 && subscriptions == 0 && buckets == 0 {
		return "No paused automation to resume in " + environment + "."
	}
	var parts []string
	if crons > 0 {
		parts = append(parts, resumeCount(crons, "cron", "crons"))
	}
	if subscriptions > 0 {
		parts = append(parts, resumeCount(subscriptions, "event subscription", "event subscriptions"))
	}
	if buckets > 0 {
		parts = append(parts, resumeCount(buckets, "bucket's notifications", "buckets' notifications"))
	}
	last := len(parts) - 1
	if last == 0 {
		return "Resumed " + parts[0] + " in " + environment + "."
	}
	return "Resumed " + strings.Join(parts[:last], ", ") + " and " + parts[last] + " in " + environment + "."
}

func resumeCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
