package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdIssuesGetInteractive(args []string) int {
	fs := newFlagSet("issues get", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose an issue")
	appFlag := fs.String("app", "", "app slug")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 {
		return printErr("Invalid interactive arguments", errors.New("use issues get --interactive with optional --app"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use issues get ISSUE_ID --app APP for scripts"))
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
	stateChoice, err := prompt.choose(ctx, "Issue state", []string{"Open", "Resolved", "Ignored", "All states"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	sortChoice, err := prompt.choose(ctx, "Issue order", []string{"Most recently seen", "Verified customer impact (24 hours)"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	options := api.IssueListOptions{State: []string{"open", "resolved", "ignored", ""}[stateChoice], Sort: []string{"recent", "impact"}[sortChoice]}
	var selected api.Issue
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		page, err := client.ListIssuesWithOptions(readCtx, slug, options)
		cancel()
		if err != nil {
			return printErr("Could not list issues", err)
		}
		labels := []string{}
		for _, issue := range page.Items {
			if issue.AppID != app.ID || !alertIDPattern.MatchString(issue.ID) {
				return printErr("Invalid issue list", errors.New("issue does not match selected app or has an invalid ID"))
			}
			impact := "unknown"
			if issue.Impact24h != nil {
				impact = fmt.Sprint(issue.Impact24h.IdentifiedCustomers)
			}
			labels = append(labels, fmt.Sprintf("%s · %s · %s · verified customers %s", oneLine(issue.Title), oneLine(issue.State), issue.LastSeenAt.Format(time.RFC3339), impact))
		}
		more := page.NextCursor != ""
		if len(labels) == 0 && !more {
			PrintProgress(osStdout, "No matching issues on this page.")
			return 0
		}
		if more {
			labels = append(labels, "Show older/more issues")
		}
		labels = append(labels, "Cancel")
		choice, err := prompt.choose(ctx, "Choose an issue for "+slug+".", labels, 0)
		if err != nil {
			return startInputExit(err)
		}
		if choice == len(labels)-1 {
			return 0
		}
		if more && choice == len(page.Items) {
			if page.NextCursor == options.Cursor || seen[page.NextCursor] {
				return printErr("Invalid pagination", errors.New("server repeated an issue cursor"))
			}
			seen[page.NextCursor] = true
			options.Cursor = page.NextCursor
			continue
		}
		selected = page.Items[choice]
		break
	}
	if selected.ID == "" {
		return printErr("Issue page limit reached", errors.New("use issues list with explicit pagination"))
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "issues", "get", quoteLogCommandArg(selected.ID), "--app", quoteLogCommandArg(slug))
	_, _ = fmt.Fprintln(osStdout, "Inspection command (POSIX shells):\n"+strings.Join(command, " "))
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	detail, err := client.GetIssuePage(readCtx, slug, selected.ID, "", "", "", "")
	if err != nil {
		return printErr("Could not read selected issue", err)
	}
	if detail.Issue.AppID != app.ID || !sameBindingDeployment(detail.Issue.ID, selected.ID) {
		return printErr("Invalid issue detail", errors.New("detail does not match selected app and issue"))
	}
	if code := jsonOut(writeJSON(detail)); code != 0 {
		return code
	}
	for _, next := range []struct{ flag, cursor string }{{"--cursor", detail.NextEventCursor}, {"--release-cursor", detail.NextReleaseCursor}, {"--activity-cursor", detail.NextActivityCursor}} {
		if next.cursor != "" {
			_, _ = fmt.Fprintln(osStdout, "Next history page: "+strings.Join(append(append([]string{}, command...), next.flag, quoteLogCommandArg(next.cursor)), " "))
		}
	}
	return 0
}
