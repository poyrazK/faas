package main

// App task history (ADR-230).
//
//	gregale app tasks [list] <app> [--limit N] [--offset N]
//	gregale app tasks show <app> <task-id>
//	gregale app tasks cancel <app> <task-id>
//
// `app exec --detach`, release commands and binding probes leave task receipts
// behind; until these commands the CLI printed a task ID that nothing could
// read back afterwards. They are thin views over the existing list, get and
// cancel routes.

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

const appTasksUsage = "usage: gregale app tasks [list] <app> [--limit N] [--offset N]\n       gregale app tasks show <app> <task-id>\n       gregale app tasks cancel <app> <task-id>"

func cmdAppTasks(slug string, args []string) int {
	verb := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	switch verb {
	case "list", "ls":
		return cmdAppTasksList(slug, args)
	case "show", "get":
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
	id, ok := appTaskIDArg(slug, args, "show")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	task, err := client.GetAppTask(context.Background(), slug, id)
	if err != nil {
		return printErr("Could not load app task", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(task))
	}
	renderAppTaskDetails(task)
	return 0
}

func cmdAppTaskCancel(slug string, args []string) int {
	id, ok := appTaskIDArg(slug, args, "cancel")
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

func appTaskIDArg(slug string, args []string, verb string) (string, bool) {
	if slug == "" || len(args) != 1 || strings.HasPrefix(args[0], "-") {
		PrintUsage(osStderr, appTasksUsage, "apps")
		return "", false
	}
	id := strings.TrimSpace(args[0])
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		printErr("Invalid task ID", fmt.Errorf("%q is not a task ID; list them with gregale app tasks %s, then run gregale app tasks %s %s <task-id>", id, quoteCLIArg(slug), verb, quoteCLIArg(slug)))
		return "", false
	}
	return id, true
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
	var line string
	if task.CommandShell && len(task.Command) == 1 {
		line = task.Command[0]
	} else {
		quoted := make([]string, len(task.Command))
		for i, arg := range task.Command {
			quoted[i] = quoteCLIArg(arg)
		}
		line = strings.Join(quoted, " ")
	}
	if max > 0 && len([]rune(line)) > max {
		line = string([]rune(line)[:max-1]) + "…"
	}
	return line
}
