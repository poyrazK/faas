package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// devStopPromptable reports whether --stop can ask before deleting data:
// only a person at a terminal is asked. Scripts, JSON output and the child
// loops of `gregale dev --all` (whose stdin is not a terminal) keep the
// existing behaviour.
func devStopPromptable() bool {
	return !jsonOutput && !nonInteractive && stdinIsTTY() && stdoutIsTTY()
}

// confirmDevStop asks before --stop deletes a developer database or bucket.
// An environment without either is stopped without a question, since
// nothing is lost that the next `gregale dev` does not recreate.
func confirmDevStop(ctx context.Context, client *Client, project, workspaceID, appSlug string, yes bool) int {
	if yes || !devStopPromptable() {
		return 0
	}
	var (
		session api.DevSessionResponse
		err     error
	)
	if appSlug != "" {
		// Selected by app: the workspace lookup would find a different
		// environment, so read the backing app's resources by listing.
		return confirmYesNo(fmt.Sprintf("Stop developer environment %s and delete its database and buckets, if any?", appSlug), "--yes", "stopping the developer environment")
	}
	session, err = client.GetDevSession(ctx, project, workspaceID)
	if err != nil {
		return 0 // nothing to describe; the delete reports a missing environment
	}
	var data []string
	if session.Postgres != nil {
		data = append(data, "database "+session.Postgres.Name)
	}
	for _, bucket := range session.Buckets {
		data = append(data, "bucket "+bucket.Bucket.Name)
	}
	if len(data) == 0 {
		return 0
	}
	question := fmt.Sprintf("Stop developer environment %s and delete %s?", project, strings.Join(data, ", "))
	return confirmYesNo(question, "--yes", "stopping the developer environment")
}

// cmdDevList prints the account's developer environments wherever they were
// started, so one whose checkout moved, was renamed or deleted can be found
// and stopped with `gregale dev --stop --name PROJECT --app SLUG`.
func cmdDevList(args []string) int {
	if len(args) != 0 {
		PrintUsage(osStderr, "usage: gregale dev list", "dev")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	list, err := client.GetDevSessions(ctx)
	if err != nil {
		return printErr("Could not list developer environments", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if len(list.Sessions) == 0 {
		PrintOK(osStdout, "No developer environments.")
		return 0
	}
	rows := [][]string{{"PROJECT", "APP", "STATE", "EXPIRES", "URL"}}
	for _, item := range list.Sessions {
		state := item.State
		if state == "" {
			state = "open"
		}
		expires := "-"
		if !item.ExpiresAt.IsZero() {
			expires = item.ExpiresAt.UTC().Format("2006-01-02 15:04 MST")
		}
		rows = append(rows, []string{item.Project, item.App, state, expires, item.URL})
	}
	printDevListTable(rows)
	_, _ = fmt.Fprintln(osStdout, "\nStop one from any directory: gregale dev --stop --name PROJECT --app APP")
	return 0
}

func printDevListTable(rows [][]string) {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], len(cell))
		}
	}
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			if i == len(row)-1 {
				b.WriteString(cell)
				break
			}
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", widths[i]-len(cell)+2))
		}
		_, _ = fmt.Fprintln(osStdout, b.String())
	}
}
