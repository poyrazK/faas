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

func cmdCronsRunsInteractive(slug string) int {
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
			target = oneLine(formatCronCommand(task))
		}
		labels = append(labels, fmt.Sprintf("%s · %s · %s · %s", task.ID, cronKindOrHTTP(task.Kind), oneLine(target), task.Schedule))
	}
	labels = append(labels, "Cancel")
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	choice, err := prompt.choose(ctx, "Choose a task to inspect for "+slug+".", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	if choice == len(tasks) {
		PrintProgress(osStdout, "History browsing canceled.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	task, err := client.GetCron(readCtx, tasks[choice].ID)
	cancel()
	if err != nil {
		return printErr("Could not read selected task", err)
	}
	if task.AppID != app.ID || !sameBindingDeployment(task.ID, tasks[choice].ID) {
		return printErr("Invalid selected task", errors.New("task does not match the selected app and ID"))
	}
	PrintProgress(osStdout, "App: %s; task: %s; kind: %s", slug, task.ID, cronKindOrHTTP(task.Kind))
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		history, err := client.ListCronRuns(readCtx, task.ID, cursor, 10)
		cancel()
		if err != nil {
			return printErr("Could not list task history", err)
		}
		printCronHistoryCommand(task.ID, cursor, "")
		if len(history.Runs) == 0 {
			message := "No runs available."
			if page > 0 {
				message = "No older runs available."
			}
			PrintProgress(osStdout, message)
			return 0
		}
		for _, run := range history.Runs {
			if !fireNowRequestIDPattern.MatchString(run.ID) {
				return printErr("Invalid run history", errors.New("a run has an invalid ID"))
			}
		}
		// This API has no next-cursor field. A full page may have older rows;
		// use its last ID, matching the existing --before contract.
		more := len(history.Runs) == 10
		for {
			labels = make([]string, 0, len(history.Runs)+2)
			for _, run := range history.Runs {
				labels = append(labels, fmt.Sprintf("%s · %s · %s · %s", run.ID, run.StartedAt.Format(time.RFC3339), run.Outcome, formatCronDuration(run.DurationMs)))
			}
			if more {
				labels = append(labels, "Show older runs")
			}
			labels = append(labels, "Done")
			selection, err := prompt.choose(ctx, "Choose a run to inspect.", labels, len(labels)-1)
			if err != nil {
				return startInputExit(err)
			}
			if selection == len(labels)-1 {
				return 0
			}
			if more && selection == len(history.Runs) {
				next := history.Runs[len(history.Runs)-1].ID
				if next == cursor || seen[next] {
					return printErr("Invalid history pagination", errors.New("the server repeated a history cursor"))
				}
				seen[next] = true
				cursor = next
				break
			}
			run := history.Runs[selection]
			renderCronRun(osStdout, run)
			if task.Kind != "command" {
				PrintProgress(osStdout, "HTTP history includes outcome, duration, and failure text; captured command output is unavailable.")
				continue
			}
			printCronHistoryCommand(task.ID, "", run.ID)
			readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			details, err := client.GetCronCommandRun(readCtx, task.ID, run.ID)
			cancel()
			if err != nil {
				return printErr("Could not load command run details", err)
			}
			if details.AppID != app.ID || !sameBindingDeployment(details.ID, run.ID) {
				return printErr("Invalid command run details", errors.New("the response does not match the selected app and run"))
			}
			renderCronCommandRunDetails(osStdout, osStderr, details)
		}
	}
	return printErr("History page limit reached", errors.New("use crons runs ID --before CURSOR to continue browsing older runs"))
}

func printCronHistoryCommand(taskID, cursor, runID string) {
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "crons", "runs", quoteLogCommandArg(taskID))
	if cursor != "" {
		command = append(command, "--before", quoteLogCommandArg(cursor))
	}
	if runID != "" {
		command = append(command, "--run", quoteLogCommandArg(runID))
	}
	_, _ = fmt.Fprintln(osStdout, "Equivalent command (POSIX shells):\n"+strings.Join(command, " "))
}
