package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

func cmdAlertActionsInteractive(slug string, timeout, interval time.Duration) int {
	if !validCLISlug(slug) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	app, err := client.GetApp(readCtx, slug)
	if err != nil {
		cancel()
		return printErr("Could not read app", err)
	}
	rows, err := client.ListAlertRollbacks(readCtx, slug)
	cancel()
	if err != nil {
		return printErr("Could not list alert actions", err)
	}
	if len(rows) == 0 {
		PrintProgress(osStdout, "No automatic rollback actions for %s.", slug)
		return 0
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].FiredAt.Equal(rows[j].FiredAt) {
			return rows[i].FiredAt.After(rows[j].FiredAt)
		}
		return rows[i].ID < rows[j].ID
	})
	labels := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		if row.AppID != app.ID || !alertIDPattern.MatchString(row.ID) {
			return printErr("Invalid action list", errors.New("an action does not match the selected app or has an invalid ID"))
		}
		labels = append(labels, fmt.Sprintf("%s · %s · rule %s · %s", row.FiredAt.UTC().Format(time.RFC3339), oneLine(row.Status), oneLine(row.RuleID), row.ID))
	}
	labels = append(labels, "Cancel")
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	choice, err := prompt.choose(ctx, "Choose an automatic rollback action for "+slug+".", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	if choice == len(rows) {
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	selected, err := client.GetAlertRollback(readCtx, slug, rows[choice].ID)
	cancel()
	if err != nil {
		return printErr("Could not read selected action", err)
	}
	if !alertActionReceiptMatches(selected, rows[choice]) || selected.ServiceRequestID != "" && selected.ServiceRequestID != selected.ID || selected.RollbackOperationID != "" && selected.RollbackOperationID != selected.ID {
		return printErr("Invalid selected action", errors.New("the action does not match the selected app, fire or deployment pair"))
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "alerts", "actions", "--app", quoteLogCommandArg(slug), "--fire", quoteLogCommandArg(selected.ID))
	_, _ = fmt.Fprintln(osStdout, "Inspection command (POSIX shells):\n"+strings.Join(command, " "))
	if code := outputAlertRollback(selected); code != 0 {
		return code
	}
	done, _ := alertActionFinished(selected)
	if done {
		return 0
	}
	follow, err := prompt.confirm(ctx, "Follow this action until it completes?")
	if err != nil {
		return startInputExit(err)
	}
	if !follow {
		return 0
	}
	command = append(command, "--wait", "--timeout", timeout.String(), "--poll-interval", interval.String())
	_, _ = fmt.Fprintln(osStdout, "Resume command (POSIX shells):\n"+strings.Join(command, " "))
	PrintProgress(osStdout, "Following action %s for up to %s.", selected.ID, timeout)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	last, err := waitAlertRollback(waitCtx, client, slug, selected, interval)
	if code := outputAlertRollback(last); code != 0 {
		return code
	}
	return checkedRollbackCLIError(ctx, "Alert action wait failed", err)
}
