package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type upcomingJob struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Kind             string                     `json:"kind"`
	State            string                     `json:"state"`
	Schedule         string                     `json:"schedule"`
	Timezone         string                     `json:"timezone"`
	ImageStatus      string                     `json:"image_status"`
	NextExpressionAt *time.Time                 `json:"next_expression_at,omitempty"`
	SchedulePolicy   *workpolicy.SchedulePolicy `json:"schedule_policy,omitempty"`
	ExecutionNote    string                     `json:"execution_note"`
	Error            string                     `json:"error,omitempty"`
}

func cmdJobsNext(args []string) int {
	if len(args) != 0 {
		return printErr("Invalid arguments", errors.New("use jobs next with optional global --json"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	jobs, _, err := collectOffsetPages(ctx, 0, true, func(ctx context.Context, offset int) ([]api.JobResponse, int, error) {
		page, err := client.ListJobs(ctx, 200, offset)
		return page.Jobs, page.NextOffset, err
	})
	if err != nil {
		return printErr("Could not list Jobs", err)
	}
	at := time.Now().UTC()
	items := make([]upcomingJob, 0, len(jobs))
	seen := map[string]bool{}
	invalid := false
	for _, job := range jobs {
		if !jobRunIDPattern.MatchString(job.ID) || !jobSlugPattern.MatchString(job.Name) || seen[job.ID] {
			return printErr("Invalid Job list", errors.New("a Job has an invalid or repeated identity"))
		}
		seen[job.ID] = true
		item := buildUpcomingJob(job, at)
		if item.Error != "" {
			invalid = true
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i].NextExpressionAt, items[j].NextExpressionAt
		if a == nil && b != nil {
			return false
		}
		if a != nil && b == nil {
			return true
		}
		if a != nil && b != nil && !a.Equal(*b) {
			return a.Before(*b)
		}
		return items[i].Name < items[j].Name
	})
	if jsonOutput {
		if code := jsonOut(writeJSONSingle(struct {
			AsOf  time.Time     `json:"as_of"`
			Items []upcomingJob `json:"items"`
		}{at, items})); code != 0 {
			return code
		}
	} else {
		if len(items) == 0 {
			PrintProgress(osStdout, "No Jobs available.")
			return 0
		}
		table := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(table, "JOB\tSTATE\tKIND\tNEXT EXPRESSION TIME\tTIMEZONE\tIMAGE")
		for _, item := range items {
			next := "-"
			if item.NextExpressionAt != nil {
				next = item.NextExpressionAt.Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(item.Name), oneLine(item.State), oneLine(item.Kind), next, oneLine(item.Timezone), oneLine(item.ImageStatus))
		}
		if err := table.Flush(); err != nil {
			return printErr("Could not write upcoming Jobs", err)
		}
		for _, item := range items {
			if item.Error != "" {
				PrintProgress(osStdout, "%s: %s", item.Name, item.Error)
			}
			if item.NextExpressionAt == nil || item.ImageStatus != "ready" || item.SchedulePolicy != nil {
				PrintProgress(osStdout, "%s: %s", item.Name, item.ExecutionNote)
			}
		}
		PrintProgress(osStdout, "Nominal expression times use each Job's timezone. Image readiness, overlap, deadlines, missed-run policy, and execution delays can change whether or when a run starts.")
	}
	if invalid {
		return 1
	}
	return 0
}

func buildUpcomingJob(job api.JobResponse, at time.Time) upcomingJob {
	item := upcomingJob{ID: job.ID, Name: job.Name, Kind: job.Kind, State: job.Status, Schedule: job.Schedule, Timezone: job.Timezone, ImageStatus: job.ImageMaterializationStatus, SchedulePolicy: job.SchedulePolicy, ExecutionNote: "Nominal schedule candidate; actual execution is subject to scheduling policy and image readiness."}
	if job.Kind == "batch" && job.Schedule == "" {
		item.ExecutionNote = "Batch Job; no recurring schedule."
		return item
	}
	if job.Kind != "recurring" || job.Schedule == "" {
		item.Error = "Job kind and recurring schedule are inconsistent."
		return item
	}
	if job.Timezone == "Local" {
		item.Error = "Host-local timezone cannot be reliably previewed; set an explicit IANA timezone."
		return item
	}
	timezone, err := cronexpr.NormalizeTimezone(job.Timezone)
	if err != nil {
		item.Error = "Invalid Job timezone."
		return item
	}
	item.Timezone = timezone
	schedule, err := cronexpr.Parse(job.Schedule, timezone)
	if err != nil {
		item.Error = "Invalid recurring expression."
		return item
	}
	if job.Status != "active" {
		item.ExecutionNote = "Job is " + job.Status + "; no upcoming dispatch is implied."
		return item
	}
	next := schedule.Next(at)
	if next.IsZero() {
		item.Error = "No future expression time."
		return item
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		item.Error = "Timezone could not be loaded."
		return item
	}
	next = next.In(location)
	item.NextExpressionAt = &next
	return item
}
