package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type jobOverviewRun struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Tasks      int    `json:"tasks"`
	Running    int    `json:"running"`
	Succeeded  int    `json:"succeeded"`
	Failed     int    `json:"failed"`
	Cancelled  int    `json:"cancelled"`
	DeadLetter int    `json:"dead_letter"`
}

type jobOverviewAction struct {
	Purpose string `json:"purpose"`
	Command string `json:"command"`
}

type jobOverviewCheck struct {
	Passed  bool     `json:"passed"`
	Reasons []string `json:"reasons"`
}

func cmdJobsOverview(args []string) int {
	fs := newFlagSet("jobs overview", flag.ContinueOnError)
	check := fs.Bool("check", false, "exit 0 only for an active Job, ready image, and succeeded latest run")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() > 1 {
		return printErr("Invalid overview target", fmt.Errorf("use jobs overview [JOB]"))
	}
	name := fs.Arg(0)
	if name == "" {
		d, path, err := readJobDefaultsForCommand([]string{"jobs", "overview", "my-job"})
		if err != nil {
			return printJobDefaultsSelectionError("Could not read Job defaults", err)
		}
		name = d.Job
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, name)
		}
	}
	if !jobSlugPattern.MatchString(name) {
		return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
	}
	connection := configuredDisplayConnection()
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	job, err := client.GetJob(ctx, name)
	if err != nil {
		return printErr("Could not read Job", err)
	}
	if job.Name != name || job.ID == "" || job.AccountID == "" {
		return printErr("Invalid Job", fmt.Errorf("server did not identify the requested Job and account"))
	}
	receipt := struct {
		Job                 string              `json:"job"`
		State               string              `json:"state"`
		ImageStatus         string              `json:"image_status"`
		ReadinessAssessment string              `json:"readiness_assessment"`
		RunAssessment       string              `json:"run_assessment"`
		LatestRun           *jobOverviewRun     `json:"latest_run"`
		Actions             []jobOverviewAction `json:"actions"`
		Check               *jobOverviewCheck   `json:"check,omitempty"`
		Connection          displayConnection   `json:"connection"`
	}{Job: name, State: job.Status, ImageStatus: job.ImageMaterializationStatus, Actions: []jobOverviewAction{}, Connection: connection}
	add := func(purpose, command string) {
		receipt.Actions = append(receipt.Actions, jobOverviewAction{purpose, command})
	}
	command := func(args ...string) string {
		return savedNameSuggestionCommand(append([]string{"jobs"}, args...))
	}
	switch job.ImageMaterializationStatus {
	case "pending", "verifying_legacy":
		add("Wait for image preparation", command("info", name, "--wait-ready"))
	case "ready":
	default:
		add("Inspect image preparation", command("info", name))
	}
	history, err := client.ListJobRunsPage(ctx, name, 1, 0)
	if err != nil {
		return printErr("Could not read latest Job history", err)
	}
	if len(history.Runs) > 0 {
		id := history.Runs[0].ID
		if !jobRunIDPattern.MatchString(id) {
			return printErr("Invalid latest Job run", fmt.Errorf("newest history row did not identify a valid run; no older run selected"))
		}
		run, err := client.GetJobRun(ctx, name, id)
		if err != nil {
			return printErr("Could not read latest Job run", err)
		}
		if run.ID != id || run.JobID != job.ID || run.AccountID != job.AccountID {
			return printErr("Invalid Job run", fmt.Errorf("server returned another run, Job, or account"))
		}
		receipt.LatestRun = &jobOverviewRun{run.ID, run.AggregateStatus, run.Tasks, run.TasksRunning, run.TasksSucceeded, run.TasksFailed, run.TasksCancelled, run.DeadLetterCount}
		add("Inspect task outcomes", command("tasks", name, id, "--all"))
		if run.TasksFailed > 0 || run.TasksCancelled > 0 || run.AggregateStatus == "failed" || run.AggregateStatus == "dead_letter" || run.AggregateStatus == "cancelled" {
			add("Inspect unsuccessful tasks", command("tasks", name, id, "--unsuccessful-only"))
			add("Read unsuccessful-task logs", command("logs", name, id, "--unsuccessful-only"))
		}
		if run.TasksFailed > 0 || run.AggregateStatus == "failed" || run.AggregateStatus == "dead_letter" {
			tasks, err := collectJobTaskPagesContext(ctx, client, name, id, 200, 0, true)
			if err != nil {
				return printErr("Could not inspect failed tasks", err)
			}
			for i, task := range tasks {
				if i == 3 {
					break
				}
				add(fmt.Sprintf("Read task %d logs", task.TaskIndex), command("logs", name, id, "--task", strconv.Itoa(task.TaskIndex)))
			}
		}
		switch run.AggregateStatus {
		case "succeeded":
			add("Collect results", command("results", name, id))
		case "queued", "running":
			add("Wait for this run", command("wait", name, id))
			add("Preview cancellation", command("cancel", name, id, "--dry-run"))
		case "failed", "dead_letter", "cancelled":
			add("Preview replay of unsuccessful tasks", command("replay-failed", name, id, "--dry-run"))
		}
	} else {
		add("Inspect Job configuration", command("info", name))
	}
	receipt.ReadinessAssessment = assessJobOverviewReadiness(receipt.State, receipt.ImageStatus)
	receipt.RunAssessment = assessJobOverviewRun(receipt.LatestRun)
	if *check {
		receipt.Check = checkJobOverview(receipt.State, receipt.ImageStatus, receipt.LatestRun)
	}
	finish := func() int {
		if receipt.Check != nil && !receipt.Check.Passed {
			return printJobReadinessCheckFailure("Job overview check failed", strings.Join(receipt.Check.Reasons, "; "), jobReadinessRecoveryCommands(name, receipt.State, receipt.ImageStatus, receipt.LatestRun, true, receipt.Actions))
		}
		return 0
	}
	if jsonOutput {
		if code := jsonOut(writeJSONSingle(receipt)); code != 0 {
			return code
		}
		return finish()
	}

	_, _ = fmt.Fprintf(osStdout, "Job: %s\nProfile: %s (source=%s)\nAPI: %s (source=%s)\n", name, oneLine(connection.Profile), oneLine(connection.ProfileSource), oneLine(connection.APIBase), oneLine(connection.APISource))
	if connection.AddressRedacted {
		_, _ = fmt.Fprintln(osStdout, "API display omits credentials, query, and fragment; malformed addresses are hidden.")
	}
	_, _ = fmt.Fprintf(osStdout, "Readiness: %s\nState: %s\nImage preparation: %s\nLatest run assessment: %s\n", receipt.ReadinessAssessment, oneLine(receipt.State), oneLine(receipt.ImageStatus), receipt.RunAssessment)
	if receipt.LatestRun == nil {
		_, _ = fmt.Fprintln(osStdout, "Latest run: none recorded")
	} else {
		r := receipt.LatestRun
		_, _ = fmt.Fprintf(osStdout, "Latest run: %s\nRun status: %s\nTasks: %d total; %d running; %d succeeded; %d failed; %d cancelled; %d dead-letter\n", r.ID, oneLine(r.Status), r.Tasks, r.Running, r.Succeeded, r.Failed, r.Cancelled, r.DeadLetter)
	}
	_, _ = fmt.Fprintln(osStdout, "\nNext commands (POSIX shells):")
	for _, action := range receipt.Actions {
		_, _ = fmt.Fprintf(osStdout, "%s:\n  %s\n", action.Purpose, action.Command)
	}
	if receipt.Check != nil && receipt.Check.Passed {
		_, _ = fmt.Fprintln(osStdout, "\nOverview check: passed")
	}
	return finish()
}

