package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func cmdJobsOccurrencesInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs occurrences NAME for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, selected, err := chooseJob(ctx, client, prompt)
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
	cursor := ""
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		current, err := client.GetJob(readCtx, job.Name)
		if err != nil {
			cancel()
			return printErr("Could not recheck Job", err)
		}
		if current.ID != job.ID || current.AccountID != job.AccountID || current.Name != job.Name {
			cancel()
			return printErr("Job changed", errors.New("the selected Job identity changed"))
		}
		page, err := client.ListJobScheduleOccurrences(readCtx, job.Name, 20, cursor)
		cancel()
		if err != nil {
			return printErr("Could not read schedule history", err)
		}
		labels := []string{}
		ids := map[string]bool{}
		for _, occurrence := range page.Occurrences {
			if !jobRunIDPattern.MatchString(occurrence.ID) || ids[occurrence.ID] || occurrence.JobRunID != "" && !jobRunIDPattern.MatchString(occurrence.JobRunID) {
				return printErr("Invalid occurrence history", errors.New("an occurrence has an invalid or repeated identity"))
			}
			ids[occurrence.ID] = true
			labels = append(labels, fmt.Sprintf("%s · %s · %s", occurrence.ScheduledFor.Format(time.RFC3339), oneLine(occurrence.Status), oneLine(occurrence.Reason)))
		}
		more := page.NextBefore != ""
		if len(labels) == 0 && !more {
			PrintProgress(osStdout, "No occurrences available on this history page for %s.", job.Name)
			return 0
		}
		if more {
			labels = append(labels, "Show older occurrences")
		}
		labels = append(labels, "Cancel")
		choice, err := prompt.choose(ctx, "Choose a scheduled occurrence for "+job.Name+".", labels, 0)
		if err != nil {
			return startInputExit(err)
		}
		if choice == len(labels)-1 {
			return 0
		}
		if more && choice == len(page.Occurrences) {
			if page.NextBefore == cursor || seen[page.NextBefore] {
				return printErr("Invalid pagination", errors.New("server repeated a schedule history cursor"))
			}
			seen[page.NextBefore] = true
			cursor = page.NextBefore
			continue
		}
		occurrence := page.Occurrences[choice]
		PrintProgress(osStdout, "Job: %s; occurrence: %s\nScheduled: %s; revision: %d\nStatus: %s\nReason: %s\nOutcome: %s\nDecision: %s", job.Name, occurrence.ID, occurrence.ScheduledFor.Format(time.RFC3339), occurrence.ScheduleRevision, oneLine(occurrence.Status), oneLine(occurrence.Reason), oneLine(occurrence.OutcomeCode), oneLine(workDecisionLabel(occurrence.WorkDecision)))
		if occurrence.StartDeadlineAt != nil {
			PrintProgress(osStdout, "Start deadline: %s", occurrence.StartDeadlineAt.Format(time.RFC3339))
		}
		if occurrence.StartedAt != nil {
			PrintProgress(osStdout, "Started: %s", occurrence.StartedAt.Format(time.RFC3339))
		}
		if occurrence.FinishedAt != nil {
			PrintProgress(osStdout, "Finished: %s", occurrence.FinishedAt.Format(time.RFC3339))
		}
		if occurrence.BlockingOccurrenceID != "" {
			PrintProgress(osStdout, "Blocking occurrence: %s", oneLine(occurrence.BlockingOccurrenceID))
		}
		if occurrence.SchedulePolicy != nil {
			policy, _ := json.Marshal(occurrence.SchedulePolicy)
			PrintProgress(osStdout, "Scheduling policy: %s", policy)
		}
		historyCommand := append(append([]string{}, command...), "jobs", "occurrences", quoteLogCommandArg(job.Name), "--limit", "20")
		if cursor != "" {
			historyCommand = append(historyCommand, "--cursor", quoteLogCommandArg(cursor))
		}
		_, _ = fmt.Fprintln(osStdout, "History command (POSIX shells):\n"+strings.Join(historyCommand, " "))
		if occurrence.JobRunID != "" {
			readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
			run, err := client.GetJobRun(readCtx, job.Name, occurrence.JobRunID)
			cancel()
			if err != nil {
				return printErr("Could not read associated run", err)
			}
			if run.ID != occurrence.JobRunID || run.JobID != job.ID || run.AccountID != job.AccountID || run.OccurrenceID != occurrence.ID {
				return printErr("Invalid associated run", errors.New("run does not match the selected Job and occurrence"))
			}
			followCommand := append(append([]string{}, command...), "jobs", "wait", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID), "--timeout", "10m")
			_, _ = fmt.Fprintln(osStdout, "Follow command (POSIX shells):\n"+strings.Join(followCommand, " "))
		} else {
			PrintProgress(osStdout, "No Job run is associated with this occurrence in the returned history.")
		}
		again, err := prompt.confirm(ctx, "Inspect another occurrence on this page?")
		if err != nil {
			return startInputExit(err)
		}
		if !again {
			return 0
		}
		// Reread this page so evolving occurrence decisions are visible.
	}
	return printErr("History page limit reached", errors.New("use jobs occurrences NAME with explicit pagination"))
}
