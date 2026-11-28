package dashboard

import (
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AppTasksData backs /dashboard/apps/{slug}/tasks: the read-only history of
// an app's one-off commands, release commands and command crons (ADR-230).
type AppTasksData struct {
	Slug    string
	Enabled bool
	Message string
	Tasks   []AppTaskRow
	// Selected is the task opened with ?task=, with its output tails.
	Selected               *AppTaskRow
	Offset, Limit          int
	PrevOffset, NextOffset int
	HasPrev, HasNext       bool
}

// AppTaskRow is one task as the page shows it.
type AppTaskRow struct {
	ID, Status, Kind, Command, ExitCode, CreatedAt, Duration, DeploymentID string
	Failure, StdoutTail, StderrTail                                        string
	OutputTruncated, Terminal, Interactive                                 bool
	MaxOutputBytes                                                         int
}

// NewAppTaskRow formats an API task for the page.
func NewAppTaskRow(task api.AppTaskResponse) AppTaskRow {
	row := AppTaskRow{
		ID: task.ID, Status: string(task.Status), Kind: string(task.Kind), DeploymentID: task.DeploymentID,
		CreatedAt: task.CreatedAt, ExitCode: "-", Duration: "-",
		StdoutTail: task.StdoutTail, StderrTail: task.StderrTail, OutputTruncated: task.OutputTruncated,
		Terminal: task.Status.Terminal(), Interactive: task.Interactive, MaxOutputBytes: task.MaxOutputBytes,
	}
	if created, err := time.Parse(time.RFC3339Nano, task.CreatedAt); err == nil {
		row.CreatedAt = created.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	if task.CommandShell && len(task.Command) == 1 {
		row.Command = task.Command[0]
	} else {
		row.Command = strings.Join(task.Command, " ")
	}
	if task.ExitCode != nil {
		row.ExitCode = strconv.Itoa(*task.ExitCode)
	}
	if task.Failure != nil {
		row.Failure = task.Failure.Code + ": " + task.Failure.Message
	}
	if task.StartedAt != nil && task.FinishedAt != nil {
		started, err1 := time.Parse(time.RFC3339Nano, *task.StartedAt)
		finished, err2 := time.Parse(time.RFC3339Nano, *task.FinishedAt)
		if err1 == nil && err2 == nil {
			row.Duration = finished.Sub(started).Round(100 * time.Millisecond).String()
		}
	}
	return row
}
