package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdIssuesIgnoreInteractive(args []string) int {
	fs := newFlagSet("issues ignore", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose an issue and ignore duration")
	appFlag := fs.String("app", "", "app slug")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 {
		return printErr("Invalid arguments", errors.New("use issues ignore --interactive with optional --app"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use issues ignore ISSUE_ID --app APP --until RFC3339 for scripts"))
	}
	slug, err := resolveReadAppTarget(*appFlag)
	if err != nil {
		return readAppTargetError(err)
	}
	if !validCLISlug(slug) {
		return printErr("Invalid app", errors.New("provide a valid app slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	app, err := client.GetApp(readCtx, slug)
	cancel()
	if err != nil {
		return printErr("Could not read app", err)
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	issue, selected, err := chooseOpenIssue(ctx, client, prompt, slug, app.ID)
	if err != nil {
		return printErr("Could not choose issue", err)
	}
	if !selected {
		return 0
	}
	choice, err := prompt.choose(ctx, "Ignore duration", []string{"1 hour", "24 hours", "7 days", "Custom duration"}, 1)
	if err != nil {
		return startInputExit(err)
	}
	durations := []time.Duration{time.Hour, 24 * time.Hour, 7 * 24 * time.Hour}
	duration := 24 * time.Hour
	if choice < len(durations) {
		duration = durations[choice]
	} else {
		for {
			value, inputErr := prompt.text(ctx, "Duration (e.g. 2h, 48h; maximum 90 days)", "24h")
			if inputErr != nil {
				return startInputExit(inputErr)
			}
			duration, err = time.ParseDuration(value)
			if err == nil && duration >= time.Second && duration <= api.IssueMaxTokenLifetime {
				break
			}
			_, _ = fmt.Fprintln(osStderr, "Enter a duration from 1 second to 2160 hours.")
		}
	}
	read := func() (api.Issue, error) {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		detail, err := client.GetIssuePage(readCtx, slug, issue.ID, "", "", "", "")
		if err == nil && (detail.Issue.ID != issue.ID || detail.Issue.AppID != app.ID) {
			err = errors.New("issue does not match selected app and ID")
		}
		return detail.Issue, err
	}
	current, err := read()
	if err != nil {
		return printErr("Could not read selected issue", err)
	}
	if current.State != "open" {
		return printErr("Issue state changed", errors.New("choose an open issue again"))
	}
	until := time.Now().UTC().Add(duration).Truncate(time.Second)
	PrintProgress(osStdout, "Ignore issue: %s (%s)\nApp: %s; environment: %s\nUntil: %s (UTC)", oneLine(current.Title), current.ID, slug, oneLine(current.Environment), until.Format(time.RFC3339))
	confirmed, err := prompt.confirm(ctx, "Mark this issue ignored until this time?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Issue suppression canceled.")
		return 0
	}
	latest, err := read()
	if err != nil {
		return printErr("Could not recheck issue", err)
	}
	if !reflect.DeepEqual(latest, current) {
		return printErr("Issue changed", errors.New("run the command again to review the current issue"))
	}
	if !until.After(time.Now()) {
		return printErr("Expiry elapsed", errors.New("choose a new duration before saving"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ignored, err := client.ActOnIssue(writeCtx, slug, current.ID, api.IssueActionRequest{Action: "ignore", IgnoredUntil: &until})
	if err != nil {
		return printErr("Could not ignore issue", err)
	}
	if ignored.ID != current.ID || ignored.AppID != app.ID || ignored.State != "ignored" || ignored.IgnoredUntil == nil || !ignored.IgnoredUntil.Equal(until) {
		return printErr("Invalid suppression receipt", errors.New("inspect the issue before taking further action"))
	}
	PrintOK(osStdout, "Issue %s ignored until %s.", ignored.ID, until.Format(time.RFC3339))
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "issues", "get", quoteLogCommandArg(current.ID), "--app", quoteLogCommandArg(slug))
	_, _ = fmt.Fprintln(osStdout, "Inspection command (POSIX shells):\n"+strings.Join(command, " "))
	return 0
}