func assessJobOverviewReadiness(state, image string) string {
	stateMessage := "Job state is unknown; inspect its configuration."
	switch state {
	case "active":
		stateMessage = "Job is active."
	case "paused":
		stateMessage = "Job is paused."
	}
	imageMessage := "Image readiness is unknown; inspect image preparation."
	switch image {
	case "ready":
		imageMessage = "Image is ready."
	case "pending", "verifying_legacy":
		imageMessage = "Image is preparing; wait for preparation before starting a run."
	case "failed":
		imageMessage = "Image preparation failed; inspect the Job before starting a run."
	}
	return stateMessage + " " + imageMessage
}

func assessJobOverviewRun(run *jobOverviewRun) string {
	if run == nil {
		return "No runs recorded yet."
	}
	switch run.Status {
	case "succeeded":
		return "Run succeeded; collect results if outputs were recorded."
	case "failed":
		return "Run failed; inspect failed tasks and their logs."
	case "dead_letter":
		return "Run reached dead-letter status; inspect failed task outcomes and their logs."
	case "cancelled":
		return "Run was cancelled; inspect task outcomes."
	case "queued", "running":
		message := "Run is queued; wait for this run."
		if run.Status == "running" {
			message = "Run is in progress; wait for this run."
		}
		// Dead-letter counts can overlap failed counts; do not count them twice.
		completed := 0
		valid := run.Tasks > 0
		for _, count := range []int{run.Succeeded, run.Failed, run.Cancelled} {
			if count < 0 || count > run.Tasks-completed {
				valid = false
				break
			}
			completed += count
		}
		if valid {
			message += fmt.Sprintf(" %d of %d tasks finished (succeeded, failed, or cancelled).", completed, run.Tasks)
		}
		return message
	default:
		return "Run status is unknown; inspect task outcomes."
	}
}

func checkJobOverview(state, image string, run *jobOverviewRun) *jobOverviewCheck {
	result := &jobOverviewCheck{Reasons: []string{}}
	if state != "active" {
		result.Reasons = append(result.Reasons, fmt.Sprintf("Job state is %s; requires active", oneLine(state)))
	}
	if image != "ready" {
		result.Reasons = append(result.Reasons, fmt.Sprintf("image preparation is %s; requires ready", oneLine(image)))
	}
	if run == nil {
		result.Reasons = append(result.Reasons, "no runs recorded; requires a succeeded latest run")
	} else if run.Status != "succeeded" {
		result.Reasons = append(result.Reasons, fmt.Sprintf("latest run %s has status %s; requires succeeded", run.ID, oneLine(run.Status)))
	}
	result.Passed = len(result.Reasons) == 0
	return result
}
