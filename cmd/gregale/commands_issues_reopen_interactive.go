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

func cmdIssuesReopenInteractive(args []string) int {
	fs := newFlagSet("issues reopen", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose a resolved or ignored issue to reopen")
	appFlag := fs.String("app", "", "app slug")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 {
		return printErr("Invalid arguments", errors.New("use issues reopen --interactive with optional --app"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use issues reopen ISSUE_ID --app APP for scripts"))
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
	stateChoice, err := prompt.choose(ctx, "Issue state", []string{"Resolved", "Ignored"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	state := []string{"resolved", "ignored"}[stateChoice]
	issue, selected, err := chooseIssueInState(ctx, client, prompt, slug, app.ID, state)
	if err != nil {
		return printErr("Could not choose issue", err)
	}
	if !selected {
		return 0
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
	if current.State != state {
		return printErr("Issue state changed", errors.New("choose a resolved or ignored issue again"))
	}
	PrintProgress(osStdout, "Reopen issue: %s (%s)\nApp: %s; environment: %s\nCurrent state: %s", oneLine(current.Title), current.ID, slug, oneLine(current.Environment), current.State)
	if current.State == "resolved" {
		PrintProgress(osStdout, "Fixing release: %s", oneLine(current.FixedDeploymentID))
		if current.ResolvedAt != nil {
			PrintProgress(osStdout, "Resolved at: %s (UTC)", current.ResolvedAt.UTC().Format(time.RFC3339))
		}
	} else if current.IgnoredUntil != nil {
		PrintProgress(osStdout, "Suppression expires: %s (UTC)", current.IgnoredUntil.UTC().Format(time.RFC3339))
	}
	PrintProgress(osStdout, "Reopening clears the current resolution and suppression, and returns the issue to open. Historical activity is retained.")
	confirmed, err := prompt.confirm(ctx, "Reopen this issue?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Issue reopening canceled.")
		return 0
	}
	latest, err := read()
	if err != nil {
		return printErr("Could not recheck issue", err)
	}
	if !reflect.DeepEqual(latest, current) {
		return printErr("Issue changed", errors.New("run the command again to review the current issue"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	reopened, err := client.ActOnIssue(writeCtx, slug, current.ID, api.IssueActionRequest{Action: "reopen"})
	if err != nil {
		return printErr("Could not reopen issue", err)
	}
	if reopened.ID != current.ID || reopened.AppID != app.ID || reopened.State != "open" || reopened.IgnoredUntil != nil || reopened.ResolvedAt != nil || reopened.FixedDeploymentID != "" || reopened.FixedDeploymentCreatedAt != nil {
		return printErr("Invalid reopening receipt", errors.New("inspect the issue before taking further action"))
	}
	PrintOK(osStdout, "Issue %s reopened.", reopened.ID)
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "issues", "get", quoteLogCommandArg(current.ID), "--app", quoteLogCommandArg(slug))
	fmt.Fprintln(osStdout, "Inspection command (POSIX shells):\n"+strings.Join(command, " "))
	return 0
}
