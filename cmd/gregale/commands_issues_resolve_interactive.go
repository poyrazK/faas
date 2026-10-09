package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdIssuesResolveInteractive(args []string) int {
	fs := newFlagSet("issues resolve", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose an issue and fixing release")
	appFlag := fs.String("app", "", "app slug")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 {
		return printErr("Invalid arguments", errors.New("use issues resolve --interactive with optional --app"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use issues resolve ISSUE_ID --app APP --deployment ID for scripts"))
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
		return printErr("Could not select issue", err)
	}
	if !selected {
		return 0
	}
	var choice int
	seen := map[string]bool{}
	var releases []api.DeploymentResponse
	releaseCursors := map[int]string{0: ""}
	seen = map[string]bool{}
	choice, err = chooseJobLogPage(ctx, prompt, "Choose the release that fixed this issue.", func(ctx context.Context, offset int) ([]string, int, error) {
		page, err := client.ListAppDeployments(ctx, slug, releaseCursors[offset], 20)
		releases = page.Items
		labels := []string{}
		for _, release := range releases {
			if release.AppID != app.ID || !deploymentIDPattern.MatchString(release.ID) {
				return nil, -1, errors.New("release does not match selected app")
			}
			labels = append(labels, deploymentLabel(release)+" · "+oneLine(release.Status)+" · scope "+oneLine(scopeOrDefault(release.Scope))+" · "+oneLine(release.CreatedAt))
		}
		next := -1
		if page.NextBefore != "" {
			if seen[page.NextBefore] || page.NextBefore == releaseCursors[offset] {
				return nil, -1, errors.New("repeated release cursor")
			}
			seen[page.NextBefore] = true
			next = offset + 1
			releaseCursors[next] = page.NextBefore
		}
		return labels, next, err
	})
	if err != nil {
		return printErr("Could not select fixing release", err)
	}
	if choice < 0 {
		return 0
	}
	release := releases[choice]
	read := func() (api.Issue, api.DeploymentResponse, error) {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		detail, err := client.GetIssuePage(readCtx, slug, issue.ID, "", "", "", "")
		if err != nil {
			return detail.Issue, release, err
		}
		summary, err := client.GetAppDeploymentSummary(readCtx, slug, release.ID)
		if err != nil {
			return detail.Issue, release, err
		}
		if detail.Issue.AppID != app.ID || detail.Issue.ID != issue.ID || summary.Deployment.AppID != app.ID || summary.Deployment.ID != release.ID {
			return detail.Issue, summary.Deployment, errors.New("selected issue or release identity changed")
		}
		return detail.Issue, summary.Deployment, nil
	}
	current, currentRelease, err := read()
	if err != nil {
		return printErr("Could not read resolution targets", err)
	}
	if current.State != "open" {
		return printErr("Issue state changed", errors.New("choose an open issue again"))
	}
	PrintProgress(osStdout, "Resolve issue: %s (%s)\nApp: %s; environment: %s\nFixing release: %s (%s); status: %s; scope: %s", oneLine(current.Title), current.ID, slug, oneLine(current.Environment), deploymentLabel(currentRelease), currentRelease.ID, oneLine(currentRelease.Status), oneLine(scopeOrDefault(currentRelease.Scope)))
	PrintProgress(osStdout, "This records your selected fixing release; it does not deploy code or verify that the failure is fixed.")
	confirmed, err := prompt.confirm(ctx, "Mark this issue resolved in this release?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Issue resolution canceled.")
		return 0
	}
	latest, latestRelease, err := read()
	if err != nil {
		return printErr("Could not recheck resolution targets", err)
	}
	if !reflect.DeepEqual(latest, current) || !reflect.DeepEqual(latestRelease, currentRelease) {
		return printErr("Resolution targets changed", errors.New("run the command again to review current issue and release"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resolved, err := client.ActOnIssue(writeCtx, slug, current.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: currentRelease.ID})
	if err != nil {
		return printErr("Could not resolve issue", err)
	}
	if resolved.ID != current.ID || resolved.AppID != app.ID || resolved.FixedDeploymentID != currentRelease.ID || resolved.State != "resolved" {
		return printErr("Invalid resolution receipt", errors.New("inspect the issue before taking further action"))
	}
	PrintOK(osStdout, "Issue %s resolved in %s.", resolved.ID, deploymentLabel(currentRelease))
	return 0
}

func chooseOpenIssue(ctx context.Context, client *api.Client, prompt *startPrompt, slug, appID string) (api.Issue, bool, error) {
	return chooseIssueInState(ctx, client, prompt, slug, appID, "open")
}

func chooseIssueInState(ctx context.Context, client *api.Client, prompt *startPrompt, slug, appID, state string) (api.Issue, bool, error) {
	var issues []api.Issue
	cursors := map[int]string{0: ""}
	seen := map[string]bool{}
	choice, err := chooseJobLogPage(ctx, prompt, "Choose a "+state+" issue.", func(ctx context.Context, offset int) ([]string, int, error) {
		page, err := client.ListIssues(ctx, slug, state, "", cursors[offset])
		issues = page.Items
		labels := []string{}
		for _, issue := range issues {
			if issue.AppID != appID || !alertIDPattern.MatchString(issue.ID) || issue.State != state {
				return nil, -1, errors.New("issue does not match selected app and requested state")
			}
			labels = append(labels, oneLine(issue.Title)+" · "+issue.LastSeenAt.Format(time.RFC3339))
		}
		next := -1
		if page.NextCursor != "" {
			if seen[page.NextCursor] || page.NextCursor == cursors[offset] {
				return nil, -1, errors.New("repeated issue cursor")
			}
			seen[page.NextCursor] = true
			next = offset + 1
			cursors[next] = page.NextCursor
		}
		return labels, next, err
	})
	if err != nil {
		return api.Issue{}, false, err
	}
	if choice < 0 {
		return api.Issue{}, false, nil
	}
	issue := issues[choice]
	return issue, true, nil
}
