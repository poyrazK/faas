package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type upcomingCron struct {
	ID               string                     `json:"id"`
	Kind             string                     `json:"kind"`
	Target           string                     `json:"target"`
	Schedule         string                     `json:"schedule"`
	Timezone         string                     `json:"timezone"`
	State            string                     `json:"state"`
	Enabled          bool                       `json:"enabled"`
	SuspendedReason  string                     `json:"suspended_reason,omitempty"`
	NextExpressionAt *time.Time                 `json:"next_expression_at,omitempty"`
	SchedulePolicy   *workpolicy.SchedulePolicy `json:"schedule_policy,omitempty"`
	SkipIfRunning    bool                       `json:"skip_if_running"`
	ExecutionNote    string                     `json:"execution_note"`
	Error            string                     `json:"error,omitempty"`
}

func cmdCronsNext(args []string) int {
	fs := newFlagSet("crons next", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug (linked app or interactive picker by default)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, err := resolveReadAppTarget(*appFlag)
	if err != nil {
		return readAppTargetError(err)
	}
	if !validCLISlug(slug) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not read app", err)
	}
	tasks, err := client.ListCrons(ctx, slug)
	if err != nil {
		return printErr("Could not list scheduled tasks", err)
	}
	at := time.Now().UTC()
	items := make([]upcomingCron, 0, len(tasks))
	invalid := false
	for _, task := range tasks {
		if task.AppID != app.ID || !cronIDPattern.MatchString(task.ID) {
			return printErr("Invalid task list", errors.New("a task does not match the selected app or has an invalid ID"))
		}
		item := buildUpcomingCron(task, at)
		if item.Error != "" {
			invalid = true
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i].NextExpressionAt, items[j].NextExpressionAt
		if left == nil && right != nil {
			return false
		}
		if left != nil && right == nil {
			return true
		}
		if left != nil && right != nil && !left.Equal(*right) {
			return left.Before(*right)
		}
		return items[i].ID < items[j].ID
	})
	if jsonOutput {
		if code := jsonOut(writeJSON(struct {
			App   string         `json:"app_slug"`
			AsOf  time.Time      `json:"as_of"`
			Items []upcomingCron `json:"items"`
		}{slug, at, items})); code != 0 {
			return code
		}
	} else {
		PrintProgress(osStdout, "Upcoming schedules for %s (each time uses its task timezone):", slug)
		if len(items) == 0 {
			PrintProgress(osStdout, "No scheduled tasks.")
		} else {
			_, _ = fmt.Fprintf(osStdout, "%-36s %-10s %-30s %-24s %-8s %s\n", "ID", "STATE", "NEXT EXPRESSION TIME", "TIMEZONE", "KIND", "TARGET")
			for _, item := range items {
				next := "—"
				if item.NextExpressionAt != nil {
					next = item.NextExpressionAt.Format(time.RFC3339)
				}
				_, _ = fmt.Fprintf(osStdout, "%-36s %-10s %-30s %-24s %-8s %s\n", item.ID, item.State, next, oneLine(item.Timezone), item.Kind, oneLine(item.Target))
				if item.State != "enabled" || item.SchedulePolicy != nil || item.SkipIfRunning || item.Error != "" {
					_, _ = fmt.Fprintln(osStdout, "  "+oneLine(item.ExecutionNote))
				}
				if item.Error != "" {
					_, _ = fmt.Fprintln(osStdout, "  Error: "+item.Error)
				}
			}
			PrintProgress(osStdout, "Expression times are schedule candidates; overlap, deadlines, missed-run policies, and execution delays can change whether or when a task runs.")
		}
	}
	if invalid {
		return 1
	}
	return 0
}

func buildUpcomingCron(task api.CronResponse, at time.Time) upcomingCron {
	item := upcomingCron{ID: task.ID, Kind: cronKindOrHTTP(task.Kind), Target: task.Path,
		Schedule: task.Schedule, Timezone: task.Timezone, Enabled: task.Enabled,
		SuspendedReason: task.SuspendedReason, SchedulePolicy: task.SchedulePolicy,
		SkipIfRunning: task.SkipIfRunning, State: "enabled",
		ExecutionNote: "Schedule candidate; actual execution may be delayed."}
	if task.Kind == "command" {
		item.Target = formatCronCommand(task)
	}
	if !task.Enabled {
		item.State = "disabled"
		item.ExecutionNote = "Disabled; no upcoming execution."
	} else if task.SuspendedReason != "" {
		item.State = "suspended"
		item.ExecutionNote = "Suspended: " + task.SuspendedReason
	} else if task.SchedulePolicy != nil || task.SkipIfRunning {
		item.ExecutionNote = "Policy-dependent candidate; overlap, deadline, or missed-run decisions may skip or delay execution."
	}
	// Inactive tasks remain visible without suggesting they will fire.
	if task.Timezone == "Local" {
		item.Error = "Task uses host-local timezone; set an explicit IANA timezone to preview reliably."
		return item
	}
	timezone, err := cronexpr.NormalizeTimezone(task.Timezone)
	if err != nil {
		item.Error = "Invalid task timezone."
		return item
	}
	item.Timezone = timezone
	schedule, err := cronexpr.Parse(task.Schedule, timezone)
	if err != nil {
		item.Error = "Invalid task schedule or no future expression time."
		return item
	}
	if item.State != "enabled" {
		return item
	}
	next := schedule.Next(at)
	if next.IsZero() {
		item.Error = "No future expression time."
		return item
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		item.Error = "Could not load task timezone."
		return item
	}
	next = next.In(location)
	item.NextExpressionAt = &next
	return item
}
