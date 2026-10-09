package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsLogsInteractiveArgs(args []string) int {
	fs := newFlagSet("jobs logs", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose a Job, run, and task")
	maxBytes := fs.Int("max-bytes", api.DefaultJobTaskLogMaxBytes, "maximum log bytes")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 || *maxBytes < 1 || *maxBytes > api.MaxJobTaskLogMaxBytes {
		return printErr("Invalid interactive log flags", errors.New("use jobs logs --interactive with optional --max-bytes"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs logs NAME RUN_ID TASK_INDEX for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, run, task, selected, err := chooseJobTask(ctx, client, prompt, false)
	if err != nil {
		return jobLogPickerError(err)
	}
	if !selected {
		return 0
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "jobs", "logs", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID), strconv.Itoa(task.TaskIndex), "--max-bytes", strconv.Itoa(*maxBytes))
	_, _ = fmt.Fprintln(osStdout, "Equivalent command (POSIX shells):\n"+strings.Join(command, " "))
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	logs, err := client.GetJobTaskLogsWithMaxBytes(readCtx, job.Name, run.ID, task.TaskIndex, *maxBytes)
	if err != nil {
		return printErr("Logs request failed", err)
	}
	return renderJobTaskLogs(logs)
}

func jobLogPickerError(err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return printErr("Could not select Job logs", err)
}

func chooseJobLogPage(ctx context.Context, prompt *startPrompt, title string, load func(context.Context, int) ([]string, int, error)) (int, error) {
	offset := 0
	for page := 0; page < 100; page++ {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		labels, next, err := load(readCtx, offset)
		cancel()
		if err != nil {
			return -1, err
		}
		count := len(labels)
		more := next >= 0
		if count == 0 && !more {
			PrintProgress(osStdout, "No entries available on this page.")
			return -1, nil
		}
		if more {
			labels = append(labels, "Show more")
		}
		labels = append(labels, "Cancel")
		choice, err := prompt.choose(ctx, title, labels, 0)
		if err != nil {
			return -1, err
		}
		if choice == len(labels)-1 {
			return -1, nil
		}
		if more && choice == count {
			if next <= offset {
				return -1, errors.New("server returned a repeated or backwards page offset")
			}
			offset = next
			continue
		}
		return choice, nil
	}
	return -1, errors.New("page limit reached; use the explicit jobs logs command")
}

func chooseJobTask(ctx context.Context, client *api.Client, prompt *startPrompt, retryOnly bool) (api.JobResponse, api.JobRunResponse, api.JobTaskResponse, bool, error) {
	var jobs []api.JobResponse
	choice, err := chooseJobLogPage(ctx, prompt, "Choose a Job.", func(ctx context.Context, offset int) ([]string, int, error) {
		page, err := client.ListJobs(ctx, 20, offset)
		jobs = page.Jobs
		labels := []string{}
		for _, job := range jobs {
			if !jobSlugPattern.MatchString(job.Name) || !jobRunIDPattern.MatchString(job.ID) {
				return nil, -1, errors.New("invalid Job identity")
			}
			labels = append(labels, oneLine(job.Name))
		}
		return labels, page.NextOffset, err
	})
	if err != nil {
		return api.JobResponse{}, api.JobRunResponse{}, api.JobTaskResponse{}, false, err
	}
	if choice < 0 {
		return api.JobResponse{}, api.JobRunResponse{}, api.JobTaskResponse{}, false, nil
	}
	job := jobs[choice]
	var runs []api.JobRunResponse
	choice, err = chooseJobLogPage(ctx, prompt, "Choose a run for "+job.Name+".", func(ctx context.Context, offset int) ([]string, int, error) {
		page, err := client.ListJobRunsPage(ctx, job.Name, 20, offset)
		runs = page.Runs
		labels := []string{}
		for _, run := range runs {
			if run.JobID != job.ID || run.AccountID != job.AccountID || !jobRunIDPattern.MatchString(run.ID) {
				return nil, -1, errors.New("run does not belong to the selected Job")
			}
			labels = append(labels, fmt.Sprintf("%s · %s · %s", oneLine(run.CreatedAt), oneLine(run.AggregateStatus), run.ID))
		}
		return labels, page.NextOffset, err
	})
	if err != nil {
		return api.JobResponse{}, api.JobRunResponse{}, api.JobTaskResponse{}, false, err
	}
	if choice < 0 {
		return api.JobResponse{}, api.JobRunResponse{}, api.JobTaskResponse{}, false, nil
	}
	run := runs[choice]
	var tasks []api.JobTaskResponse
	choice, err = chooseJobLogPage(ctx, prompt, "Choose a task.", func(ctx context.Context, offset int) ([]string, int, error) {
		page, err := client.ListJobRunTasksPage(ctx, job.Name, run.ID, 20, offset)
		tasks = nil
		labels := []string{}
		for _, task := range page.Tasks {
			if task.RunID != run.ID || task.TaskIndex < 0 {
				return nil, -1, errors.New("task does not belong to the selected run")
			}
			if retryOnly && !jobTaskRetryStatus(task.Status) {
				continue
			}
			tasks = append(tasks, task)
			labels = append(labels, fmt.Sprintf("Task %d · %s · attempt %d · %s", task.TaskIndex, oneLine(task.Status), task.Attempt, oneLine(task.ErrorMessage)))
		}
		return labels, page.NextOffset, err
	})
	if err != nil {
		return api.JobResponse{}, api.JobRunResponse{}, api.JobTaskResponse{}, false, err
	}
	if choice < 0 {
		return api.JobResponse{}, api.JobRunResponse{}, api.JobTaskResponse{}, false, nil
	}
	task := tasks[choice]
	return job, run, task, true, nil
}
