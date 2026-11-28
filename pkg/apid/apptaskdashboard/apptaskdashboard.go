// Package apptaskdashboard assembles the dashboard's read-only history of an
// app's tasks (ADR-230): one-off commands, release commands and command crons.
// apidserver keeps the route, session auth and app lookup.
package apptaskdashboard

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// PageSize is how many tasks one history page shows.
const PageSize = 25

// Store is the part of the state store the page reads.
type Store interface {
	AppTaskByID(ctx context.Context, accountID, appID, taskID string) (state.AppTask, error)
	ListAppTasks(ctx context.Context, accountID, appID string, limit, offset int) ([]state.AppTask, error)
}

// Request names the app and the page asked for.
type Request struct {
	AccountID, AppID, Slug string
	// Enabled is the app-task API gate; off shows the page without history.
	Enabled bool
	// Query carries ?task= (one task) and ?offset= (a history page).
	Query url.Values
	// Response formats a stored task as the API does, which also hides the
	// output of tasks whose output has its own scoped reader.
	Response func(state.AppTask) api.AppTaskResponse
}

// Build reads the page's data. Read failures become a message on the page,
// never an error: the page still renders.
func Build(ctx context.Context, store Store, req Request) dashboard.AppTasksData {
	data := dashboard.AppTasksData{Slug: req.Slug, Enabled: req.Enabled, Limit: PageSize}
	if !req.Enabled {
		return data
	}
	if id := req.Query.Get("task"); id != "" {
		if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
			data.Message = "That task ID is not valid."
		} else if task, err := store.AppTaskByID(ctx, req.AccountID, req.AppID, id); errors.Is(err, state.ErrNotFound) {
			data.Message = "That task was not found for this app."
		} else if err != nil {
			data.Message = "Task details are unavailable right now; try again shortly."
		} else {
			row := dashboard.NewAppTaskRow(req.Response(task))
			data.Selected = &row
			return data
		}
	}
	if raw := req.Query.Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			data.Offset = parsed
		}
	}
	// One extra row says whether an older page exists.
	rows, err := store.ListAppTasks(ctx, req.AccountID, req.AppID, data.Limit+1, data.Offset)
	if err != nil {
		if data.Message == "" {
			data.Message = "Task history is unavailable right now; try again shortly."
		}
		return data
	}
	if len(rows) > data.Limit {
		rows, data.HasNext, data.NextOffset = rows[:data.Limit], true, data.Offset+data.Limit
	}
	for _, task := range rows {
		data.Tasks = append(data.Tasks, dashboard.NewAppTaskRow(req.Response(task)))
	}
	if data.Offset > 0 {
		data.HasPrev, data.PrevOffset = true, max(0, data.Offset-data.Limit)
	}
	return data
}
