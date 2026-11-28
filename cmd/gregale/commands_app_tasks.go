package main

// App task history (ADR-230).
//
//	gregale app tasks [list] <app> [--limit N] [--offset N]
//	gregale app tasks show <app> <task-id> [--follow]
//	gregale app tasks cancel <app> <task-id>
//
// `app exec --detach`, release commands and binding probes leave task receipts
// behind; until these commands the CLI printed a task ID that nothing could
// read back afterwards. They are thin views over the existing list, get and
// cancel routes.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

const appTasksUsage = "usage: gregale app tasks [list] <app> [--limit N] [--offset N]\n       gregale app tasks show <app> <task-id> [--follow] [--poll-interval D] [--wait-timeout D]\n       gregale app tasks cancel <app> <task-id>"

func cmdAppTasks(slug string, args []string) int {
	verb := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	switch verb {
	case "list":
		return cmdAppTasksList(slug, args)
	case "show":
		return cmdAppTaskShow(slug, args)
	case "cancel":
		return cmdAppTaskCancel(slug, args)
	default:
		return printErr("Unknown tasks command", fmt.Errorf("%q is not a tasks command; use list, show or cancel\n%s", verb, appTasksUsage))
	}
}

func cmdAppTasksList(slug string, args []string) int {
	fs := newFlagSet("app-tasks", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "tasks per page (1..200)")
	offset := fs.Int("offset", 0, "tasks to skip (non-negative)")
	flags, positionals := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || slug == "" || len(positionals) != 0 {
		PrintUsage(osStderr, appTasksUsage, "apps")
		return 1
	}
	if err := validateCLILimit("limit", *limit, 200); err != nil {
		return printErr("Invalid --limit", err)
	}
	if err := validateCLIOffset("offset", *offset); err != nil {
		return printErr("Invalid --offset", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	page, err := client.ListAppTasks(context.Background(), slug, *limit, *offset)
	if err != nil {
		return printErr("Could not list app tasks", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Tasks) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no tasks)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tKIND\tEXIT\tCREATED\tDURATION\tCOMMAND")
	for _, task := range page.Tasks {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			task.ID, task.Status, task.Kind, appTaskExitLabel(task), task.CreatedAt, appTaskDuration(task), appTaskCommandLine(task, 60))
	}
	_ = tw.Flush()
	if page.NextOffset > 0 && len(page.Tasks) == page.Limit {
		_, _ = fmt.Fprintf(osStderr, "More tasks: gregale app tasks %s --offset %d\n", quoteCLIArg(slug), page.NextOffset)
	}
	return 0
}

func cmdAppTaskShow(slug string, args []string) int {
	fs := newFlagSet("app-tasks-show", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "wait for the task to finish, then print its result")
	pollInterval := fs.Duration("poll-interval", executionPollIntervalDefault, "status polling interval with --follow")
	waitTimeout := fs.Duration("wait-timeout", appTaskWaitTimeoutDefault, "how long --follow waits")
	flags, positionals := splitArgsForFlags(args, "follow")
	if err := fs.Parse(flags); err != nil {
		PrintUsage(osStderr, appTasksUsage, "apps")
		return 1
	}
	slug, id, ok := appTaskIDArg(slug, positionals, "show")
	if !ok {
		return 1
	}
	if *follow && (*pollInterval <= 0 || *waitTimeout <= 0) {
		return printErr("Invalid --follow timing", fmt.Errorf("--poll-interval and --wait-timeout must be positive"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	task, err := client.GetAppTask(context.Background(), slug, id)
	if err != nil {
		return printErr("Could not load app task", err)
	}
	if *follow {
		return followAppTask(client, slug, task, *pollInterval, *waitTimeout)
	}
	if jsonOutput {
		return jsonOut(writeJSON(task))
	}
	renderAppTaskDetails(task)
	return 0
}

// followAppTask polls a task until it finishes, reporting each status change
// on stderr, then prints its result the way attached app exec does and exits
// with the task's exit code. Interrupting stops following; the task keeps
// running. Output is read when the task finishes: the runtime returns it
// whole, not line by line.
func followAppTask(client *Client, slug string, task api.AppTaskResponse, pollInterval, waitTimeout time.Duration) int {
	interrupt, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupt, waitTimeout)
	defer cancel()
	last := task.Status
	if !jsonOutput && !task.Status.Terminal() {
		_, _ = fmt.Fprintf(osStderr, "Task %s is %s; waiting for it to finish (Ctrl-C stops following, not the task)\n", task.ID, task.Status)
	}
	for !task.Status.Terminal() {
		select {
		case <-ctx.Done():
			hint := fmt.Sprintf("gregale app tasks show %s %s --follow", quoteCLIArg(slug), task.ID)
			if errors.Is(interrupt.Err(), context.Canceled) {
				PrintWarn(osStderr, "stopped following task %s (status=%s); it keeps running. Resume with %s", task.ID, task.Status, hint)
				return 130
			}
			return printErr("Stopped following; the task is still "+string(task.Status), fmt.Errorf("waited %s; resume with %s", waitTimeout, hint))
		case <-time.After(pollInterval):
		}
		next, err := client.GetAppTask(ctx, slug, task.ID)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			return printErr("Could not load app task", err)
		}
		task = next
		if task.Status != last && !jsonOutput && !task.Status.Terminal() {
			_, _ = fmt.Fprintf(osStderr, "Task %s is %s\n", task.ID, task.Status)
		}
		last = task.Status
	}
	if jsonOutput {
		if err := writeJSON(task); err != nil {
			return jsonOut(err)
		}
		return appTaskExitCode(task)
	}
	return renderAppTaskTerminal(task)
}

func cmdAppTaskCancel(slug string, args []string) int {
	slug, id, ok := appTaskIDArg(slug, args, "cancel")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	task, err := client.CancelAppTask(context.Background(), slug, id)
	if err != nil {
		return printErr("Could not cancel app task", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(task))
	}
	switch {
	case task.Status == api.AppTaskStatusCancelled:
		PrintOK(osStdout, "Task %s cancelled.", task.ID)
	case task.Status.Terminal():
		PrintWarn(osStdout, "Task %s had already finished (status=%s); nothing to cancel.", task.ID, task.Status)
	default:
		// Restoring or running work is acknowledged after VM teardown.
		PrintOK(osStdout, "Cancellation requested for task %s (status=%s); follow it with gregale app tasks show %s %s", task.ID, task.Status, quoteCLIArg(slug), task.ID)
	}
	return 0
}

// appTaskIDArg returns the app and task ID for show and cancel. In a linked
// checkout `app tasks show <task-id>` reaches here with the task ID in the
// app position (a UUID is a valid slug), so a lone UUID names the task and
// the linked app is used.
func appTaskIDArg(slug string, args []string, verb string) (string, string, bool) {
	if len(args) == 0 && validTaskID(slug) {
		app, err := resolveAppFlagOrContext("")
		if err != nil || app == "" {
			printErr("Missing app", fmt.Errorf("name the app: gregale app tasks %s <app> %s", verb, slug))
			return "", "", false
		}
		slug, args = app, []string{slug}
	}
	if slug == "" || len(args) != 1 || strings.HasPrefix(args[0], "-") {
		PrintUsage(osStderr, appTasksUsage, "apps")
		return "", "", false
	}
	id := strings.TrimSpace(args[0])
	if !validTaskID(id) {
		printErr("Invalid task ID", fmt.Errorf("%q is not a task ID; list them with gregale app tasks %s, then run gregale app tasks %s %s <task-id>", id, quoteCLIArg(slug), verb, quoteCLIArg(slug)))
		return "", "", false
	}
	return slug, id, true
}

func validTaskID(value string) bool {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil && parsed != uuid.Nil
}

// renderAppTaskDetails prints the receipt on stdout, including the captured
// output tails, so `tasks show ID > file` keeps the whole record.
func renderAppTaskDetails(task api.AppTaskResponse) {
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	row := func(label, value string) {
		if value != "" {
			_, _ = fmt.Fprintf(tw, "%s:\t%s\n", label, value)
		}
	}
	row("Task", task.ID)
	row("Status", string(task.Status))
	row("Kind", string(task.Kind))
	row("Command", appTaskCommandLine(task, 0))
	row("Deployment", task.DeploymentID)
	row("Exit code", appTaskExitLabel(task))
	if task.Failure != nil {
		row("Failure", task.Failure.Code+": "+task.Failure.Message)
	}
	if task.AttemptCount > 1 || task.RetryMax > 0 {
		row("Attempts", fmt.Sprintf("%d (retries allowed: %d)", task.AttemptCount, task.RetryMax))
	}
	row("Created", task.CreatedAt)
	row("Started", derefString(task.StartedAt))
	row("Finished", derefString(task.FinishedAt))
	row("Duration", appTaskDuration(task))
	row("Cancel requested", derefString(task.CancelRequestedAt))
	if task.Interactive {
		row("Interactive", "yes")
	}
	row("Timeout", fmt.Sprintf("%ds", task.TimeoutSeconds))
	_ = tw.Flush()
	for _, stream := range []struct{ name, text string }{{"stdout", task.StdoutTail}, {"stderr", task.StderrTail}} {
		if stream.text == "" {
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "\n--- %s ---\n%s", stream.name, stream.text)
		if !strings.HasSuffix(stream.text, "\n") {
			_, _ = fmt.Fprintln(osStdout)
		}
	}
	if task.OutputTruncated {
		PrintWarn(osStderr, "output was truncated at %d bytes (stdout and stderr combined); only the tail is kept", task.MaxOutputBytes)
	}
}

func appTaskExitLabel(task api.AppTaskResponse) string {
	if task.ExitCode != nil {
		return fmt.Sprintf("%d", *task.ExitCode)
	}
	return "-"
}

func appTaskDuration(task api.AppTaskResponse) string {
	if task.StartedAt == nil {
		return "-"
	}
	started, err := time.Parse(time.RFC3339Nano, *task.StartedAt)
	if err != nil {
		return "-"
	}
	end := time.Now()
	if task.FinishedAt != nil {
		if finished, err := time.Parse(time.RFC3339Nano, *task.FinishedAt); err == nil {
			end = finished
		}
	} else if task.Status.Terminal() {
		return "-"
	}
	return end.Sub(started).Round(100 * time.Millisecond).String()
}

// appTaskCommandLine renders argv for display; max > 0 shortens it.
func appTaskCommandLine(task api.AppTaskResponse, max int) string {
	line := task.CommandLine()
	if max > 0 && len([]rune(line)) > max {
		line = string([]rune(line)[:max-1]) + "…"
	}
	return line
}
