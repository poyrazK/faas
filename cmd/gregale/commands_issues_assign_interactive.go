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

func cmdIssuesAssignInteractive(args []string) int {
	fs := newFlagSet("issues assign", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose an open issue and review ownership")
	appFlag := fs.String("app", "", "app slug")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 {
		return printErr("Invalid arguments", errors.New("use issues assign --interactive with optional --app"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use issues assign ISSUE_ID --app APP --assignee ACCOUNT_ID for scripts"))
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
	choice, err := prompt.choose(ctx, "Issue owner", []string{"Assign to me", "Unassign"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	assignee := ""
	proposedOwner := "unassigned"
	if choice == 0 {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		account, err := client.Whoami(readCtx)
		cancel()
		if err != nil {
			return printErr("Could not read your account", err)
		}
		if !alertIDPattern.MatchString(account.ID) {
			return printErr("Invalid account identity", errors.New("account has an invalid ID"))
		}
		assignee = account.ID
		proposedOwner = oneLine(account.Email) + " (" + account.ID + ")"
	}
	currentOwner := current.AssigneeAccountID
	if currentOwner == "" {
		currentOwner = "unassigned"
	}
	PrintProgress(osStdout, "Issue: %s (%s)\nApp: %s; environment: %s\nCurrent owner: %s\nProposed owner: %s", oneLine(current.Title), current.ID, slug, oneLine(current.Environment), oneLine(currentOwner), proposedOwner)
	if current.AssigneeAccountID == assignee {
		PrintProgress(osStdout, "Ownership already matches; no change needed.")
		return 0
	}
	confirmed, err := prompt.confirm(ctx, "Save this ownership change?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Issue assignment canceled.")
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
	assigned, err := client.ActOnIssue(writeCtx, slug, current.ID, api.IssueActionRequest{Action: "assign", AssigneeAccountID: assignee})
	if err != nil {
		return printErr("Could not assign issue", err)
	}
	if assigned.ID != current.ID || assigned.AppID != app.ID || assigned.State != current.State || assigned.AssigneeAccountID != assignee {
		return printErr("Invalid assignment receipt", errors.New("inspect the issue before taking further action"))
	}
	PrintOK(osStdout, "Issue %s owner: %s.", assigned.ID, proposedOwner)
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "issues", "get", quoteLogCommandArg(current.ID), "--app", quoteLogCommandArg(slug))
	fmt.Fprintln(osStdout, "Inspection command (POSIX shells):\n"+strings.Join(command, " "))
	return 0
}
