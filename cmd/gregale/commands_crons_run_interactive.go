package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
)

func cmdCronsRunInteractive(slug string, timeout time.Duration) int {
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
	tasks, err := client.ListCrons(readCtx, slug)
	cancel()
	if err != nil {
		return printErr("Could not list scheduled tasks", err)
	}
	if len(tasks) == 0 {
		PrintProgress(osStdout, "No scheduled tasks available for %s.", slug)
		return 0
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	labels := make([]string, 0, len(tasks)+1)
	for _, task := range tasks {
		if task.AppID != app.ID || !cronIDPattern.MatchString(task.ID) {
			return printErr("Invalid task list", errors.New("a task does not match the selected app or has an invalid ID"))
		}
		target := task.Path
		if task.Kind == "command" {
			target = formatCronCommand(task)
		}
		labels = append(labels, fmt.Sprintf("%s · %s · %s · enabled=%t", task.ID, cronKindOrHTTP(task.Kind), oneLine(target), task.Enabled))
	}
	labels = append(labels, "Cancel")
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	choice, err := prompt.choose(ctx, "Choose a task to run now for "+slug+".", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	if choice == len(tasks) {
		PrintProgress(osStdout, "No manual run submitted.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	selected, err := client.GetCron(readCtx, tasks[choice].ID)
	cancel()
	if err != nil {
		return printErr("Could not read selected task", err)
	}
	if selected.AppID != app.ID || !sameBindingDeployment(selected.ID, tasks[choice].ID) {
		return printErr("Invalid selected task", errors.New("task does not match the selected app and ID"))
	}
	PrintProgress(osStdout, "Review manual run for app %s:", slug)
	renderCronInfo(osStdout, selected)
	PrintProgress(osStdout, "This submits one fire-now request. Server admission rules apply; following stops after %s. Stopping the wait does not cancel the request.", timeout)
	command := cronRunCommand("run", selected.ID)
	_, _ = fmt.Fprintln(osStdout, "Equivalent submission command (POSIX shells):\n"+command)
	confirmed, err := prompt.confirm(ctx, "Submit this task once now?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "No manual run submitted.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	latest, err := client.GetCron(readCtx, selected.ID)
	cancel()
	if err != nil {
		return printErr("Could not recheck selected task", err)
	}
	selected.LastFiredAt, latest.LastFiredAt = "", ""
	if !reflect.DeepEqual(selected, latest) {
		return printErr("Task changed during review", errors.New("run the guide again to review its current configuration"))
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	submitCtx, submitCancel := context.WithTimeout(ctx, 30*time.Second)
	resp, err := client.FireCron(submitCtx, selected.ID)
	submitCancel()
	if err != nil {
		return printErr("Could not confirm fire-now submission", fmt.Errorf("%w; inspect task history before submitting again because the request may have been accepted", err))
	}
	PrintProgress(osStdout, "Fire-now request enqueued: %s", oneLine(resp.RequestID))
	if !fireNowRequestIDPattern.MatchString(resp.RequestID) || resp.CronID != "" && !sameBindingDeployment(resp.CronID, selected.ID) {
		return printErr("Invalid fire-now receipt", errors.New("cannot follow the returned receipt; inspect task history before submitting again"))
	}
	_, _ = fmt.Fprintln(osStdout, "Inspect this request (reads only):\n"+cronRunCommand("fire-now", resp.RequestID))
	printCronHistoryCommand(selected.ID, "", "")
	return followGuidedCronRequest(ctx, client, selected.ID, resp.RequestID, timeout)
}

func cronRunCommand(verb, id string) string {
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "crons", verb, quoteLogCommandArg(id))
	return strings.Join(command, " ")
}

func followGuidedCronRequest(parent context.Context, client *Client, cronID, requestID string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	status := ""
	for {
		if err := ctx.Err(); err != nil {
			PrintProgress(osStdout, "Stopped following; the request may still continue. Inspect it with: %s", cronRunCommand("fire-now", requestID))
			if errors.Is(err, context.Canceled) {
				return 130
			}
			return 3
		}
		progress, err := client.GetFireCronRequest(ctx, requestID)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			PrintProgress(osStdout, "Resume inspection with: %s", cronRunCommand("fire-now", requestID))
			return printErr("Request was enqueued, but following failed", err)
		}
		if !sameBindingDeployment(progress.RequestID, requestID) || !sameBindingDeployment(progress.CronID, cronID) {
			return printErr("Invalid fire-now status", errors.New("the returned status does not match the submitted task and request"))
		}
		if progress.Status != status {
			renderFireNowStatus(osStdout, progress)
			status = progress.Status
		}
		if progress.OperationID != nil && *progress.OperationID != "" && (status == fireNowStatusSucceeded || status == fireNowStatusFailed || status == fireNowStatusCancelled) {
			PrintProgress(osStdout, "Operation: %s", oneLine(*progress.OperationID))
		}
		switch status {
		case fireNowStatusSucceeded, fireNowStatusFailed, fireNowStatusCancelled:
			if progress.TaskID != nil && fireNowRequestIDPattern.MatchString(*progress.TaskID) {
				printCronHistoryCommand(cronID, "", *progress.TaskID)
			}
			if status == fireNowStatusSucceeded {
				PrintProgress(osStdout, "Fire-now request completed; inspect the resulting run for execution details.")
				return 0
			}
			return 1
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
